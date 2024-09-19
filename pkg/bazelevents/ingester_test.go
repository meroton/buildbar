package bazelevents_test

import (
	"context"
	"errors"
	"io"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	bazelEventStarted := &buildeventstream.BuildEvent{
		Payload: &buildeventstream.BuildEvent_Started{
			Started: &buildeventstream.BuildStarted{},
		},
	}
	bazelEventFinished := &buildeventstream.BuildEvent{
		Payload: &buildeventstream.BuildEvent_Finished{
			Finished: &buildeventstream.BuildFinished{},
		},
		LastMessage: true,
	}
	eventTime1 := &timestamppb.Timestamp{
		Seconds: 123,
	}
	eventTime2 := &timestamppb.Timestamp{
		Seconds: 456,
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
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, bazelEventStarted).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventFinished).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBazelEvents(ingestContext, digest.MustNewInstanceName("my-instance"), &build.StreamId{
			InvocationId: "my_invocation_id",
		})
		require.NoError(t, err)
		require.NoError(t, stream.Send(eventTime1, bazelEventStarted))
		require.NoError(t, stream.Send(eventTime2, bazelEventFinished))
		require.NoError(t, stream.Recv())
		require.NoError(t, stream.Recv())
	})

	t.Run("FailAfterLastMessage", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, bazelEventStarted).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventFinished).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBazelEvents(ingestContext, digest.MustNewInstanceName("my-instance"), &build.StreamId{
			InvocationId: "my_invocation_id",
		})
		require.NoError(t, err)
		require.NoError(t, stream.Send(eventTime1, bazelEventStarted))
		require.NoError(t, stream.Send(eventTime2, bazelEventFinished))
		require.NoError(t, stream.Recv())
		require.NoError(t, stream.Recv())
		err = stream.Send(eventTime2, bazelEventFinished)
		testutil.RequireEqualStatus(t, status.Error(codes.InvalidArgument, "Last message already received"), err)
		require.Error(t, io.EOF, stream.Recv())
	})

	// Give up if upload fails.
	t.Run("UploadFailed", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, bazelEventStarted).Return(startedDocuments, nil)
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventFinished).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(errors.New("Upload failed"))
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBazelEvents(ingestContext, digest.MustNewInstanceName("my-instance"), &build.StreamId{
			InvocationId: "my_invocation_id",
		})
		require.NoError(t, err)
		// This one will fail.
		err = stream.Send(eventTime1, bazelEventStarted)
		testutil.RequireEqualStatus(t, status.Error(
			codes.Unknown, "Bazel event my_invocation_id-started for invocation my_invocation_id: Upload failed"), err)
		// This one should still succeed.
		err = stream.Send(eventTime2, bazelEventFinished)
		require.NoError(t, err)
	})

	// Give up if the Bazel event is malicious.
	t.Run("ExtractingInterestingDataFail", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, bazelEventStarted).Return(nil, errors.New("Failed to convert"))
		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventFinished).Return(finishedDocuments, nil)
		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
		stream, err := ingester.PublishBazelEvents(ingestContext, digest.MustNewInstanceName("my-instance"), &build.StreamId{
			InvocationId: "my_invocation_id",
		})
		require.NoError(t, err)
		// This one will fail.
		err = stream.Send(eventTime1, bazelEventStarted)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Bazel event for invocation my_invocation_id: Failed to convert"), err)
		// This one should still succeed.
		err = stream.Send(eventTime2, bazelEventFinished)
		require.NoError(t, err)
	})
}
