package sync

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mrsimpson/agentic-office/client/sync/control"
)

// MainDirectory implements control.Sync. The common Git directory is the ".git" directory of the
// main directory, for the main directory itself and for every linked worktree.
func (s *syncer) MainDirectory(ctx context.Context, dir string) (string, error) {
	git, err := s.gitPath()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	common, ok := query(ctx, git, abs, "rev-parse", "--git-common-dir")
	if !ok || common == "" {
		return "", fmt.Errorf("%w: %s is no Git repository", control.ErrNotSetUp, abs)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(abs, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("%w: %s has no main directory", control.ErrNotSetUp, abs)
	}
	return s.mainDirectory(git, filepath.Dir(common))
}
