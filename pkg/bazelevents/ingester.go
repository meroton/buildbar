package bazelevents

import (
	"context"
	"io"
	"sync"

	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/prometheus/client_golang/prometheus"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ingesterPrometheusMetrics sync.Once

	ingesterEventsReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "buildevents",
		Name:      "ingester_events_received",
		Help:      "Number of Build Events that has been received.",
	})
	ingesterInvalidEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "buildevents",
		Name:      "ingester_invalid_events",
		Help:      "Number of Build Events that could not be handled.",
	})
)

// The Ingester can be used to receive BuildEvents and
// push them into some database. This allows for realtime or post-build
// analysis in a remote service. This is particularly useful for understanding
// how builds change over time by inspecting the aggregated BuildEvent metadata.
type ingester struct {
	converterFactory BazelEventConverterFactory
	uploader         elasticsearch.Uploader
	errorLogger      util.ErrorLogger
}

// BazelEventConverterFactory creates a BazelEventConverter.
type BazelEventConverterFactory func(errorLogger util.ErrorLogger) BazelEventConverter

// NewIngester creates a new Ingester that uploads BuildEvents to Elasticsearch.
func NewIngester(
	converterFactory BazelEventConverterFactory,
	uploader elasticsearch.Uploader,
	errorLogger util.ErrorLogger,
) buildevents.BuildEventServer {
	ingesterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(ingesterEventsReceived)
		prometheus.MustRegister(ingesterInvalidEvents)
	})
	return &ingester{
		converterFactory: converterFactory,
		uploader:         uploader,
		errorLogger:      errorLogger,
	}
}

type ingestingWrappedErrorLogger struct {
	backend      util.ErrorLogger
	instanceName digest.InstanceName
	invocationID string
}

func (el *ingestingWrappedErrorLogger) Log(err error) {
	el.backend.Log(util.StatusWrapf(err, "Instance %s, invocation %s", el.instanceName, el.invocationID))
}

func (i *ingester) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	// noop
	return nil
}

func (i *ingester) PublishBuildToolEventStream(ctx context.Context) (buildevents.BuildToolEventStreamClient, error) {
	return &ingestingStreamClient{
		ctx:         ctx,
		errorLogger: i.errorLogger,
		uploader:    i.uploader,

		converterFactory: i.converterFactory,
		converter:        nil,

		sendResponses: make(chan struct{}),
		ackCounter:    0,
	}, nil
}

type ingestingStreamClient struct {
	ctx         context.Context
	errorLogger util.ErrorLogger
	uploader    elasticsearch.Uploader

	converterFactory BazelEventConverterFactory
	converter        BazelEventConverter
	// instanceName is valid if converter is non-nil.
	instanceName digest.InstanceName
	// streamID is valid if converter is non-nil.
	streamID *build.StreamId

	sendResponses chan struct{}
	ackCounter    int
}

func (s *ingestingStreamClient) Send(request *buildevents.BufferedPublishBuildToolEventStreamRequest) error {
	select {
	case <-s.sendResponses:
		return status.Error(codes.InvalidArgument, "Last message already received")
	default:
		// noop
	}
	ingesterEventsReceived.Inc()

	if s.converter == nil {
		streamID := request.OrderedBuildEvent.GetStreamId()
		if streamID == nil {
			return status.Error(codes.InvalidArgument, "Stream ID is empty")
		}
		instanceName, err := digest.NewInstanceName(request.ProjectId)
		if err != nil {
			return util.StatusWrap(err, "Bad build tool event instance name")
		}
		if streamID.InvocationId == "" {
			return status.Errorf(codes.InvalidArgument, "Invocation ID is empty")
		}
		// Wrap the logger with invocationID from now on.
		s.errorLogger = &ingestingWrappedErrorLogger{
			backend:      s.errorLogger,
			instanceName: instanceName,
			invocationID: streamID.InvocationId,
		}
		s.streamID = streamID
		s.instanceName = instanceName
		s.converter = s.converterFactory(s.errorLogger)
	}

	eventTime := request.GetOrderedBuildEvent().GetEvent().GetEventTime()
	event, err := request.GetBufferedBazelBuildEvent()
	if err != nil {
		return util.StatusWrap(err, "Failed to ingest event")
	}
	if event != nil {
		documents, err := s.converter.ExtractInterestingData(s.ctx, eventTime, event)
		if err != nil {
			return util.StatusWrapf(err, "Bazel event for invocation %s", s.streamID.InvocationId)
		}
		for suffix, document := range documents {
			uuid := s.streamID.InvocationId + "-" + suffix
			document["invocation_id"] = s.streamID.InvocationId
			document["correlated_invocation_id"] = s.streamID
			if err := s.uploader.Put(s.ctx, uuid, document); err != nil {
				return util.StatusWrapf(err, "Bazel event %s for invocation %s", uuid, s.streamID.InvocationId)
			}
		}

		// As we need all messages to grab metadata and potentially build up
		// all the file sets, the acks cannot be sent until after the
		// last message has been processed, to make sure that all messages
		// are resent on connection loss.
		// TODO: Read back BuildMetadata from some other cache.
		s.ackCounter++
		if event.LastMessage {
			close(s.sendResponses)
		}
	} else {
		s.ackCounter++
	}
	return nil
}

func (s *ingestingStreamClient) CloseSend() {
	// noop
}

func (s *ingestingStreamClient) Recv() error {
	select {
	case <-s.sendResponses:
		if s.ackCounter == 0 {
			return io.EOF
		}
		s.ackCounter--
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
