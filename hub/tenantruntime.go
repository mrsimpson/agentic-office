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
	"github.com/mrsimpson/agentic-office/hub/documents"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/records"
	"github.com/mrsimpson/agentic-office/hub/session"
	"github.com/mrsimpson/agentic-office/hub/slides"
	"github.com/mrsimpson/agentic-office/hub/tasks"
	"github.com/mrsimpson/agentic-office/hub/wiki"
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
	// archiveChanges tells the followers of the tenant which artifacts changed (dec-artifact-sync).
	archiveChanges *archive.ChangesService
	// wiki is the derived view of the wiki of the tenant (concept-wiki); it only reads.
	wiki     *wiki.Service
	sessions *session.Service
	stop     context.CancelFunc
	done     chan struct{}
}

// runtimes holds the runtime of every tenant that has been used, and opens one on first use.
type runtimes struct {
	repos           *archive.Repos
	kinds           tokencheck.Kinds
	signingKey      ed25519.PrivateKey
	archiveInterval time.Duration
	restoreWait     time.Duration
	gracePeriod     time.Duration

	mu sync.Mutex
	m  map[string]*tenantRuntime
}

func newRuntimes(repos *archive.Repos, kinds tokencheck.Kinds, key ed25519.PrivateKey, interval, restoreWait, gracePeriod time.Duration) *runtimes {
	return &runtimes{repos: repos, kinds: kinds, signingKey: key, archiveInterval: interval, restoreWait: restoreWait,
		gracePeriod: gracePeriod, m: map[string]*tenantRuntime{}}
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
	logs, err := r.repos.SessionLogs(tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", tenant, err)
	}
	staging, err := r.repos.Staging(tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", tenant, err)
	}
	logf := func(format string, args ...any) { log.Printf("tenant "+tenant+": "+format, args...) }
	rt := &tenantRuntime{id: tenant, done: make(chan struct{})}
	// The parts follow the archive of the tenant: they reload the files that a push changed
	// (if-archive-watch).
	watcher, err := r.repos.Watcher(tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", tenant, err)
	}
	stagingWatcher, err := r.repos.StagingWatcher(tenant)
	if err != nil {
		return nil, fmt.Errorf("tenant %s: %w", tenant, err)
	}
	if rt.records, err = records.Open(ctx, records.Options{Archive: arch, Watcher: watcher, Staging: staging,
		StagingWatcher: stagingWatcher, Logf: logf}); err != nil {
		return nil, err
	}
	nd, nm, nn := rt.records.Counts()
	if rt.tasks, err = tasks.Open(ctx, arch, watcher, logf); err != nil {
		rt.records.Close()
		return nil, err
	}
	if rt.slides, err = slides.Open(ctx, slides.Options{Archive: arch, Watcher: watcher, Staging: staging,
		StagingWatcher: stagingWatcher, Logf: logf}); err != nil {
		rt.records.Close()
		rt.tasks.Close()
		return nil, err
	}
	// The wiki part follows the artifact repository only, never the staging repository.
	if rt.wiki, err = wiki.Open(ctx, wiki.Options{Archive: arch, Watcher: watcher, Kinds: r.kinds, Logf: logf}); err != nil {
		rt.records.Close()
		rt.tasks.Close()
		rt.slides.Close()
		return nil, err
	}
	opened := false
	defer func() {
		if !opened {
			rt.records.Close()
			rt.tasks.Close()
			rt.slides.Close()
			rt.wiki.Close()
		}
	}()
	logf("%d decisions, %d minutes, %d notes, %d local drafts, %d decks, %d artifacts in the wiki", nd, nm, nn,
		rt.tasks.Count(), rt.slides.Count(), rt.wiki.Count())
	rt.sessions = session.New(session.NewMemoryStore(), session.TokenCheck(), rt.records, r.signingKey)
	// The records, the slides and the documents route a write that names a session by its release
	// status (if-session-release).
	rt.records.UseSessions(rt.sessions.Releases())
	rt.slides.UseSessions(rt.sessions.Releases())
	if rt.documents, err = documents.Open(ctx, documents.Options{Archive: arch, Staging: staging, Sessions: rt.sessions.Releases(),
		Notify: rt.sessions, Logf: logf}); err != nil {
		return nil, err
	}
	rt.archiveRead = archive.NewReadService(arch, staging)
	if rt.archiveChanges, err = r.repos.Changes(tenant); err != nil {
		return nil, err
	}
	if err := rt.sessions.UseArchive(ctx, session.ArchiveOptions{Archive: logs, Interval: r.archiveInterval,
		RestoreWait: r.restoreWait, Logf: logf}); err != nil {
		return nil, err
	}
	// The meeting session owns the release of its sessions (concept-session-release); it rebuilds
	// the deadlines from the staging repository.
	if err := rt.sessions.UseRelease(ctx, session.ReleaseOptions{Staging: staging, GracePeriod: r.gracePeriod, Logf: logf}); err != nil {
		return nil, err
	}
	// The writer of the sessions runs until the hub stops, then writes a last time. The release
	// worker of the tenant runs next to it.
	var runCtx context.Context
	runCtx, rt.stop = context.WithCancel(context.Background())
	released := make(chan struct{})
	go func() { rt.sessions.RunRelease(runCtx); close(released) }()
	go func() { rt.sessions.RunArchive(runCtx); <-released; close(rt.done) }()
	r.m[tenant] = rt
	opened = true
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
		rt.records.Close()
		rt.tasks.Close()
		rt.slides.Close()
		rt.wiki.Close()
	}
}
