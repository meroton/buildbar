package bazelevents

import (
	"context"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/digest"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BazelEventServer is a Go adapted version of
// build.PublishBuildEventServer but only receives
// Bazel buildeventstream.BuildEvents.
type BazelEventServer interface {
	// PublishBazelEvents creates a stream where Bazel buildeventstream.BuildEvents
	// can be sent to. It is implementation defined if the same streamID can be reused.
	PublishBazelEvents(ctx context.Context, instanceName digest.InstanceName, streamID *build.StreamId) (BazelEventStreamClient, error)
}

// BazelEventStreamClient receives a stream of Bazel buildeventstream.BuildEvents
// until an event with event.LastMessage is set to true is received.
//
// A cancelled stream of events is signalled by a cancelled context.
type BazelEventStreamClient interface {
	Send(eventTime *timestamppb.Timestamp, event *buildeventstream.BuildEvent) error
	Recv() error
}
