package completedaction

import (
	"sync"

	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/prometheus/client_golang/prometheus"

	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	ingesterPrometheusMetrics sync.Once

	ingesterActionsReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "completedaction",
		Name:      "ingester_actions_received",
		Help:      "Number of Completed Actions that has been received.",
	})
	ingesterInvalidActions = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "completedaction",
		Name:      "ingester_invalid_actions",
		Help:      "Number of Completed Actions that could not be handled.",
	})
)

// The Ingester can be used to receive CompletedActions and
// push them into some database. This allows for realtime or post-build
// analysis in a remote service. This is particularly useful for understanding
// how build actions change over time by inspecting the aggregated
// CompletedAction metadata.
type Ingester struct {
	uploader  elasticsearch.Uploader
	converter Converter
}

// NewIngester creates a new Ingester that uploads CompletedActions to Elasticsearch.
func NewIngester(
	uploader elasticsearch.Uploader,
	converter Converter,
) *Ingester {
	ingesterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(ingesterActionsReceived)
		prometheus.MustRegister(ingesterInvalidActions)
	})

	return &Ingester{
		uploader:  uploader,
		converter: converter,
	}
}

// LogCompletedActions is part of the API of the CompletedActionLogger service.
func (cai *Ingester) LogCompletedActions(stream cal_proto.CompletedActionLogger_LogCompletedActionsServer) error {
	for {
		completedAction, err := stream.Recv()
		if err != nil {
			return util.StatusWrap(err, "Failed to receive completed action")
		}
		ingesterActionsReceived.Inc()
		flatCompletedAction, err := cai.converter.FlattenCompletedAction(stream.Context(), completedAction)
		if err != nil {
			ingesterInvalidActions.Inc()
			return util.StatusWrapf(err, "Failed to marshal completed action %s", completedAction.Uuid)
		}
		if err := cai.uploader.Put(stream.Context(), completedAction.Uuid, flatCompletedAction); err != nil {
			if util.IsInfrastructureError(err) {
				// Let the client know that it needs to retry.
				return util.StatusWrap(err, "Failed to ingest completed action")
			}
		}
		// Acknowledge the CompletedAction and let the client drop it from its queue.
		stream.Send(&emptypb.Empty{})
	}
}
