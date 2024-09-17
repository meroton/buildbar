package buildevents_test

import (
	"context"
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

func TestAnnotatedBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	besBackend := mock.NewMockBuildEventServer(ctrl)

	server := buildevents.NewAnnotatedServer(besBackend, "my-label")

	t.Run("PublishLifecycleEventNoError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(ctx, request).Return(nil)
		err := server.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishLifecycleEventError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(ctx, request).Return(status.Error(codes.Internal, "Bad"))
		err := server.PublishLifecycleEvent(ctx, request)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "my-label: Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamNoError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		request := &build.PublishBuildToolEventStreamRequest{}

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(request).Return(nil)
		err = stream.Send(request)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(nil)
		err = stream.Recv()
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamError", func(t *testing.T) {
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(nil, status.Error(codes.Internal, "Bad"))

		_, err := server.PublishBuildToolEventStream(ctx)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "my-label: Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamSendError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		streamBackend.EXPECT().Send(gomock.Any()).Return(status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		err = stream.Send(nil)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "my-label: Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamRecvError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		streamBackend.EXPECT().Recv().Return(status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "my-label: Bad"), err)
	})
}
