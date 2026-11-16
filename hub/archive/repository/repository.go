package repository

import (
	"context"
	"errors"
)

// ErrNoTenant is the error for a tenant that has no repository.
var ErrNoTenant = errors.New("repository: the tenant has no repository")

// Repositories gives Git access the repository of a tenant. Git access writes no file through
// it: Git itself changes the repository, and Git access only tells the archive afterwards.
type Repositories interface {
	// Dir returns the directory of the bare Git repository of the tenant, where Git finds it.
	// It fails with ErrNoTenant if the tenant has no repository. The branch is main.
	Dir(tenant string) (string, error)
	// MainMoved tells the archive that something other than the archive moved the branch main
	// of the tenant, for example a push. Git access calls it after Git has accepted the push.
	// The archive compares the new state with the last state it knows and sends the change to
	// the followers of the tenant (see if-artifact-changes). The call returns at once and
	// never waits for a follower. It does nothing if main did not move. It fails with
	// ErrNoTenant if the tenant has no repository.
	MainMoved(ctx context.Context, tenant string) error
}
