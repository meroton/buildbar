package buildevents

import (
	"context"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
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
	Send(*build.PublishBuildToolEventStreamRequest) error
	CloseSend()
	Recv() error
}
