package buildevents_test

import (
	"context"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	bazel_protobuf "github.com/bazelbuild/bazel/src/main/protobuf"
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

func TestRedactingBuildEventServer(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	besBackend := mock.NewMockBuildEventServer(ctrl)

	server := buildevents.NewRedactingBuildEventServer(besBackend, []string{"MY_ENV"})

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
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Redacting: Bad"), err)
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

	t.Run("RedactingEvent", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		inputBazelEvent := &buildeventstream.BuildEvent{
			Payload: &buildeventstream.BuildEvent_StructuredCommandLine{
				StructuredCommandLine: &bazel_protobuf.CommandLine{
					Sections: []*bazel_protobuf.CommandLineSection{
						{
							SectionType: &bazel_protobuf.CommandLineSection_OptionList{
								OptionList: &bazel_protobuf.OptionList{
									Option: []*bazel_protobuf.Option{
										{
											CombinedForm: "hello",
											OptionName:   "client_env",
											OptionValue:  "MY_ENV=allowed",
										},
										{
											CombinedForm: "--client_env=SECRET_ENV=secret",
											OptionName:   "client_env",
											OptionValue:  "SECRET_ENV=secret",
										},
									},
								},
							},
						},
					},
				},
			},
		}
		redactedBazelEvent := &buildeventstream.BuildEvent{
			Payload: &buildeventstream.BuildEvent_StructuredCommandLine{
				StructuredCommandLine: &bazel_protobuf.CommandLine{
					Sections: []*bazel_protobuf.CommandLineSection{
						{
							SectionType: &bazel_protobuf.CommandLineSection_OptionList{
								OptionList: &bazel_protobuf.OptionList{
									Option: []*bazel_protobuf.Option{
										{
											CombinedForm: "hello",
											OptionName:   "client_env",
											OptionValue:  "MY_ENV=allowed",
										},
										{
											CombinedForm: "--client_env=SECRET_ENV=<REDACTED>",
											OptionName:   "client_env",
											OptionValue:  "SECRET_ENV=<REDACTED>",
										},
									},
								},
							},
						},
					},
				},
			},
		}
		bazelEventData, err := anypb.New(inputBazelEvent)
		require.NoError(t, err)
		request := &build.PublishBuildToolEventStreamRequest{
			ProjectId: "my-instance",
			OrderedBuildEvent: &build.OrderedBuildEvent{
				Event: &build.BuildEvent{
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventData,
					},
				},
			},
		}

		// Check that the channels are passed straight through.
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		streamBackend.EXPECT().Send(gomock.Any()).DoAndReturn(func(request *build.PublishBuildToolEventStreamRequest) error {
			var actualBazelEvent buildeventstream.BuildEvent
			require.NoError(t, request.OrderedBuildEvent.GetEvent().GetBazelEvent().UnmarshalTo(&actualBazelEvent))
			testutil.RequireEqualProto(t, redactedBazelEvent, &actualBazelEvent)
			return nil
		}).Times(1)
		err = stream.Send(request)
		require.NoError(t, err)

		streamBackend.EXPECT().Recv().Return(nil)
		err = stream.Recv()
		require.NoError(t, err)
	})

	t.Run("PublishBuildToolEventStreamError", func(t *testing.T) {
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(nil, status.Error(codes.Internal, "Bad"))

		_, err := server.PublishBuildToolEventStream(ctx)
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Redacting: Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamSendError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		streamBackend.EXPECT().Send(gomock.Any()).Return(status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		err = stream.Send(&build.PublishBuildToolEventStreamRequest{})
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Redacting: Bad"), err)
	})

	t.Run("PublishBuildToolEventStreamRecvError", func(t *testing.T) {
		streamBackend := mock.NewMockBuildToolEventStreamClient(ctrl)
		besBackend.EXPECT().PublishBuildToolEventStream(ctx).Return(streamBackend, nil)
		streamBackend.EXPECT().Recv().Return(status.Error(codes.Internal, "Bad"))

		stream, err := server.PublishBuildToolEventStream(ctx)
		require.NoError(t, err)

		err = stream.Recv()
		testutil.RequireEqualStatus(t, status.Error(codes.Internal, "Redacting: Bad"), err)
	})
}
