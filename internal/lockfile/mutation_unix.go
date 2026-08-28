//go:build unix

package lockfile

import (
	"fmt"
	"os"
	"syscall"
)

// acquireMutationOS locks the stable repository directory itself, avoiding a
// second persistent lock file whose mere existence could dirty older repos.
func acquireMutationOS(repoRoot string) (func() error, error) {
	f, err := os.Open(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open repository for Delivery mutation serialization: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock repository for Delivery mutation serialization: %w", err)
	}
	return func() error {
		if err := f.Close(); err != nil {
			return fmt.Errorf("release Delivery mutation serialization: %w", err)
		}
		return nil
	}, nil
}
