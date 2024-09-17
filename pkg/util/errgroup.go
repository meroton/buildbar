package util

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// ErrGroupGoAsync runs the function f and returns the error to group.
// In case groupCtx is canceled before f finished, the group will
// be released and any error from f will not be reported.
func ErrGroupGoAsync(ctx context.Context, group *errgroup.Group, f func() error) {
	errorChannel := make(chan error, 1)
	group.Go(func() error {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errorChannel:
			return err
		}
	})
	go func() {
		errorChannel <- f()
	}()
}
