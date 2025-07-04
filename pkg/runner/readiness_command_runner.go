package runner

import (
	"context"
	"os/exec"

	runner_pb "github.com/buildbarn/bb-remote-execution/pkg/proto/runner"
	"github.com/buildbarn/bb-storage/pkg/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/emptypb"
)

type redinessCommandRunner struct {
	base      runner_pb.RunnerServer
	arguments []string
}

// NewRedinessCommandRunner creates a decorator of RunnerServer
// that is only healthy when certain command succeeds.
func NewRedinessCommandRunner(base runner_pb.RunnerServer, arguments []string) runner_pb.RunnerServer {
	return &redinessCommandRunner{
		base:      base,
		arguments: arguments,
	}
}

func (r *redinessCommandRunner) runReadinessChecker(ctx context.Context) error {
	return exec.CommandContext(ctx, r.arguments[0], r.arguments[1:]...).Run()
}

func (r *redinessCommandRunner) CheckReadiness(ctx context.Context, request *runner_pb.CheckReadinessRequest) (*emptypb.Empty, error) {
	if err := r.runReadinessChecker(ctx); err != nil {
		return nil, util.StatusWrapWithCode(err, codes.Internal, "Failed to run readiness command")
	}
	return r.base.CheckReadiness(ctx, request)
}

func (r *redinessCommandRunner) Run(ctx context.Context, request *runner_pb.RunRequest) (*runner_pb.RunResponse, error) {
	response, err := r.base.Run(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.ExitCode != 0 {
		// Execution failues may be caused by the system to fail. Suppress the
		// results in case the readiness check fails.
		if err := r.runReadinessChecker(ctx); err != nil {
			return nil, util.StatusWrap(err, "The runner became unready during execution")
		}
	}
	return response, nil
}
