package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mrsimpson/agentic-office/hub/archive/repository"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// The layout below the root is fixed (concept-multi-tenancy): the hub repository and, apart from
// it, two repositories for each tenant: hub/, tenants/<id>/ for the artifacts and
// session-logs/<id>/ for the session logs. The three kinds lie in three directories, so no
// tenant ID can make one repository the same as another.
const (
	hubName         = "hub"
	tenantsName     = "tenants"
	sessionLogsName = "session-logs"
)

// Repos is the set of the repositories of the hub: the hub repository and two for each tenant (the
// artifacts and the session logs). It implements storage.Archives. Each repository is a Repo with
// its own commit queue.
type Repos struct {
	root string
	opts Options

	mu      sync.Mutex
	hub     *Repo
	tenants map[string]*Repo
	// sessionLogs holds the session-log repository of each tenant. Nothing but Archives hands it
	// out, and only to the meeting session.
	sessionLogs map[string]*Repo
}

var _ storage.Archives = (*Repos)(nil)

// OpenRepos opens the repositories below root and creates the hub repository if needed. It opens
// the repositories of every tenant that already has an artifact repository and creates the
// session-log repository of such a tenant if it is missing. opts.Dir is ignored.
func OpenRepos(ctx context.Context, root string, opts Options) (*Repos, error) {
	r := &Repos{root: root, opts: opts, tenants: map[string]*Repo{}, sessionLogs: map[string]*Repo{}}
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
		if r.sessionLogs[e.Name()], err = r.open(ctx, filepath.Join(root, sessionLogsName, e.Name())); err != nil {
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

var _ repository.Repositories = (*Repos)(nil)

// Dir implements repository.Repositories.
func (r *Repos) Dir(tenant string) (string, error) {
	repo, err := r.tenant(tenant)
	if err != nil {
		return "", err
	}
	return repo.Dir(), nil
}

// MainMoved implements repository.Repositories.
func (r *Repos) MainMoved(ctx context.Context, tenant string) error {
	repo, err := r.tenant(tenant)
	if err != nil {
		return err
	}
	return repo.MainMoved(ctx)
}

// Changes returns the changes interface (if-artifact-changes) of a tenant.
func (r *Repos) Changes(tenant string) (*ChangesService, error) {
	repo, err := r.tenant(tenant)
	if err != nil {
		return nil, err
	}
	return NewChangesService(repo), nil
}

func (r *Repos) tenant(id string) (*Repo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if repo := r.tenants[id]; repo != nil {
		return repo, nil
	}
	return nil, repository.ErrNoTenant
}

// HubDir returns the directory of the hub repository.
func (r *Repos) HubDir() string { return r.hub.Dir() }

// Hub implements storage.Archives.
func (r *Repos) Hub() storage.Archive { return r.hub }

// CreateTenant implements storage.Archives. The artifact repository starts with one first commit
// that holds no artifact, so main exists from the start. The session-log repository stays empty.
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
	if err := repo.firstCommit(ctx); err != nil {
		return err
	}
	logs, err := r.open(ctx, filepath.Join(r.root, sessionLogsName, id))
	if err != nil {
		return err
	}
	r.tenants[id], r.sessionLogs[id] = repo, logs
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

// SessionLogs implements storage.Archives.
func (r *Repos) SessionLogs(id string) (storage.Archive, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if repo := r.sessionLogs[id]; repo != nil {
		return repo, nil
	}
	return nil, storage.ErrNoTenant
}
