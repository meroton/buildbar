package elasticsearch

import (
	"context"
	"fmt"
	"sync"

	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/result"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	uploaderPrometheusMetrics sync.Once

	uploaderUploadDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "buildbar",
			Subsystem: "elasticsearch",
			Name:      "upload_duration_seconds",
			Help:      "Amount of time spent from receiving data until it was stored in the database.",
			Buckets:   util.DecimalExponentialBuckets(-3, 6, 2),
		},
		[]string{"index", "result"})
)

// The Uploader can be used to push documents into Elasticsearch.
type Uploader interface {
	Put(ctx context.Context, id string, document interface{}) error
}

type uploader struct {
	elasticsearchClient *elasticsearch.TypedClient
	index               string
	clock               clock.Clock
	warningLogger       util.ErrorLogger
}

// NewUploader creates a new Uploader that uploads generic documents to Elasticsearch.
func NewUploader(
	elasticsearchClient *elasticsearch.TypedClient,
	index string,
	clock clock.Clock,
	warningLogger util.ErrorLogger,
) Uploader {
	uploaderPrometheusMetrics.Do(func() {
		prometheus.MustRegister(uploaderUploadDurationSeconds)
	})

	return &uploader{
		elasticsearchClient: elasticsearchClient,
		index:               index,
		clock:               clock,
		warningLogger:       warningLogger,
	}
}

// Put uploads a document with a specific id to Elasticsearch.
// The assumption is that documents are only uploaded once.
// If a document with the same id exists, the document will be replaced
// and a log line will be written.
func (u *uploader) Put(ctx context.Context, id string, document interface{}) error {
	indexStartTime := u.clock.Now()
	res, err := u.elasticsearchClient.Index(u.index).
		Id(id).
		Document(document).
		Do(ctx)
	duration := u.clock.Now().Sub(indexStartTime)
	if err != nil {
		uploaderUploadDurationSeconds.
			WithLabelValues(u.index, "transport-error").
			Observe(duration.Seconds())
		return util.StatusWrapf(err, "Failed to index document %s into %s in Elasticsearch", id, u.index)
	}
	uploaderUploadDurationSeconds.
		WithLabelValues(u.index, res.Result.String()).
		Observe(duration.Seconds())
	if res.Result != result.Created {
		u.warningLogger.Log(fmt.Errorf(
			"Unexpected successful result when indexing %s into %s in Elasticsearch: %v", id, u.index, res.Result,
		))
	}
	return nil
}
