package buildevents_test

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGrpcBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	// Create an RPC server/client pair.
	l := bufconn.Listen(1 << 20)
	besBackend := mock.NewMockBuildEventServer(ctrl)
	server := grpc.NewServer()
	build.RegisterPublishBuildEventServer(server, buildevents.NewGrpcServer(besBackend))
	go func() {
		require.NoError(t, server.Serve(l))
	}()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return l.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer server.Stop()
	defer conn.Close()
	client := build.NewPublishBuildEventClient(conn)

	t.Run("PublishLifecycleEventNoError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(nil)
		_, err := client.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishLifecycleEventError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(status.Error(codes.Internal, "Bad"))
		_, err := client.PublishLifecycleEvent(ctx, request)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamNoError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		request := &build.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &build.OrderedBuildEvent{
				SequenceNumber: 42,
				StreamId: &build.StreamId{
					InvocationId: "invocation",
				},
			},
		}

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(gomock.Any()).Return(streamBackend, nil)
		stream, err := client.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(gomock.Any()).DoAndReturn(func(req *build.PublishBuildToolEventStreamRequest) error {
			testutil.RequireEqualProto(t, request, req)
			return nil
		})
		require.NoError(t, stream.Send(request))
		streamBackend.EXPECT().CloseSend().Return()
		require.NoError(t, stream.CloseSend())

		streamBackend.EXPECT().Recv().Return(nil)
		streamBackend.EXPECT().Recv().Return(io.EOF)
		response, err := stream.Recv()
		require.NoError(t, err)
		require.Equal(t, int64(42), response.SequenceNumber)
		testutil.RequireEqualProto(t, &build.StreamId{
			InvocationId: "invocation",
		}, response.StreamId)
	})

	t.Run("PublishBuildToolEventStreamError", func(t *testing.T) {
		besBackend.EXPECT().PublishBuildToolEventStream(gomock.Any()).Return(nil, status.Error(codes.Internal, "Bad"))

		stream, err := client.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)
		_, err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamSendError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(gomock.Any()).Return(streamBackend, nil)
		streamBackend.EXPECT().Send(gomock.Any()).Return(status.Error(codes.Internal, "Bad"))

		stream, err := client.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(nil))
		_, err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamRecvError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(gomock.Any()).Return(streamBackend, nil)
		streamBackend.EXPECT().Send(gomock.Any()).Return(nil)
		streamBackend.EXPECT().CloseSend().Return().MaxTimes(1)
		streamBackend.EXPECT().Recv().Return(status.Error(codes.Internal, "Bad"))

		stream, err := client.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(nil))
		_, err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})
}
