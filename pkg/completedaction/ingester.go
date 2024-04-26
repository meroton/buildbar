package completedaction

import (
	"log"
	"sync"

	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/typedapi/types/enums/result"
	"github.com/prometheus/client_golang/prometheus"

	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	ingesterPrometheusMetrics sync.Once

	ingesterActionsReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "ingester",
		Name:      "completed_action_ingester_actions_received_total",
		Help:      "Number of Completed Actions that has been received.",
	})
	ingesterUploadDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "buildbar",
			Subsystem: "ingester",
			Name:      "completed_action_ingester_upload_duration_seconds",
			Help:      "Amount of time spent from receiving a Completed Action until uploaded into the database.",
			Buckets:   util.DecimalExponentialBuckets(-3, 6, 2),
		},
		[]string{"result"})
)

// The Ingester can be used to receive CompletedActions and
// push them into some database. This allows for realtime or post-build
// analysis in a remote service. This is particularly useful for understanding
// how build actions change over time by inspecting the aggregated
// CompletedAction metadata.
type Ingester interface {
	LogCompletedActions(cal_proto.CompletedActionLogger_LogCompletedActionsServer) error
}

type ingester struct {
	elasticsearchClient *elasticsearch.TypedClient
	ingestIndex         string
	clock               clock.Clock
	converter           Converter
}

// NewIngester creates a new Ingester that uploads CompletedActions to Elasticsearch.
func NewIngester(
	elasticsearchClient *elasticsearch.TypedClient,
	ingestIndex string,
	clock clock.Clock,
	converter Converter,
) Ingester {
	ingesterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(ingesterActionsReceived)
		prometheus.MustRegister(ingesterUploadDurationSeconds)
	})

	return &ingester{
		elasticsearchClient: elasticsearchClient,
		ingestIndex:         ingestIndex,
		clock:               clock,
		converter:           converter,
	}
}

func (cai *ingester) LogCompletedActions(stream cal_proto.CompletedActionLogger_LogCompletedActionsServer) error {
	for {
		completedAction, err := stream.Recv()
		if err != nil {
			return util.StatusWrap(err, "Failed to receive completed action")
		}
		ingesterActionsReceived.Inc()
		flatCompletedAction, err := cai.converter.FlattenCompletedAction(stream.Context(), completedAction)
		if err != nil {
			return util.StatusWrapf(err, "Failed to marshal completed action %s", completedAction.Uuid)
		}

		indexStartTime := cai.clock.Now()
		res, err := cai.elasticsearchClient.Index(cai.ingestIndex).
			Id(completedAction.Uuid).
			Document(flatCompletedAction).
			Do(stream.Context())
		duration := cai.clock.Now().Sub(indexStartTime)
		if err != nil {
			ingesterUploadDurationSeconds.
				WithLabelValues("transport-error").
				Observe(duration.Seconds())
			log.Printf("Error indexing document %s: %s", completedAction.Uuid, err)
			return util.StatusWrap(err, "Elasticsearch index request failed")
		}
		ingesterUploadDurationSeconds.
			WithLabelValues(res.Result.String()).
			Observe(duration.Seconds())
		if res.Result != result.Created {
			log.Printf("Unexpected indexing result for %s: %s", completedAction.Uuid, res.Result)
		}
		// Acknowledge the CompletedAction and let the client drop it from its queue.
		stream.Send(&emptypb.Empty{})
	}
}
