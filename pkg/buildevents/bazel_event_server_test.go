package buildevents_test

import (
	"context"
	"io"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/golang/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestBazelEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	bazelBackend := mock.NewMockBazelEventServer(ctrl)

	server := buildevents.NewBazelBuildEventServer(bazelBackend)

	t.Run("PublishLifecycleEventNoError", func(t *testing.T) {
		request := &build.PublishLifecycleEventRequest{}
		err := server.PublishLifecycleEvent(ctx, request)
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamNoError", func(t *testing.T) {
		streamBackend := mock.NewMockBazelEventStreamClient(ctrl)
		streamId := &build.StreamId{}
		bazelEvent1 := &buildeventstream.BuildEvent{}
		bazelEventData1, err := anypb.New(bazelEvent1)
		require.NoError(t, err)
		err = bazelEventData1.UnmarshalTo(bazelEvent1)
		require.NoError(t, err)
		request1 := &build.PublishBuildToolEventStreamRequest{
			ProjectId: "my-instance",
			OrderedBuildEvent: &build.OrderedBuildEvent{
				StreamId: streamId,
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData1,
					},
				},
			},
		}
		bazelEvent2 := &buildeventstream.BuildEvent{
			LastMessage: true,
		}
		bazelEventData2, err := anypb.New(bazelEvent2)
		require.NoError(t, err)
		err = bazelEventData2.UnmarshalTo(bazelEvent2)
		require.NoError(t, err)
		request2 := &build.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &build.OrderedBuildEvent{
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData2,
					},
				},
			},
		}

		bazelBackend.EXPECT().
			PublishBazelEvents(gomock.Any(), digest.MustNewInstanceName("my-instance"), streamId).
			Return(streamBackend, nil)

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		// Send a non-Bazel event.
		err = stream.Send(&build.PublishBuildToolEventStreamRequest{})
		require.NoError(t, err)
		err = stream.Recv()
		require.NoError(t, err)

		// Send Bazel events.
		streamBackend.EXPECT().Send(bazelEvent1).Return(nil)
		err = stream.Send(request1)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(bazelEvent2).Return(nil)
		err = stream.Send(request2)
		require.NoError(t, err)

		stream.CloseSend()

		streamBackend.EXPECT().Recv().Return(nil)
		err = stream.Recv()
		require.NoError(t, err)
		streamBackend.EXPECT().Recv().Return(nil)
		err = stream.Recv()
		require.NoError(t, err)
		err = stream.Recv()
		require.Error(t, io.EOF, err)
	})

	t.Run("PublishBuildToolEventStreamError", func(t *testing.T) {
		streamId := &build.StreamId{}
		bazelEvent1 := &buildeventstream.BuildEvent{}
		bazelEventData1, err := anypb.New(bazelEvent1)
		require.NoError(t, err)
		err = bazelEventData1.UnmarshalTo(bazelEvent1)
		require.NoError(t, err)
		request1 := &build.PublishBuildToolEventStreamRequest{
			ProjectId: "my-instance",
			OrderedBuildEvent: &build.OrderedBuildEvent{
				StreamId: streamId,
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData1,
					},
				},
			},
		}

		bazelBackend.EXPECT().
			PublishBazelEvents(gomock.Any(), digest.MustNewInstanceName("my-instance"), streamId).
			Return(nil, status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		// Send a non-Bazel event.
		err = stream.Send(&build.PublishBuildToolEventStreamRequest{})
		require.NoError(t, err)
		err = stream.Recv()
		require.NoError(t, err)

		err = stream.Send(request1)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Failed to create Bazel event stream: Bad"), err)
	})
	t.Run("PublishBazelEventSendError", func(t *testing.T) {
		streamBackend := mock.NewMockBazelEventStreamClient(ctrl)
		streamId := &build.StreamId{}
		bazelEvent1 := &buildeventstream.BuildEvent{}
		bazelEventData1, err := anypb.New(bazelEvent1)
		require.NoError(t, err)
		err = bazelEventData1.UnmarshalTo(bazelEvent1)
		require.NoError(t, err)
		request1 := &build.PublishBuildToolEventStreamRequest{
			ProjectId: "my-instance",
			OrderedBuildEvent: &build.OrderedBuildEvent{
				StreamId: streamId,
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData1,
					},
				},
			},
		}

		bazelBackend.EXPECT().
			PublishBazelEvents(gomock.Any(), digest.MustNewInstanceName("my-instance"), streamId).
			Return(streamBackend, nil)

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(bazelEvent1).Return(status.Error(codes.Internal, "Bad"))
		err = stream.Send(request1)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})

	t.Run("PublishBazelEventSendError", func(t *testing.T) {
		streamBackend := mock.NewMockBazelEventStreamClient(ctrl)
		streamId := &build.StreamId{}
		bazelEvent1 := &buildeventstream.BuildEvent{}
		bazelEventData1, err := anypb.New(bazelEvent1)
		require.NoError(t, err)
		err = bazelEventData1.UnmarshalTo(bazelEvent1)
		require.NoError(t, err)
		request1 := &build.PublishBuildToolEventStreamRequest{
			ProjectId: "my-instance",
			OrderedBuildEvent: &build.OrderedBuildEvent{
				StreamId: streamId,
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData1,
					},
				},
			},
		}

		bazelBackend.EXPECT().
			PublishBazelEvents(gomock.Any(), digest.MustNewInstanceName("my-instance"), streamId).
			Return(streamBackend, nil)

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(bazelEvent1).Return(nil)
		err = stream.Send(request1)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(status.Error(codes.Internal, "Bad"))
		err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Bad"), err)
	})
}
