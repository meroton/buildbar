package main

import (
	"context"
	"os"

	bes_proto "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/global"
	bb_grpc "github.com/buildbarn/bb-storage/pkg/grpc"
	"github.com/buildbarn/bb-storage/pkg/program"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/meroton/buildbar/proto/configuration/bb_build_event_service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	program.RunMain(func(ctx context.Context, siblingsGroup, dependenciesGroup program.Group) error {
		if len(os.Args) != 2 {
			return status.Error(
				codes.InvalidArgument,
				"Usage: bb_build_event_service bb_build_event_service.jsonnet",
			)
		}
		var configuration bb_build_event_service.ApplicationConfiguration
		if err := util.UnmarshalConfigurationFromFile(os.Args[1], &configuration); err != nil {
			return util.StatusWrapf(err, "Failed to read configuration from %s", os.Args[1])
		}
		lifecycleState, grpcClientFactory, err := global.ApplyConfiguration(configuration.Global)
		if err != nil {
			return util.StatusWrap(err, "Failed to apply global configuration options")
		}

		receiver, err := buildevents.NewBuildEventsServerFromConfiguration(ctx, configuration.Receiver, grpcClientFactory)
		if err != nil {
			return util.StatusWrap(err, "Failed to create receiver")
		}

		if err := bb_grpc.NewServersFromConfigurationAndServe(
			configuration.GrpcServers,
			func(s grpc.ServiceRegistrar) {
				bes_proto.Register(s, receiver)
			},
			siblingsGroup,
		); err != nil {
			return util.StatusWrap(err, "gRPC server failure")
		}

		lifecycleState.MarkReadyAndWait(siblingsGroup)
		return nil
	})
}

func newRelayTargetFromConfiguration() (pbe_pb.PublishBuildEventClient, error) {
	switch cacheReplacementPolicy {
	case pb.CacheReplacementPolicy_FIRST_IN_FIRST_OUT:
		return NewFIFOSet[T](), nil
	case pb.CacheReplacementPolicy_LEAST_RECENTLY_USED:
		return NewLRUSet[T](), nil
	case pb.CacheReplacementPolicy_RANDOM_REPLACEMENT:
		return NewRRSet[T](), nil
	default:
		return nil, status.Errorf(codes.InvalidArgument, "Unknown cache replacement policy")
	}
}
