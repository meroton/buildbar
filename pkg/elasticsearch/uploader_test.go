package elasticsearch_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/golang/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	bb_elasticsearch "github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUploader(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	clock := clock.SystemClock
	errorLogger := mock.NewMockErrorLogger(ctrl)
	roundTripper := mock.NewMockRoundTripper(ctrl)

	elasticsearchConfig := elasticsearch.Config{
		Transport: roundTripper,
	}
	esClient, err := elasticsearch.NewTypedClient(elasticsearchConfig)
	require.NoError(t, err)

	flattenedAction := map[string]interface{}{
		"action": "flattened",
	}

	t.Run("UploadDocument", func(t *testing.T) {
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

		uploader := bb_elasticsearch.NewUploader(esClient, "my-index", clock, errorLogger)
		err := uploader.Put(ctx, "my-uuid", flattenedAction)
		require.NoError(t, err)
	})

	// Nothing to do if Elasticsearch receives the data and refuses to ingest.
	t.Run("ElasticsearchRefuseToIngest", func(t *testing.T) {
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
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			require.ErrorContains(t, err, "Unexpected successful result when indexing my-uuid into my-index in Elasticsearch: noop")
		})

		uploader := bb_elasticsearch.NewUploader(esClient, "my-index", clock, errorLogger)
		err := uploader.Put(ctx, "my-uuid", flattenedAction)
		// There is no point to retry, the document should be there.
		require.NoError(t, err)
	})

	// Give up if the connection is broken.
	t.Run("ConnectionFail", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Elastic-Product", "Elasticsearch")
		roundTripper.EXPECT().RoundTrip(gomock.Any()).Return(&http.Response{
			Status:     "400 Bad Request",
			StatusCode: 400,
			Header:     header,
			Body:       io.NopCloser(bytes.NewBufferString(`{"result": "created"}`)),
		}, nil)
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			testutil.RequirePrefixedStatus(t, status.Error(codes.InvalidArgument, "Failed to index document my-uuid into my-index in Elasticsearch: "), err)
		})

		uploader := bb_elasticsearch.NewUploader(esClient, "my-index", clock, errorLogger)
		err := uploader.Put(ctx, "my-uuid", flattenedAction)
		testutil.RequirePrefixedStatus(t, status.Error(codes.InvalidArgument, "Failed to index document my-uuid into my-index in Elasticsearch: "), err)
	})

	// Retry on server errors, i.e. status code >=500.
	t.Run("ServerError", func(t *testing.T) {
		header := http.Header{}
		header.Add("X-Elastic-Product", "Elasticsearch")
		roundTripper.EXPECT().RoundTrip(gomock.Any()).Return(&http.Response{
			Status:     "500 Internal server error",
			StatusCode: 500,
			Header:     header,
			Body:       io.NopCloser(bytes.NewBufferString(``)),
		}, nil)
		// The error should be logged.
		errorLogger.EXPECT().Log(gomock.Any()).DoAndReturn(func(err error) {
			testutil.RequirePrefixedStatus(t, status.Error(codes.Unknown, "Failed to index document my-uuid into my-index in Elasticsearch: "), err)
		})

		uploader := bb_elasticsearch.NewUploader(esClient, "my-index", clock, errorLogger)
		err := uploader.Put(ctx, "my-uuid", flattenedAction)
		testutil.RequirePrefixedStatus(t, status.Error(codes.Unknown, "Failed to index document my-uuid into my-index in Elasticsearch: "), err)
	})
}
