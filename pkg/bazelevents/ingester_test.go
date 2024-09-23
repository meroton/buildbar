package bazelevents_test

import (
	"context"
	"errors"
	"io"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBazelEventsIngester(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	converter := mock.NewMockBazelEventConverter(ctrl)
	converterFactory := func(errorLogger util.ErrorLogger) bazelevents.BazelEventConverter {
		return converter
	}
	uploader := mock.NewMockUploader(ctrl)
	errorLogger := mock.NewMockErrorLogger(ctrl)

	eventTime1 := &timestamppb.Timestamp{
		Seconds: 123,
	}
	eventTime2 := &timestamppb.Timestamp{
		Seconds: 456,
	}

	bazelEventStarted := &buildeventstream.BuildEvent{
		Payload: &buildeventstream.BuildEvent_Started{
			Started: &buildeventstream.BuildStarted{},
		},
	}
	bazelEventStartedAnypb, err := anypb.New(bazelEventStarted)
	require.NoError(t, err)
	buildEventStarted := &buildevents.BufferedPublishBuildToolEventStreamRequest{
		PublishBuildToolEventStreamRequest: &build.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &build.OrderedBuildEvent{
				StreamId: &build.StreamId{
					InvocationId: "my_invocation_id",
				},
				Event: &build.BuildEvent{
					EventTime: eventTime1,
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventStartedAnypb,
					},
				},
			},
			ProjectId: "my-instance",
		},
	}

	bazelEventFinished := &buildeventstream.BuildEvent{
		Payload: &buildeventstream.BuildEvent_Finished{
			Finished: &buildeventstream.BuildFinished{},
		},
		LastMessage: true,
	}
	bazelEventFinishedAnypb, err := anypb.New(bazelEventFinished)
	require.NoError(t, err)
	buildEventFinished := &buildevents.BufferedPublishBuildToolEventStreamRequest{
		PublishBuildToolEventStreamRequest: &build.PublishBuildToolEventStreamRequest{
			OrderedBuildEvent: &build.OrderedBuildEvent{
				Event: &build.BuildEvent{
					EventTime: eventTime2,
					Event: &build.BuildEvent_BazelEvent{
						BazelEvent: bazelEventFinishedAnypb,
					},
				},
			},
		},
	}

	startedDocuments := map[string]bazelevents.ConvertedDocument{
		"started": {
			"key": "value",
		},
	}
	finishedDocuments := map[string]bazelevents.ConvertedDocument{
		"finished": {
			"something": true,
		},
	}

	t.Run("ForwardBazelEvents", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, testutil.EqProto(t, bazelEventStarted)).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, testutil.EqProto(t, bazelEventFinished)).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBuildToolEventStream(ingestContext)
		require.NoError(t, err)
		require.NoError(t, stream.Send(buildEventStarted))
		require.NoError(t, stream.Send(buildEventFinished))
		require.NoError(t, stream.Recv())
		require.NoError(t, stream.Recv())
	})

	t.Run("FailAfterLastMessage", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, testutil.EqProto(t, bazelEventStarted)).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, testutil.EqProto(t, bazelEventFinished)).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBuildToolEventStream(ingestContext)
		require.NoError(t, err)
		require.NoError(t, stream.Send(buildEventStarted))
		require.NoError(t, stream.Send(buildEventFinished))
		require.NoError(t, stream.Recv())
		require.NoError(t, stream.Recv())
		err = stream.Send(buildEventFinished)
		testutil.RequireEqualStatus(t, status.Error(codes.InvalidArgument, "Last message already received"), err)
		require.Error(t, io.EOF, stream.Recv())
	})

	// Give up if upload fails.
	t.Run("UploadFailed", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, testutil.EqProto(t, bazelEventStarted)).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, testutil.EqProto(t, bazelEventFinished)).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(errors.New("Upload failed"))
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBuildToolEventStream(ingestContext)
		require.NoError(t, err)
		// This one will fail.
		err = stream.Send(buildEventStarted)
		testutil.RequireEqualStatus(t, status.Error(
			codes.Unknown, "Bazel event my_invocation_id-started for invocation my_invocation_id: Upload failed"), err)
		// This one should still succeed.
		err = stream.Send(buildEventFinished)
		require.NoError(t, err)
	})

	// Give up if the Bazel event is malicious.
	t.Run("ExtractingInterestingDataFail", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, testutil.EqProto(t, bazelEventStarted)).Return(nil, errors.New("Failed to convert"))
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, testutil.EqProto(t, bazelEventFinished)).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBuildToolEventStream(ingestContext)
		require.NoError(t, err)
		// This one will fail.
		err = stream.Send(buildEventStarted)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Bazel event for invocation my_invocation_id: Failed to convert"), err)
		// This one should still succeed.
		err = stream.Send(buildEventFinished)
		require.NoError(t, err)
	})
}
