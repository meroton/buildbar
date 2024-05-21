package main

import (
	"context"
	"os"

	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	blobstore_configuration "github.com/buildbarn/bb-storage/pkg/blobstore/configuration"
	"github.com/buildbarn/bb-storage/pkg/clock"
	"github.com/buildbarn/bb-storage/pkg/global"
	bb_grpc "github.com/buildbarn/bb-storage/pkg/grpc"
	"github.com/buildbarn/bb-storage/pkg/program"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/completedaction"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/meroton/buildbar/proto/configuration/bb_completed_actions_ingester"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	program.RunMain(func(ctx context.Context, siblingsGroup, dependenciesGroup program.Group) error {
		if len(os.Args) != 2 {
			return status.Error(
				codes.InvalidArgument,
				"Usage: bb_completed_actions_ingester bb_completed_actions_ingester.jsonnet",
			)
		}
		var configuration bb_completed_actions_ingester.ApplicationConfiguration
		if err := util.UnmarshalConfigurationFromFile(os.Args[1], &configuration); err != nil {
			return util.StatusWrapf(err, "Failed to read configuration from %s", os.Args[1])
		}
		lifecycleState, grpcClientFactory, err := global.ApplyConfiguration(configuration.Global)
		if err != nil {
			return util.StatusWrap(err, "Failed to apply global configuration options")
		}

		blobAccessCreator := blobstore_configuration.NewCASBlobAccessCreator(
			grpcClientFactory,
			int(configuration.MaximumMessageSizeBytes))
		contentAddressableStorage, err := blobstore_configuration.NewBlobAccessFromConfiguration(
			dependenciesGroup,
			configuration.ContentAddressableStorage,
			blobAccessCreator)
		if err != nil {
			return util.StatusWrap(err, "Failed to create CAS")
		}

		elasticsearchClient, err := elasticsearch.NewClientFromConfiguration(configuration.Elasticsearch)
		if err != nil {
			return util.StatusWrap(err, "Failed to create Elasticsearch client")
		}
		go func() {
			elasticsearch.DieWhenConnectionFails(ctx, elasticsearchClient)
		}()

		if configuration.ElasticsearchIndex == "" {
			return status.Error(codes.InvalidArgument, "The configured Elasticsearch index is empty")
		}
		ingester := completedaction.NewIngester(
			elasticsearch.NewUploader(
				elasticsearchClient,
				configuration.ElasticsearchIndex,
				clock.SystemClock,
				util.DefaultErrorLogger,
			),
			completedaction.NewConverter(
				contentAddressableStorage.BlobAccess,
				int(configuration.MaximumMessageSizeBytes),
			),
		)

		if err := bb_grpc.NewServersFromConfigurationAndServe(
			configuration.GrpcServers,
			func(s grpc.ServiceRegistrar) {
				cal_proto.RegisterCompletedActionLoggerServer(s, ingester)
			},
			siblingsGroup,
		); err != nil {
			return util.StatusWrap(err, "gRPC server failure")
		}

		lifecycleState.MarkReadyAndWait(siblingsGroup)
		return nil
	})
}
