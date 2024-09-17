package buildevents_test

import (
	"context"
	"io"
	"testing"

	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMultiplexingBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	besBackend1 := mock.NewMockBuildEventServer(ctrl)
	besBackend2 := mock.NewMockBuildEventServer(ctrl)

	server := buildevents.NewMultiplexingServer(
		[]buildevents.BuildEventServer{besBackend1, besBackend2},
	)

	t.Run("PublishLifecycleEventNoError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend1.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(nil)
		besBackend2.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(nil)
		err := server.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishLifecycleEventError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend1.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(nil)
		besBackend2.EXPECT().PublishLifecycleEvent(gomock.Any(), request).Return(status.Error(codes.Internal, "Backend2 broken"))
		err := server.PublishLifecycleEvent(ctx, request)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Backend2 broken"), err)
	})

	t.Run("PublishBuildToolEventStream", func(t *testing.T) {
		streamBackend1 := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend1.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend1, nil)
		streamBackend2 := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend2.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend2, nil)

		// The ackResponses channels are unbuffered.
		ackResponses1 := make(chan struct{}, 0)
		streamBackend1.EXPECT().Recv().DoAndReturn(func() error {
			_, ok := <-ackResponses1
			if !ok {
				return io.EOF
			}
			return nil
		}).Times(3)
		ackResponses2 := make(chan struct{}, 0)
		streamBackend2.EXPECT().Recv().DoAndReturn(func() error {
			_, ok := <-ackResponses2
			if !ok {
				return io.EOF
			}
			return nil
		}).Times(3)

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		requestA := &build.PublishBuildToolEventStreamRequest{}
		requestB := &build.PublishBuildToolEventStreamRequest{}

		requestQueue1 := make(chan *build.PublishBuildToolEventStreamRequest, 1)
		streamBackend1.EXPECT().Send(gomock.Any()).DoAndReturn(func(req *build.PublishBuildToolEventStreamRequest) error {
			requestQueue1 <- req
			return nil
		}).Times(2)

		requestQueue2 := make(chan *build.PublishBuildToolEventStreamRequest, 1)
		streamBackend2.EXPECT().Send(gomock.Any()).DoAndReturn(func(req *build.PublishBuildToolEventStreamRequest) error {
			requestQueue2 <- req
			return nil
		}).Times(2)

		// Let backend 1 receive both requests before backend 2.
		require.NoError(t, stream.Send(requestA))
		require.Equal(t, requestA, <-requestQueue1)
		// The requestQueue2 buffer is full.
		send2Done := make(chan struct{})
		go func() {
			require.NoError(t, stream.Send(requestB))
			close(send2Done)
		}()
		require.Equal(t, requestB, <-requestQueue1)
		// Release the send call.
		require.Equal(t, requestA, <-requestQueue2)
		<-send2Done
		require.Equal(t, requestB, <-requestQueue2)

		// Backend 1 acks event by event.
		// Backend 2 acks all events at the end.
		streamBackend1.EXPECT().CloseSend()
		streamBackend2.EXPECT().CloseSend()
		stream.CloseSend()

		ackResponses1 <- struct{}{}
		ackResponses1 <- struct{}{} // Unbuffered.
		close(ackResponses1)
		ackResponses2 <- struct{}{}
		require.NoError(t, stream.Recv())
		ackResponses2 <- struct{}{}
		close(ackResponses2)
		require.NoError(t, stream.Recv())
		require.Error(t, io.EOF, stream.Recv())
	})
}
