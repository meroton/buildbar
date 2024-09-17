package buildevents

import (
	"context"

	"github.com/buildbarn/bb-storage/pkg/util"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
)

type annotatedServer struct {
	backend BuildEventServer
	reason  string
}

// NewAnnotatedServer creates a BuildEventServer which annotates all
// returned errors with a certain reason.
func NewAnnotatedServer(backend BuildEventServer, reason string) BuildEventServer {
	return &annotatedServer{
		backend: backend,
		reason:  reason,
	}
}

func (bes *annotatedServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	err := bes.backend.PublishLifecycleEvent(ctx, in)
	if err != nil {
		return util.StatusWrap(err, bes.reason)
	}
	return nil
}

func (bes *annotatedServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	stream, err := bes.backend.PublishBuildToolEventStream(ctx)
	if err != nil {
		return nil, util.StatusWrap(err, bes.reason)
	}
	return &annotatedBuildToolEventStreamClient{
		backend: stream,
		reason:  bes.reason,
	}, nil
}

type annotatedBuildToolEventStreamClient struct {
	backend BuildToolEventStreamClient
	reason  string
}

func (s *annotatedBuildToolEventStreamClient) Send(req *build.PublishBuildToolEventStreamRequest) error {
	if err := s.backend.Send(req); err != nil {
		return util.StatusWrap(err, s.reason)
	}
	return nil
}

func (s *annotatedBuildToolEventStreamClient) CloseSend() {
	s.backend.CloseSend()
}

func (s *annotatedBuildToolEventStreamClient) Recv() error {
	if err := s.backend.Recv(); err != nil {
		return util.StatusWrap(err, s.reason)
	}
	return nil
}
