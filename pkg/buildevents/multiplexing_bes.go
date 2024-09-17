package buildevents

import (
	"context"
	"io"
	"log"
	"math"

	"github.com/meroton/buildbar/pkg/util"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
)

type multiplexingServer struct {
	targets []BuildEventServer
}

// NewMultiplexingServer creates a BuildEventServer that is distributing the
// build event stream to multiple backends.
func NewMultiplexingServer(targets []BuildEventServer) BuildEventServer {
	return &multiplexingServer{
		targets: targets,
	}
}

func (bes *multiplexingServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	group, groupCtx := errgroup.WithContext(ctx)
	for _, target := range bes.targets {
		util.ErrGroupGoAsync(groupCtx, group, func() error {
			return target.PublishLifecycleEvent(groupCtx, in)
		})
	}
	return group.Wait()
}

func (bes *multiplexingServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	recvGroup, recvContext := errgroup.WithContext(ctx)
	backends := make([]BuildToolEventStreamClient, len(bes.targets))
	ackCounters := make([]*semaphore.Weighted, len(backends))
	for i, target := range bes.targets {
		backend, err := target.PublishBuildToolEventStream(ctx)
		backends[i] = backend
		if err != nil {
			return nil, err
		}
		ackCounter := semaphore.NewWeighted(math.MaxInt64)
		if !ackCounter.TryAcquire(math.MaxInt64) {
			log.Fatalf("Semaphore construction of size MaxInt64 failed")
		}
		ackCounters[i] = ackCounter
		// Receive acks asynchronously.
		recvGroup.Go(func() error {
			for {
				if err := backend.Recv(); err != nil {
					if err == io.EOF {
						return nil
					}
					return err
				}
				ackCounter.Release(1)
			}
		})
	}
	go func() {
		recvGroup.Wait()
	}()
	return &multiplexingBuildToolEventStreamClient{
		sendBackends: backends,
		recvContext:  recvContext,
		ackCounters:  ackCounters,
	}, nil
}

type multiplexingBuildToolEventStreamClient struct {
	sendBackends []BuildToolEventStreamClient
	recvContext  context.Context
	ackCounters  []*semaphore.Weighted
}

func (s *multiplexingBuildToolEventStreamClient) Send(req *build.PublishBuildToolEventStreamRequest) error {
	for _, backend := range s.sendBackends {
		if err := backend.Send(req); err != nil {
			return err
		}
	}
	return nil
}

func (s *multiplexingBuildToolEventStreamClient) CloseSend() {
	for _, backend := range s.sendBackends {
		backend.CloseSend()
	}
}

func (s *multiplexingBuildToolEventStreamClient) Recv() error {
	for _, ackCounter := range s.ackCounters {
		if err := ackCounter.Acquire(s.recvContext, 1); err != nil {
			return err
		}
	}
	return nil
}
