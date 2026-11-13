package evalplan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kninetimmy/orch/internal/execx"
)

// PreviewRepository adds fixed repository and storage boundaries to Preview.
// Only the proposal file must be repository-relative; referenced artifacts keep
// Preview's explicit local-path rules. CLI Preview retains its existing behavior.
func PreviewRepository(ctx context.Context, repo, storageRoot, file string, runner execx.Runner) (*Record, error) {
	if err := relativeName(file); err != nil {
		return nil, fmt.Errorf("plan file must be repository-relative: %w", err)
	}
	repository, err := openGuarded(repo)
	if err != nil {
		return nil, fmt.Errorf("bound evaluation repository identity: %w", err)
	}
	defer repository.close()
	storage, err := openGuarded(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("evaluation storage binding: %w", err)
	}
	defer storage.close()
	data, err := repository.read(file, MaxPlanBytes)
	if err != nil {
		return nil, fmt.Errorf("plan file: %w", err)
	}
	record, err := preparePreview(ctx, repository.path, filepath.Join(repository.path, filepath.FromSlash(file)), data, runner)
	if err != nil {
		return nil, err
	}
	destination, err := openGuarded(record.Plan.StorageRoot)
	if err != nil {
		return nil, fmt.Errorf("proposal storage destination identity: %w", err)
	}
	defer destination.close()
	same, err := samePath(destination.path, storage.path)
	if err != nil || !same || !os.SameFile(destination.info, storage.info) {
		return nil, fmt.Errorf("proposal storage_root does not match the launch-bound evaluation storage root: %v", err)
	}
	if err := errors.Join(repository.check(), destination.check()); err != nil {
		return nil, err
	}
	// Publish through the already opened bound directory, never a proposal-selected
	// destination. This is the same bounded serialization/no-replace publisher as
	// save, with directory identity checks before and after the write.
	name := strings.TrimPrefix(record.PlanDigest, "sha256:") + ".json"
	if err := errors.Join(storage.publish(name, record), repository.check(), destination.check()); err != nil {
		return nil, err
	}
	return record, nil
}
