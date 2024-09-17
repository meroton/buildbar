package buildevents_test

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
)

func TestBufferedBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	besBackend := mock.NewMockBuildEventServer(ctrl)

	server, err := buildevents.NewBufferedServer(besBackend, 2)
	require.NoError(t, err)

	t.Run("PublishBuildToolEventStream", func(t *testing.T) {
		backendRequests := make(chan *build.PublishBuildToolEventStreamRequest, 0)

		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		streamBackend.EXPECT().Send(gomock.Any()).DoAndReturn(func(req *build.PublishBuildToolEventStreamRequest) error {
			backendRequests <- req
			return nil
		}).Times(7)
		closeSendDone := make(chan struct{})
		streamBackend.EXPECT().CloseSend().DoAndReturn(func() {
			close(closeSendDone)
		}).Times(1)
		streamBackend.EXPECT().Recv().Return(nil).Times(7)

		besBackend.EXPECT().PublishBuildToolEventStream(gomock.Any()).Return(streamBackend, nil)

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)
		// Should be able to post two events.
		request1 := &build.PublishBuildToolEventStreamRequest{}
		request2 := &build.PublishBuildToolEventStreamRequest{}
		stream.Send(request1)
		stream.Send(request2)
		require.Equal(t, request1, <-backendRequests)
		require.Equal(t, request2, <-backendRequests)

		// Don't let missing Recv() block requests.
		for i := 0; i < 5; i++ {
			require.NoError(t, stream.Send(request2))
			require.Equal(t, request2, <-backendRequests)
		}
		stream.CloseSend()
		<-closeSendDone
		for i := 0; i < 7; i++ {
			require.NoError(t, stream.Recv())
		}
	})
}
