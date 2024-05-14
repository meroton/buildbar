package completedaction_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/golang/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/completedaction"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestCompletedActionIngester(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	clock := clock.SystemClock
	converter := mock.NewMockConverter(ctrl)
	calStream := mock.NewMockCompletedActionLogger_LogCompletedActionsServer(ctrl)
	roundTripper := mock.NewMockRoundTripper(ctrl)

	elasticsearchConfig := elasticsearch.Config{
		Transport: roundTripper,
	}
	esClient, err := elasticsearch.NewTypedClient(elasticsearchConfig)
	require.NoError(t, err)

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
		calStream.EXPECT().Send(&emptypb.Empty{}).Return(nil)
		calStream.EXPECT().Recv().Return(nil, io.EOF)
		roundTripper.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "PUT", r.Method)
			require.Equal(t, "http://localhost:9200/my-index/_doc/my-uuid", r.URL.String())
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, string(body), `{"action":"flattened"}`)
			err = r.Body.Close()
			require.NoError(t, err)
			header := http.Header{}
			header.Add("X-Elastic-Product", "Elasticsearch")
			response := &http.Response{
				Status:     "200 OK",
				StatusCode: 200,
				Header:     header,
				Body:       io.NopCloser(bytes.NewBufferString(`{"result": "created"}`)),
			}
			return response, nil
		})

		ingester := completedaction.NewIngester(esClient, "my-index", clock, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to receive completed action: EOF"), err)
	})

	t.Run("ElasticsearchRefuseToIngest", func(t *testing.T) {
		// Nothing to do if Elasticsearch receives the data and refuses to ingest.
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(flattenedAction, nil)
		calStream.EXPECT().Send(&emptypb.Empty{}).Return(nil)
		calStream.EXPECT().Recv().Return(nil, io.EOF)
		roundTripper.EXPECT().RoundTrip(gomock.Any()).DoAndReturn(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "PUT", r.Method)
			require.Equal(t, "http://localhost:9200/my-index/_doc/my-uuid", r.URL.String())
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.Equal(t, string(body), `{"action":"flattened"}`)
			err = r.Body.Close()
			require.NoError(t, err)
			header := http.Header{}
			header.Add("X-Elastic-Product", "Elasticsearch")
			response := &http.Response{
				Status:     "200 OK",
				StatusCode: 200,
				Header:     header,
				// Not creating a new document.
				Body: io.NopCloser(bytes.NewBufferString(`{"result": "noop"}`)),
			}
			return response, nil
		})

		ingester := completedaction.NewIngester(esClient, "my-index", clock, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to receive completed action: EOF"), err)
	})

	// Give up if the completed action is malicious.
	t.Run("FlatteningFail", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(nil, errors.New("Failed to flatten"))

		ingester := completedaction.NewIngester(esClient, "my-index", clock, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Failed to marshal completed action my-uuid: Failed to flatten"), err)
	})

	// Give up if the connection is broken.
	t.Run("ConnectionFail", func(t *testing.T) {
		ingestContext := context.WithValue(ctx, "key", "value")
		calStream.EXPECT().Context().AnyTimes().Return(ingestContext)
		calStream.EXPECT().Recv().Return(completedAction, nil)
		converter.EXPECT().FlattenCompletedAction(ingestContext, completedAction).Return(flattenedAction, nil)
		header := http.Header{}
		header.Add("X-Elastic-Product", "Elasticsearch")
		roundTripper.EXPECT().RoundTrip(gomock.Any()).Return(&http.Response{
			Status:     "400 Bad Request",
			StatusCode: 400,
			Header:     header,
			Body:       io.NopCloser(bytes.NewBufferString(`{"result": "created"}`)),
		}, nil)

		ingester := completedaction.NewIngester(esClient, "my-index", clock, converter)
		err := ingester.LogCompletedActions(calStream)
		testutil.RequireEqualStatus(t, status.Error(codes.Unknown, "Elasticsearch index request failed: status: 400, failed: [], reason: "), err)
	})
}
