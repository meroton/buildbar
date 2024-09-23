package buildevents

import (
	"context"

	"github.com/buildbarn/bb-storage/pkg/util"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type grpcClient struct {
	backend build.PublishBuildEventClient
}

// NewGrpcClientBuildEventServer creates a gRPC client for passing
// build events remotely.
func NewGrpcClientBuildEventServer(client grpc.ClientConnInterface) BuildEventServer {
	return &grpcClient{
		backend: build.NewPublishBuildEventClient(client),
	}
}

func (bes *grpcClient) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	_, err := bes.backend.PublishLifecycleEvent(ctx, in)
	return err
}

func (bes *grpcClient) PublishBuildToolEventStream(ctx context.Context) (BuildToolEventStreamClient, error) {
	stream, err := bes.backend.PublishBuildToolEventStream(ctx)
	if err != nil {
		return nil, util.StatusWrap(err, "gRPC client PublishBuildToolEventStream")
	}
	return &grpcBuildToolEventStreamClient{
		backend:        stream,
		gotInitialData: make(chan struct{}),
	}, nil
}

type grpcBuildToolEventStreamClient struct {
	backend                build.PublishBuildEvent_PublishBuildToolEventStreamClient
	initialStreamID        *build.StreamId
	nextSendSequenceNumber int64
	nextRecvSequenceNumber int64
	gotInitialData         chan struct{}
}

func (s *grpcBuildToolEventStreamClient) Send(request *BufferedPublishBuildToolEventStreamRequest) error {
	select {
	case <-s.gotInitialData:
		// noop
	default:
		if request.OrderedBuildEvent == nil {
			return status.Errorf(codes.InvalidArgument, "Missing OrderedBuildEvent in build tool event stream request")
		}
		s.initialStreamID = request.OrderedBuildEvent.StreamId
		initialSequenceNumber := request.OrderedBuildEvent.SequenceNumber
		s.nextSendSequenceNumber = initialSequenceNumber + 1
		s.nextRecvSequenceNumber = initialSequenceNumber
		close(s.gotInitialData)
	}
	streamID := request.OrderedBuildEvent.GetStreamId()
	requestNumber := request.OrderedBuildEvent.GetSequenceNumber()
	if !equalStreamID(streamID, s.initialStreamID) {
		return status.Errorf(codes.InvalidArgument, "gRPC client: Expected consistent request streamId %v but got %v", s.initialStreamID, streamID)
	} else if requestNumber != s.nextSendSequenceNumber {
		return status.Errorf(codes.InvalidArgument, "gRPC client: Expected consecutive request sequence number %d but got %d", s.nextSendSequenceNumber, requestNumber)
	}
	s.nextSendSequenceNumber++

	s.backend.Send(request.PublishBuildToolEventStreamRequest)
	return nil
}

func (s *grpcBuildToolEventStreamClient) CloseSend() {
	err := s.backend.CloseSend()
	if err != nil {
		// Assume that the backend is responding with errors through
		// Send() and Recv().
	}
}

func (s *grpcBuildToolEventStreamClient) Recv() error {
	response, err := s.backend.Recv()
	if err != nil {
		return err
	}
	select {
	case <-s.gotInitialData:
		// noop
	default:
		return status.Error(codes.InvalidArgument, "gRPC client: Got response before request")
	}

	streamID := response.GetStreamId()
	responseNumber := response.GetSequenceNumber()
	if !equalStreamID(streamID, s.initialStreamID) {
		return status.Errorf(codes.InvalidArgument, "gRPC client: Expected consistent response streamId %v but got %v", s.initialStreamID, streamID)
	} else if responseNumber != s.nextRecvSequenceNumber {
		return status.Errorf(codes.InvalidArgument, "gRPC client: Expected consecutive response number %d but got %d", s.nextRecvSequenceNumber, responseNumber)
	}
	s.nextRecvSequenceNumber++

	return nil
}
