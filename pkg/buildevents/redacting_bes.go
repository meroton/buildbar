package buildevents

import (
	"context"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/third_party/buildbuddy/server/util/redact"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

type redactingBuildEventServer struct {
	backend        BuildEventServer
	allowedEnvVars []string
}

// NewRedactingBuildEventServer creates a new BuildEventServer that extracts Bazel events
// and forwards them to a receiver for further processing.
func NewRedactingBuildEventServer(
	backend BuildEventServer,
	allowedEnvVars []string,
) BuildEventServer {
	return &redactingBuildEventServer{
		backend:        backend,
		allowedEnvVars: allowedEnvVars,
	}
}

func (bes *redactingBuildEventServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	if err := bes.backend.PublishLifecycleEvent(ctx, in); err != nil {
		return util.StatusWrap(err, "Redacting")
	}
	return nil
}

func (bes *redactingBuildEventServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	stream, err := bes.backend.PublishBuildToolEventStream(ctx)
	if err != nil {
		return nil, util.StatusWrap(err, "Redacting")
	}
	redactor := redact.NewStreamingRedactor()
	redactor.AllowedEnvVars = append(redactor.AllowedEnvVars, bes.allowedEnvVars...)
	return &redactingBuildEventStreamClient{
		ctx:      ctx,
		stream:   stream,
		redactor: redactor,
	}, nil
}

type redactingBuildEventStreamClient struct {
	ctx      context.Context
	stream   BuildToolEventStreamClient
	redactor *redact.StreamingRedactor
}

func (s *redactingBuildEventStreamClient) Send(request *build.PublishBuildToolEventStreamRequest) error {
	if bazelEvent := request.OrderedBuildEvent.GetEvent().GetBazelEvent(); bazelEvent != nil {
		var bazelBuildEvent buildeventstream.BuildEvent
		if err := bazelEvent.UnmarshalTo(&bazelBuildEvent); err != nil {
			return util.StatusWrap(err, "Unable to unmarshal Bazel event for redacting")
		}
		if err := s.redactor.RedactMetadata(&bazelBuildEvent); err != nil {
			return util.StatusWrap(err, "Failed to redact Bazel event")
		}
		bazelBuildEventData, err := anypb.New(&bazelBuildEvent)
		if err != nil {
			return util.StatusWrap(err, "Unable to marshal Bazel event after redacting")
		}
		// Replace the event but don't modify the incoming message.
		request = proto.Clone(request).(*build.PublishBuildToolEventStreamRequest)
		request.OrderedBuildEvent.Event.Event = &build.BuildEvent_BazelEvent{
			BazelEvent: bazelBuildEventData,
		}
	}
	if err := s.stream.Send(request); err != nil {
		return util.StatusWrap(err, "Redacting")
	}
	return nil
}

func (s *redactingBuildEventStreamClient) CloseSend() {
	s.stream.CloseSend()
}

func (s *redactingBuildEventStreamClient) Recv() error {
	if err := s.stream.Recv(); err != nil {
		return util.StatusWrap(err, "Redacting")
	}
	return nil
}
