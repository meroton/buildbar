package buildevents

import (
	"context"
	"sync"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type bufferedServer struct {
	BuildEventServer
	bufferSize int
}

// NewBufferedServer buffers up to bufferSize build events.
func NewBufferedServer(backend BuildEventServer, bufferSize int) (BuildEventServer, error) {
	if bufferSize <= 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Buffer size %d is too small", bufferSize)
	}
	return &bufferedServer{
		BuildEventServer: backend,
		bufferSize:       bufferSize,
	}, nil
}

func (bes *bufferedServer) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	stream, err := bes.BuildEventServer.PublishBuildToolEventStream(ctx)
	if err != nil {
		return nil, err
	}
	return newBufferedBuildToolEventStreamClient(ctx, stream, bes.bufferSize), nil
}

type bufferedBuildToolEventStreamClient struct {
	backend BuildToolEventStreamClient
	// requestBuffer is never closed.
	requestBuffer chan *build.PublishBuildToolEventStreamRequest
	lock          sync.Mutex
	sendError     error
}

func newBufferedBuildToolEventStreamClient(ctx context.Context, backend BuildToolEventStreamClient, bufferSize int) BuildToolEventStreamClient {
	ret := &bufferedBuildToolEventStreamClient{
		backend:       backend,
		requestBuffer: make(chan *build.PublishBuildToolEventStreamRequest, bufferSize-1),
		lock:          sync.Mutex{},
		sendError:     nil,
	}
	// Send asynchronously.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case req, ok := <-ret.requestBuffer:
				if !ok {
					backend.CloseSend()
					return
				}
				if err := backend.Send(req); err != nil {
					ret.lock.Lock()
					ret.sendError = err
					ret.lock.Unlock()
					return
				}
			}
		}
	}()
	return ret
}

func (s *bufferedBuildToolEventStreamClient) Send(req *build.PublishBuildToolEventStreamRequest) error {
	s.lock.Lock()
	err := s.sendError
	s.lock.Unlock()
	if err != nil {
		return err
	}
	s.requestBuffer <- req
	return nil
}

func (s *bufferedBuildToolEventStreamClient) CloseSend() {
	// CloseSend must not be called in concurrently with Send(),
	// so closing the channel should be fine.
	s.lock.Lock()
	s.sendError = status.Error(codes.FailedPrecondition, "The stream has already been closed")
	s.lock.Unlock()
	// Trigger CloseSend() in the send goroutine.
	close(s.requestBuffer)
}

func (s *bufferedBuildToolEventStreamClient) Recv() error {
	return s.backend.Recv()
}
