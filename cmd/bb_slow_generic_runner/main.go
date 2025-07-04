package main

import (
	"context"
	"os"

	"github.com/buildbarn/bb-remote-execution/pkg/cleaner"
	re_filesystem "github.com/buildbarn/bb-remote-execution/pkg/filesystem"
	runner_pb "github.com/buildbarn/bb-remote-execution/pkg/proto/runner"
	"github.com/buildbarn/bb-remote-execution/pkg/runner"
	"github.com/buildbarn/bb-storage/pkg/filesystem"
	"github.com/buildbarn/bb-storage/pkg/filesystem/path"
	"github.com/buildbarn/bb-storage/pkg/global"
	bb_grpc "github.com/buildbarn/bb-storage/pkg/grpc"
	"github.com/buildbarn/bb-storage/pkg/program"
	"github.com/buildbarn/bb-storage/pkg/util"
	own_runner "github.com/meroton/buildbar/pkg/runner"
	"github.com/meroton/buildbar/proto/configuration/bb_slow_generic_runner"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	program.RunMain(func(ctx context.Context, siblingsGroup, dependenciesGroup program.Group) error {
		if len(os.Args) != 2 {
			return status.Error(codes.InvalidArgument, "Usage: bb_slow_generic_runner bb_slow_generic_runner.jsonnet")
		}
		var configuration bb_slow_generic_runner.ApplicationConfiguration
		if err := util.UnmarshalConfigurationFromFile(os.Args[1], &configuration); err != nil {
			return util.StatusWrapf(err, "Failed to read configuration from %#v", os.Args[1])
		}
		lifecycleState, grpcClientFactory, err := global.ApplyConfiguration(configuration.Global)
		if err != nil {
			return util.StatusWrap(err, "Failed to apply global configuration options")
		}

		buildDirectoryPath, scopeWalker := path.EmptyBuilder.Join(path.NewAbsoluteScopeWalker(path.VoidComponentWalker))
		if err := path.Resolve(path.LocalFormat.NewParser(configuration.BuildDirectoryPath), scopeWalker); err != nil {
			return util.StatusWrapf(err, "Failed to resolve build directory %#v", configuration.BuildDirectoryPath)
		}
		buildDirectory := re_filesystem.NewLazyDirectory(
			func() (filesystem.DirectoryCloser, error) {
				return filesystem.NewLocalDirectory(buildDirectoryPath)
			})

		commandCreator := own_runner.NewCommandWrapperCreator(configuration.RunCommandWrapper)

		setTmpdirEnvironmentVariable := false
		r := runner.NewLocalRunner(
			buildDirectory,
			buildDirectoryPath,
			commandCreator,
			setTmpdirEnvironmentVariable)

		if len(configuration.CleanerCommand) > 0 {
			r = runner.NewCleanRunner(
				r,
				cleaner.NewIdleInvoker(cleaner.NewCommandRunningCleaner(
					configuration.CleanerCommand[0],
					configuration.CleanerCommand[1:])))
		}

		// Command to succeed for the runner to be healthy.
		if len(configuration.ReadinessCommand) > 0 {
			r = own_runner.NewRedinessCommandRunner(r, configuration.ReadinessCommand)
		}

		if err := bb_grpc.NewServersFromConfigurationAndServe(
			configuration.GrpcServers,
			func(s grpc.ServiceRegistrar) {
				runner_pb.RegisterRunnerServer(s, r)
			},
			siblingsGroup,
			grpcClientFactory,
		); err != nil {
			return util.StatusWrap(err, "gRPC server failure")
		}

		lifecycleState.MarkReadyAndWait(siblingsGroup)
		return nil
	})
}
