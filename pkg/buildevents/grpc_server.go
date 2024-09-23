package buildevents

import (
	"context"
	"io"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/meroton/buildbar/pkg/util"
	"golang.org/x/sync/errgroup"
	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewGrpcServer creates a new BuildEventServer that acts as
// a gRPC build.PublishBuildEventServer.
func NewGrpcServer(backend BuildEventServer) build.PublishBuildEventServer {
	return &grpcServer{
		backend: backend,
	}
}

type grpcServer struct {
	backend BuildEventServer
}

func (bes *grpcServer) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) (*empty.Empty, error) {
	err := bes.backend.PublishLifecycleEvent(ctx, in)
	return &empty.Empty{}, err
}

func (bes *grpcServer) PublishBuildToolEventStream(upstream build.PublishBuildEvent_PublishBuildToolEventStreamServer) error {
	group, groupCtx := errgroup.WithContext(upstream.Context())
	stream, err := bes.backend.PublishBuildToolEventStream(groupCtx)
	if err != nil {
		return err
	}
	// Receive the first message to get the first sequence number.
	initialRequest, err := upstream.Recv()
	if err != nil {
		stream.CloseSend()
		return err
	}
	bufferedInitialRequest := &BufferedPublishBuildToolEventStreamRequest{
		PublishBuildToolEventStreamRequest: initialRequest,
	}
	if err := stream.Send(bufferedInitialRequest); err != nil {
		return err
	}

	initialStreamID := initialRequest.OrderedBuildEvent.GetStreamId()
	initialRequestNumber := initialRequest.OrderedBuildEvent.GetSequenceNumber()

	// Forward requests.
	util.ErrGroupGoAsync(groupCtx, group, func() error {
		defer stream.CloseSend()
		nextRequestNumber := initialRequestNumber + 1
		for {
			request, err := upstream.Recv()
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			streamID := request.OrderedBuildEvent.GetStreamId()
			requestNumber := request.OrderedBuildEvent.GetSequenceNumber()
			if !equalStreamID(streamID, initialStreamID) {
				return status.Errorf(codes.InvalidArgument, "gRPC server: Expected consistent request streamId %v but got %v", initialStreamID, streamID)
			} else if requestNumber != nextRequestNumber {
				return status.Errorf(codes.InvalidArgument, "gRPC server: Expected consecutive request sequence number %d but got %d", nextRequestNumber, requestNumber)
			}
			bufferedRequest := &BufferedPublishBuildToolEventStreamRequest{
				PublishBuildToolEventStreamRequest: request,
			}
			if err := stream.Send(bufferedRequest); err != nil {
				return err
			}
			nextRequestNumber++
		}
	})
	// Forward responses.
	util.ErrGroupGoAsync(groupCtx, group, func() error {
		response := &build.PublishBuildToolEventStreamResponse{
			StreamId:       initialStreamID,
			SequenceNumber: initialRequestNumber,
		}
		for {
			if err := stream.Recv(); err != nil {
				if err == io.EOF {
					return nil
				}
				return err
			}
			if err := upstream.Send(response); err != nil {
				return err
			}
			response.SequenceNumber++
		}
	})
	return group.Wait()
}
