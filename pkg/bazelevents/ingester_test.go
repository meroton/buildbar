package bazelevents_test

/*
import (
	"context"
	"errors"
	"io"
	"testing"

	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"go.uber.org/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/completedaction"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestBazelEventsIngester(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	uploader := mock.NewMockUploader(ctrl)

	completedAction := &cal_proto.CompletedAction{
		Uuid: "my-uuid",
	}
	flattenedAction := map[string]interface{}{
		"action": "flattened",
	}

	t.Run("ForwardOneCompletedAction", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(flattenedAction, nil)
		uploader.EXPECT().Put(ingestContext, "my-uuid", flattenedAction).Return(nil)
		calStream.EXPECT().Send(&emptypb.Empty{}).Return(nil)
		calStream.EXPECT().Recv().Return(nil, io.EOF)

		ingester := completedaction.NewIngester(uploader, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to receive completed action: EOF"), err)
	})

	// Give up if upload failed.
	t.Run("UploadFailed", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(flattenedAction, nil)
		uploader.EXPECT().Put(ingestContext, "my-uuid", flattenedAction).Return(errors.New("Upload failed"))

		ingester := completedaction.NewIngester(uploader, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to ingest completed action: Upload failed"), err)
	})

	// Give up if the completed action is malicious.
	t.Run("FlatteningFail", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(nil, errors.New("Failed to flatten"))

		ingester := completedaction.NewIngester(uploader, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to marshal completed action my-uuid: Failed to flatten"), err)
	})
}
*/
