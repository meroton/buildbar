package buildevents

import (
	"context"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/util"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// BuildEventServer is a Go adapted version of
// build.PublishBuildEventServer.
type BuildEventServer interface {
	PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error
	PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error)
}

// BuildToolEventStreamClient is a subset of the gRPC
// PublishBuildEvent_PublishBuildToolEventStreamClient interface.
type BuildToolEventStreamClient interface {
	Send(*BufferedPublishBuildToolEventStreamRequest) error
	CloseSend()
	Recv() error
}

// BufferedPublishBuildToolEventStreamRequest wraps
// *build.PublishBuildToolEventStreamRequest and caches unpacking of
// BazelEvents.
type BufferedPublishBuildToolEventStreamRequest struct {
	*build.PublishBuildToolEventStreamRequest

	// bazelBuildEvent is nil until it has been unmarshalled from the request.
	bazelBuildEvent *buildeventstream.BuildEvent
}

// GetBufferedBazelBuildEvent unmarshals a Bazel Build Event or returns nil if it
// is not present. The unmarshalled data is cached for subsequent calls
// to GetBufferedBazelBuildEvent. Must bo be called while UpdateBazelBuildEvent
// is in progress.
func (buf *BufferedPublishBuildToolEventStreamRequest) GetBufferedBazelBuildEvent() (*buildeventstream.BuildEvent, error) {
	if buf.bazelBuildEvent == nil {
		if bazelEvent := buf.GetOrderedBuildEvent().GetEvent().GetBazelEvent(); bazelEvent != nil {
			var bazelBuildEvent buildeventstream.BuildEvent
			if err := bazelEvent.UnmarshalTo(&bazelBuildEvent); err != nil {
				return nil, util.StatusWrap(err, "Failed to unmarshal Bazel build event")
			}
			buf.bazelBuildEvent = &bazelBuildEvent
		}
	}
	return buf.bazelBuildEvent, nil
}

// UpdateBazelBuildEvent writes a Bazel Build Event into the request.
// UpdateBazelBuildEvent must not be called concurrently with GetBufferedBazelBuildEvent.
func (buf *BufferedPublishBuildToolEventStreamRequest) UpdateBazelBuildEvent(event *buildeventstream.BuildEvent) error {
	orderedBuildEvent := buf.GetOrderedBuildEvent()
	if orderedBuildEvent.GetEvent().GetBazelEvent() == nil {
		return status.Error(codes.InvalidArgument, "Build tool event does not contain a Bazel build event to update")
	}
	serializedEvent, err := anypb.New(event)
	if err != nil {
		return util.StatusWrap(err, "Failed to marshal Bazel build event")
	}
	orderedBuildEvent.Event.Event = &build.BuildEvent_BazelEvent{
		BazelEvent: serializedEvent,
	}
	buf.bazelBuildEvent = nil
	return nil
}
