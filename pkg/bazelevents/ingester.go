package bazelevents

import (
	"context"
	"io"
	"sync"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/prometheus/client_golang/prometheus"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	converterFactory ConverterFactory
	uploader         elasticsearch.Uploader
	errorLogger      util.ErrorLogger
}

// ConverterFactory creates a Converter.
type ConverterFactory func(errorLogger util.ErrorLogger) Converter

// NewIngester creates a new Ingester that uploads BuildEvents to Elasticsearch.
func NewIngester(
	converterFactory ConverterFactory,
	uploader elasticsearch.Uploader,
	errorLogger util.ErrorLogger,
) BazelEventServer {
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

func (i *ingester) PublishBazelEvents(ctx context.Context, instanceName digest.InstanceName, streamID *build.StreamId) (BazelEventStreamClient, error) {
	if streamID.InvocationId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invocation ID is empty")
	}
	converter := i.converterFactory(i.errorLogger)
	return &ingestingStreamClient{
		ctx:         ctx,
		errorLogger: i.errorLogger,

		instanceName: instanceName,
		streamID:     streamID,

		converter: converter,
		uploader:  i.uploader,

		sendResponses: make(chan struct{}),
		ackCounter:    0,
	}, nil
}

type ingestingStreamClient struct {
	ctx         context.Context
	errorLogger util.ErrorLogger

	instanceName digest.InstanceName
	streamID     *build.StreamId

	converter Converter
	uploader  elasticsearch.Uploader

	sendResponses chan struct{}
	ackCounter    int
}

func (s *ingestingStreamClient) Send(eventTime *timestamppb.Timestamp, event *buildeventstream.BuildEvent) error {
	select {
	case <-s.sendResponses:
		return status.Error(codes.InvalidArgument, "Last message already received")
	default:
		// noop
	}
	ingesterEventsReceived.Inc()

	documents, err := s.converter.ExtractInterestingData(s.ctx, eventTime, event)
	if err != nil {
		return util.StatusWrapf(err, "Converting Bazel event for invocation %s", s.streamID.InvocationId)
	}
	for suffix, document := range documents {
		uuid := s.streamID.InvocationId + "-" + suffix
		if err := s.uploader.Put(s.ctx, uuid, document); err != nil {
			return util.StatusWrapf(err, "Uploading Bazel event %s for invocation %s", uuid, s.streamID.InvocationId)
		}
	}

	// As we need all messages to build up all the file sets, the acks cannot
	// be sent until after the last message has been processed.
	s.ackCounter++
	if event.LastMessage {
		close(s.sendResponses)
	}
	return nil
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
