package buildevents

import (
	"context"
	"log"
	"math"

	"github.com/buildbarn/bb-storage/pkg/util"
	"golang.org/x/sync/semaphore"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
)

type errorIgnoringServer struct {
	backend     BuildEventServer
	errorLogger util.ErrorLogger
}

// NewErrorIgnoringServer creates a BuildEventServer that logs errors instead
// of sending them back to the client.
func NewErrorIgnoringServer(backend BuildEventServer, errorLogger util.ErrorLogger) BuildEventServer {
	return &errorIgnoringServer{
		backend:     backend,
		errorLogger: errorLogger,
	}
}

func (bes *errorIgnoringServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	if err := bes.backend.PublishLifecycleEvent(ctx, in); err != nil {
		bes.errorLogger.Log(util.StatusWrap(err, "Ignoring PublishLifecycleEvent error"))
	}
	return nil
}

func (bes *errorIgnoringServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	stream, err := bes.backend.PublishBuildToolEventStream(ctx)
	if err != nil {
		bes.errorLogger.Log(util.StatusWrap(err, "Ignoring PublishBuildToolEventStream error"))
		stream = nil
	}
	return newErrorIgnoringBuildToolEventStreamClient(ctx, stream, bes.errorLogger), nil
}

type errorIgnoringBuildToolEventStreamClient struct {
	ctx         context.Context
	sendBackend BuildToolEventStreamClient
	recvBackend BuildToolEventStreamClient
	ackCounter  *semaphore.Weighted
	errorLogger util.ErrorLogger
}

func newErrorIgnoringBuildToolEventStreamClient(ctx context.Context, backend BuildToolEventStreamClient, errorLogger util.ErrorLogger) BuildToolEventStreamClient {
	ackCounter := semaphore.NewWeighted(math.MaxInt64)
	if !ackCounter.TryAcquire(math.MaxInt64) {
		log.Fatalf("Semaphore construction of size MaxInt64 failed")
	}
	return &errorIgnoringBuildToolEventStreamClient{
		ctx:         ctx,
		sendBackend: backend,
		recvBackend: backend,
		ackCounter:  ackCounter,
		errorLogger: errorLogger,
	}
}

func (s *errorIgnoringBuildToolEventStreamClient) Send(req *build.PublishBuildToolEventStreamRequest) error {
	s.ackCounter.Release(1)
	if s.sendBackend != nil {
		if err := s.sendBackend.Send(req); err != nil {
			s.errorLogger.Log(util.StatusWrap(err, "Ignoring BuildToolEventStream.Send error"))
			// Don't try sending any more events.
			s.sendBackend = nil
		}
	}
	return nil
}

func (s *errorIgnoringBuildToolEventStreamClient) CloseSend() {
	if s.sendBackend != nil {
		s.sendBackend.CloseSend()
	}
}

func (s *errorIgnoringBuildToolEventStreamClient) Recv() error {
	if s.recvBackend != nil {
		if err := s.recvBackend.Recv(); err != nil {
			s.errorLogger.Log(util.StatusWrap(err, "Ignoring BuildToolEventStream.Recv error"))
			// Don't try receiving any more acknowledgements.
			s.recvBackend = nil
		}
	}
	return s.ackCounter.Acquire(s.ctx, 1)
}
