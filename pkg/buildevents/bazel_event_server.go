package buildevents

import (
	"context"
	"io"
	"sync"

	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/prometheus/client_golang/prometheus"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
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

type bazelBuildEventServer struct {
	receiver bazelevents.BazelEventServer
}

// NewBazelBuildEventServer creates a new BuildEventServer that extracts Bazel events
// and forwards them to a receiver for further processing.
func NewBazelBuildEventServer(
	receiver bazelevents.BazelEventServer,
) BuildEventServer {
	ingesterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(ingesterEventsReceived)
		prometheus.MustRegister(ingesterInvalidEvents)
	})
	return &bazelBuildEventServer{
		receiver: receiver,
	}
}

func (i *bazelBuildEventServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	// Nothing to do.
	return nil
}

func (i *bazelBuildEventServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	return &bazelBuildEventStreamClient{
		ctx: ctx,
		// Create a reasonably buffered channel.
		recvChannel: make(chan bool, 100),
		receiver:    i.receiver,
		stream:      nil,
	}, nil
}

type bazelBuildEventStreamClient struct {
	ctx         context.Context
	recvChannel chan bool
	// The receiver is needed to initialize the stream when the first event is received.
	receiver bazelevents.BazelEventServer
	stream   bazelevents.BazelEventStreamClient
}

func (s *bazelBuildEventStreamClient) Send(request *build.PublishBuildToolEventStreamRequest) error {
	ingesterEventsReceived.Inc()

	// Only forward Bazel events.
	if bazelEvent := request.OrderedBuildEvent.GetEvent().GetBazelEvent(); bazelEvent != nil {
		if s.stream == nil {
			streamID := request.OrderedBuildEvent.GetStreamId()
			if streamID == nil {
				return status.Error(codes.InvalidArgument, "Stream ID is empty")
			}
			instanceName, err := digest.NewInstanceName(request.ProjectId)
			if err != nil {
				return util.StatusWrap(err, "Bad build tool event instance name")
			}
			s.stream, err = s.receiver.PublishBazelEvents(s.ctx, instanceName, streamID)
			if err != nil {
				s.stream = nil
				return util.StatusWrap(err, "Failed to create Bazel event stream")
			}
			s.receiver = nil
		}
		var bazelBuildEvent buildeventstream.BuildEvent
		if err := bazelEvent.UnmarshalTo(&bazelBuildEvent); err != nil {
			return util.StatusWrap(err, "Failed to unmarshal Bazel build event")
		}
		if err := s.stream.Send(request.OrderedBuildEvent.GetEvent().GetEventTime(), &bazelBuildEvent); err != nil {
			return err
		}
		s.recvChannel <- true
	} else {
		// Unused event.
		s.recvChannel <- false
	}
	return nil
}

func (s *bazelBuildEventStreamClient) CloseSend() {
	close(s.recvChannel)
}

func (s *bazelBuildEventStreamClient) Recv() error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case recvFromDownstream, ok := <-s.recvChannel:
		if !ok {
			return io.EOF
		}
		if !recvFromDownstream {
			return nil
		}
		err := s.stream.Recv()
		if err != nil {
			// Just continue to get errors downstream.
			s.recvChannel <- true
		}
		return err
	}
}
