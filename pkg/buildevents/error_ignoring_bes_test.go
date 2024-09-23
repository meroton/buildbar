package buildevents_test

import (
	"context"
	"testing"

	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorIgnoringBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	besBackend := mock.NewMockBuildEventServer(ctrl)
	errorLogger := mock.NewMockErrorLogger(ctrl)

	server := buildevents.NewErrorIgnoringServer(besBackend, errorLogger)

	t.Run("PublishLifecycleEventNoError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(ctx, request).Return(nil)
		err := server.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishLifecycleEventError", func(t *testing.T) {
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			require.Error(t, err, "Ignoring backend error: Bad")
		})

		request := &build.PublishLifecycleEventRequest{}
		besBackend.EXPECT().PublishLifecycleEvent(ctx, request).Return(status.Error(codes.Internal, "Bad"))
		err := server.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamNoError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		request := &buildevents.BufferedPublishBuildToolEventStreamRequest{}
		streamBackend.EXPECT().Send(request).Return(nil)
		err = stream.Send(request)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(nil)
		err = stream.Recv()
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamOrderedAcks", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		responses := make(chan struct{}, 10)
		backendRecvCalled := make(chan struct{}, 10)
		request := &buildevents.BufferedPublishBuildToolEventStreamRequest{}
		streamBackend.EXPECT().Send(request).Return(nil).Times(3)
		streamBackend.EXPECT().Recv().DoAndReturn(func() error {
			backendRecvCalled <- struct{}{}
			return nil
		}).Times(3)

		// Multiple sends should be allowed before calling Recv().
		for i := 0; i < 2; i++ {
			err = stream.Send(request)
			require.NoError(t, err)
		}

		go func() {
			for i := 0; i < 3; i++ {
				err := stream.Recv()
				require.NoError(t, err)
				responses <- struct{}{}
			}
			close(responses)
		}()

		<-backendRecvCalled
		<-backendRecvCalled
		<-responses
		<-responses
		<-backendRecvCalled
		select {
		case <-responses:
			require.FailNow(t, "Received response 3 before sending request 3")
		default:
			// noop
		}
		err = stream.Send(request)
		require.NoError(t, err)

		_, ok := <-responses
		require.True(t, ok)
		_, ok = <-responses
		require.False(t, ok)
	})

	t.Run("PublishBuildToolEventStreamError", func(t *testing.T) {
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			require.Error(t, err, "Ignoring PublishBuildToolEventStream error: Bad")
		})

		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(nil, status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		request := &buildevents.BufferedPublishBuildToolEventStreamRequest{}
		err = stream.Send(request)
		require.NoError(t, err)

		err = stream.Recv()
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamSendError", func(t *testing.T) {
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			require.Error(t, err, "Ignoring BuildToolEventStream.Send error: Bad")
		})

		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		request := &buildevents.BufferedPublishBuildToolEventStreamRequest{}
		streamBackend.EXPECT().Send(request).Return(status.Error(codes.Internal, "Bad"))
		err = stream.Send(request)
		require.NoError(t, err)
		// The backend should not be used again.
		err = stream.Send(request)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(nil).MaxTimes(2)
		err = stream.Recv()
		require.NoError(t, err)
		err = stream.Recv()
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamRecvError", func(t *testing.T) {
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			require.Error(t, err, "Ignoring BuildToolEventStream.Recv error: Bad")
		})

		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		request := &buildevents.BufferedPublishBuildToolEventStreamRequest{}
		streamBackend.EXPECT().Send(request).Return(nil).Times(2)
		err = stream.Send(request)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(status.Error(codes.Internal, "Bad"))
		err = stream.Recv()
		require.NoError(t, err)

		err = stream.Send(request)
		require.NoError(t, err)
		// The backend should not be used again.
		err = stream.Recv()
		require.NoError(t, err)
	})
}
