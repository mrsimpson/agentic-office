package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// The layout below the root is fixed (concept-multi-tenancy): the hub repository and, apart from
// it, one repository for each tenant.
const (
	hubName     = "hub"
	tenantsName = "tenants"
)

// Repos is the set of the repositories of the hub: the hub repository and one for each tenant. It
// implements storage.Archives. Each repository is a Repo with its own commit queue.
type Repos struct {
	root string
	opts Options

	mu      sync.Mutex
	hub     *Repo
	tenants map[string]*Repo
}

var _ storage.Archives = (*Repos)(nil)

// OpenRepos opens the repositories below root and creates the hub repository if needed. It opens
// the repository of every tenant that already has one. opts.Dir is ignored.
func OpenRepos(ctx context.Context, root string, opts Options) (*Repos, error) {
	r := &Repos{root: root, opts: opts, tenants: map[string]*Repo{}}
	var err error
	if r.hub, err = r.open(ctx, filepath.Join(root, hubName)); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, tenantsName))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("archive: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || storage.CheckTenantID(e.Name()) != nil {
			continue
		}
		if r.tenants[e.Name()], err = r.open(ctx, filepath.Join(root, tenantsName, e.Name())); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Repos) open(ctx context.Context, dir string) (*Repo, error) {
	o := r.opts
	o.Dir = dir
	return Open(ctx, o)
}

// HubDir returns the directory of the hub repository.
func (r *Repos) HubDir() string { return r.hub.Dir() }

// Hub implements storage.Archives.
func (r *Repos) Hub() storage.Archive { return r.hub }

// CreateTenant implements storage.Archives.
func (r *Repos) CreateTenant(ctx context.Context, id string) error {
	if err := storage.CheckTenantID(id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tenants[id] != nil {
		return nil
	}
	repo, err := r.open(ctx, filepath.Join(r.root, tenantsName, id))
	if err != nil {
		return err
	}
	r.tenants[id] = repo
	return nil
}

// Tenant implements storage.Archives.
func (r *Repos) Tenant(id string) (storage.Archive, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if repo := r.tenants[id]; repo != nil {
		return repo, nil
	}
	return nil, storage.ErrNoTenant
}
