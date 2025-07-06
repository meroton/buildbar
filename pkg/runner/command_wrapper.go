package runner

import (
	"context"
	"os/exec"

	bb_runner "github.com/buildbarn/bb-remote-execution/pkg/runner"
	"github.com/buildbarn/bb-storage/pkg/filesystem/path"
	"github.com/buildbarn/bb-storage/pkg/util"
)

// NewCommandWrapperCreator returns a CommandCreator that prefixes the remote
// execution command arguments with more arguments. This way, the command can be
// wrapped in a shell script or another program that performs additional actions
// before executing the actual command.
func NewCommandWrapperCreator(commandWrapperArguments []string) bb_runner.CommandCreator {
	return func(ctx context.Context, arguments []string, inputRootDirectory *path.Builder, workingDirectoryParser path.Parser, pathVariable string) (*exec.Cmd, error) {
		inputRootDirectoryStr, err := path.LocalFormat.GetString(inputRootDirectory)
		if err != nil {
			return nil, util.StatusWrap(err, "Failed to create local representation of input root directory")
		}
		relWorkingDirectory, scopeWalker := path.EmptyBuilder.Join(path.NewRelativeScopeWalker(path.VoidComponentWalker))
		if err := path.Resolve(workingDirectoryParser, scopeWalker); err != nil {
			return nil, util.StatusWrap(err, "Failed to resolve relative working directory")
		}
		relWorkingDirectoryStr, err := path.LocalFormat.GetString(relWorkingDirectory)
		if err != nil {
			return nil, util.StatusWrap(err, "Failed to create local representation of relative working directory")
		}
		fullCommandArguments := append(append(commandWrapperArguments, inputRootDirectoryStr, relWorkingDirectoryStr), arguments...)

		// Run the wrapper in the current working directory, the wrapper can change directory if wanted.
		cmd := exec.CommandContext(ctx, fullCommandArguments[0], fullCommandArguments[1:]...)
		return cmd, nil
	}
}
