package configuration

import (
	"context"

	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/grpc"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	pb_buildevents "github.com/meroton/buildbar/proto/configuration/buildevents"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewBuildEventsServerFromConfiguration constructs a BuildEventServer based on
// options specified in a configuration message.
func NewBuildEventsServerFromConfiguration(ctx context.Context, configuration *pb_buildevents.BuildEventStreamConfiguration, grpcClientFactory grpc.ClientFactory) (buildevents.BuildEventServer, error) {
	// Protobuf does not support anchors/aliases like YAML. Have
	// separate 'with_labels' and 'labels' backends that can be used
	// to declare anchors and aliases, respectively.
	switch backend := configuration.GetBackend().(type) {
	case *pb_buildevents.BuildEventStreamConfiguration_Multiplexing:
		config := backend.Multiplexing
		backends := make([]buildevents.BuildEventServer, len(config.Backends))
		for i, innerConfig := range config.Backends {
			backend, err := NewBuildEventsServerFromConfiguration(ctx, innerConfig, grpcClientFactory)
			if err != nil {
				return nil, util.StatusWrapf(err, "Multiplexing backend %d", i+1)
			}
			backends = append(backends, backend)
		}
		return buildevents.NewMultiplexingServer(backends), nil
	case *pb_buildevents.BuildEventStreamConfiguration_Annotated:
		config := backend.Annotated
		base, err := NewBuildEventsServerFromConfiguration(ctx, config.Backend, grpcClientFactory)
		if err != nil {
			return nil, util.StatusWrapf(err, "Annotated %#v", config.Label)
		}
		return buildevents.NewAnnotatedServer(base, config.Label), nil
	case *pb_buildevents.BuildEventStreamConfiguration_Grpc:
		client, err := grpcClientFactory.NewClientFromConfiguration(backend.Grpc)
		if err != nil {
			return nil, util.StatusWrapf(err, "gRPC client for %#v", backend.Grpc.Address)
		}
		return buildevents.NewGrpcClientBuildEventServer(client), nil
	case *pb_buildevents.BuildEventStreamConfiguration_Elasticsearch:
		config := backend.Elasticsearch
		elasticsearchClient, err := elasticsearch.NewClientFromConfiguration(config.Endpoint)
		if err != nil {
			return nil, util.StatusWrap(err, "Failed to create Elasticsearch client")
		}
		go func() {
			elasticsearch.DieWhenConnectionFails(ctx, elasticsearchClient)
		}()
		if config.Index == "" {
			return nil, status.Error(codes.InvalidArgument, "The configured Elasticsearch index is empty")
		}
		return bazelevents.NewIngester(
			bazelevents.NewBazelEventConverter,
			elasticsearch.NewUploader(
				elasticsearchClient,
				config.Index,
				clock.SystemClock,
				util.DefaultErrorLogger,
			),
			util.DefaultErrorLogger,
		), nil
	case *pb_buildevents.BuildEventStreamConfiguration_Buffered:
		config := backend.Buffered
		base, err := NewBuildEventsServerFromConfiguration(ctx, config.Backend, grpcClientFactory)
		if err != nil {
			return nil, util.StatusWrap(err, "Buffered")
		}
		bes, err := buildevents.NewBufferedServer(base, int(config.QueueSize))
		if err != nil {
			return nil, util.StatusWrap(err, "Buffered")
		}
		return bes, nil
	case *pb_buildevents.BuildEventStreamConfiguration_ErrorIgnoring:
		base, err := NewBuildEventsServerFromConfiguration(ctx, backend.ErrorIgnoring, grpcClientFactory)
		if err != nil {
			return nil, util.StatusWrap(err, "Buffered")
		}
		return buildevents.NewErrorIgnoringServer(base, util.DefaultErrorLogger), nil
	default:
		return nil, status.Error(codes.InvalidArgument, "Configuration did not contain a supported build event service backend")
	}
}
