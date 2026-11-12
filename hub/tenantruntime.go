package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"log"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/archive"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/documents"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/records"
	"github.com/mrsimpson/agentic-office/hub/session"
	"github.com/mrsimpson/agentic-office/hub/slides"
	"github.com/mrsimpson/agentic-office/hub/tasks"
)

// tenantRuntime is the content of one tenant: its session service, records, tasks and slide
// decks. They know nothing about tenants. Each reads and writes the repository of the tenant only,
// so the data of two tenants never meets (concept-multi-tenancy).
type tenantRuntime struct {
	id      string
	records *records.Service
	tasks   *tasks.Service
	slides  *slides.Service
	// documents and archiveRead are the text documents of the tenant and the read interface of its
	// repository (dec-artifacts).
	documents   *documents.Service
	archiveRead *archive.ReadService
	sessions    *session.Service
	stop        context.CancelFunc
	done        chan struct{}
}

// runtimes holds the runtime of every tenant that has been used, and opens one on first use.
type runtimes struct {
	repos           storage.Archives
	signingKey      ed25519.PrivateKey
	archiveInterval time.Duration
	restoreWait     time.Duration

	mu sync.Mutex
	m  map[string]*tenantRuntime
}

func newRuntimes(repos storage.Archives, key ed25519.PrivateKey, interval, restoreWait time.Duration) *runtimes {
	return &runtimes{repos: repos, signingKey: key, archiveInterval: interval, restoreWait: restoreWait,
		m: map[string]*tenantRuntime{}}
}

// get returns the runtime of a tenant and opens it if needed: it reads the records, tasks and
// slide decks of the tenant repository and restores its sessions.
func (r *runtimes) get(ctx context.Context, tenant string) (*tenantRuntime, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if rt := r.m[tenant]; rt != nil {
		return rt, nil
	}
	arch, err := r.repos.Tenant(tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", tenant, err)
	}
	logf := func(format string, args ...any) { log.Printf("tenant "+tenant+": "+format, args...) }
	rt := &tenantRuntime{id: tenant, done: make(chan struct{})}
	if rt.records, err = records.Open(ctx, arch); err != nil {
		return nil, err
	}
	nd, nm, nn := rt.records.Counts()
	if rt.tasks, err = tasks.Open(ctx, arch); err != nil {
		return nil, err
	}
	if rt.slides, err = slides.Open(ctx, arch); err != nil {
		return nil, err
	}
	logf("%d decisions, %d minutes, %d notes, %d local drafts, %d decks", nd, nm, nn, rt.tasks.Count(), rt.slides.Count())
	rt.sessions = session.New(session.NewMemoryStore(), session.TokenCheck(), rt.records, r.signingKey)
	rt.documents = documents.New(arch, rt.sessions)
	rt.archiveRead = archive.NewReadService(arch)
	if err := rt.sessions.UseArchive(ctx, session.ArchiveOptions{Archive: arch, Interval: r.archiveInterval,
		RestoreWait: r.restoreWait, Logf: logf}); err != nil {
		return nil, err
	}
	// The writer of the sessions runs until the hub stops, then writes a last time.
	var runCtx context.Context
	runCtx, rt.stop = context.WithCancel(context.Background())
	go func() { rt.sessions.RunArchive(runCtx); close(rt.done) }()
	r.m[tenant] = rt
	return rt, nil
}

// forCall returns the runtime of the tenant of a call. The identity interceptor has checked that
// the caller is a member of the tenant and has put the tenant into the caller.
func (r *runtimes) forCall(ctx context.Context) (*tenantRuntime, error) {
	c, _ := tokencheck.CallerFrom(ctx)
	if c.TenantID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "name the tenant of the call")
	}
	rt, err := r.get(context.WithoutCancel(ctx), c.TenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	return rt, nil
}

// stopAll stops the writers of all tenants; each writes its sessions a last time.
func (r *runtimes) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rt := range r.m {
		rt.stop()
	}
	for _, rt := range r.m {
		<-rt.done
	}
}
