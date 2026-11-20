package wiki

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	wikiv1 "github.com/mrsimpson/agentic-office/hub/wiki/api"
	"github.com/mrsimpson/agentic-office/hub/wiki/api/wikiv1connect"
)

// The number of hits of a search without a limit, and the most hits of one search.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Options are the inputs of Open.
type Options struct {
	// Archive is the artifact repository of the tenant (if-archive-storage).
	Archive storage.Archive
	// Watcher reports the changes of the artifact repository (if-archive-watch).
	Watcher watch.Watcher
	// Logf logs; nil means log.Printf.
	Logf func(string, ...any)
}

// Service keeps the view of the wiki of one tenant and implements the wiki interface
// (if-wiki-api).
type Service struct {
	arch    storage.Archive
	watcher watch.Watcher
	logf    func(string, ...any)
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu sync.RWMutex
	v  *view
}

var _ wikiv1connect.WikiServiceHandler = (*Service)(nil)

// Open reads every file of the artifact repository and returns the service with its view. The
// service follows the repository until Close.
func Open(ctx context.Context, o Options) (*Service, error) {
	if o.Logf == nil {
		o.Logf = log.Printf
	}
	// Watch first, then read: a change in between is reported and read again.
	sub, err := o.Watcher.Watch()
	if err != nil {
		return nil, fmt.Errorf("wiki: watch the archive: %w", err)
	}
	s := &Service{arch: o.Archive, watcher: o.Watcher, logf: o.Logf, done: make(chan struct{})}
	if err := s.loadAll(ctx); err != nil {
		sub.Close()
		return nil, err
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.run(sub)
	return s, nil
}

// Close stops following the archive. The service still answers calls.
func (s *Service) Close() {
	s.cancel()
	<-s.done
}

// Count returns the number of artifacts in the index.
func (s *Service) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.v.entries)
}

// loadAll builds the view from every file of the repository.
func (s *Service) loadAll(ctx context.Context) error {
	files, err := s.arch.Read(ctx, storage.Root)
	if err != nil {
		return fmt.Errorf("wiki: read the archive: %w", err)
	}
	v := newView()
	for _, f := range files {
		v.set(f.Path, f.Data)
	}
	s.mu.Lock()
	s.v = v
	s.mu.Unlock()
	return nil
}

// run follows the changes until Close. A subscription that was dropped is replaced, and then
// every file is read again, because changes may have been missed.
func (s *Service) run(sub watch.Subscription) {
	defer close(s.done)
	for {
		s.pump(sub)
		sub.Close()
		if s.ctx.Err() != nil {
			return
		}
		s.logf("wiki: the archive dropped the watcher (%v); watching again and reading every file", sub.Err())
		var err error
		if sub, err = s.watcher.Watch(); err != nil {
			s.logf("wiki: cannot watch the archive again; the view stays as it is until the hub restarts: %v", err)
			return
		}
		if err := s.loadAll(s.ctx); err != nil {
			s.logf("%v", err)
		}
	}
}

func (s *Service) pump(sub watch.Subscription) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case changes, ok := <-sub.Changes():
			if !ok {
				return
			}
			if err := s.reload(s.ctx, changes); err != nil {
				s.logf("%v", err)
			}
		}
	}
}

// reload reads the files that changed and puts them into the view.
func (s *Service) reload(ctx context.Context, changes []watch.Change) error {
	type update struct {
		path string
		data []byte
		gone bool
	}
	var updates []update
	for _, ch := range changes {
		if ch.Kind == watch.Removed {
			updates = append(updates, update{path: ch.Path, gone: true})
			continue
		}
		files, err := s.arch.Read(ctx, ch.Path)
		if err != nil {
			return fmt.Errorf("wiki: read %s: %w", ch.Path, err)
		}
		u := update{path: ch.Path, gone: true}
		for _, f := range files {
			if f.Path == ch.Path {
				u = update{path: f.Path, data: f.Data}
			}
		}
		updates = append(updates, u)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range updates {
		if u.gone {
			s.v.remove(u.path)
		} else {
			s.v.set(u.path, u.data)
		}
	}
	return nil
}

// checkCaller fails unless the call has the token of a participant.
func checkCaller(ctx context.Context) error {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return connect.NewError(connect.CodePermissionDenied, "a stage link cannot read the wiki")
	}
	return nil
}

func artifactOf(e *entry) *wikiv1.Artifact {
	if e == nil {
		return nil
	}
	return &wikiv1.Artifact{Path: e.path, Schema: e.desc.Schema, Title: e.desc.Title, Summary: e.desc.Summary,
		Tags: slices.Clone(e.desc.Tags), Source: e.desc.Source}
}

// ListArtifacts implements if-wiki-api.
func (s *Service) ListArtifacts(ctx context.Context, req *wikiv1.ListArtifactsRequest) (*wikiv1.ListArtifactsResponse, error) {
	if err := checkCaller(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := &wikiv1.ListArtifactsResponse{}
	counts := map[string]uint32{}
	for _, e := range s.v.sorted() {
		tags := slices.Clone(e.desc.Tags)
		slices.Sort(tags)
		for _, t := range slices.Compact(tags) {
			counts[t]++
		}
		if req.GetTag() == "" || slices.Contains(e.desc.Tags, req.GetTag()) {
			res.Artifacts = append(res.Artifacts, artifactOf(e))
		}
	}
	for t, n := range counts {
		res.Tags = append(res.Tags, &wikiv1.TagCount{Tag: t, Count: n})
	}
	slices.SortFunc(res.Tags, func(a, b *wikiv1.TagCount) int { return strings.Compare(a.GetTag(), b.GetTag()) })
	return res, nil
}

// GetArtifact implements if-wiki-api.
func (s *Service) GetArtifact(ctx context.Context, req *wikiv1.GetArtifactRequest) (*wikiv1.GetArtifactResponse, error) {
	if err := checkCaller(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetPath()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "name the path of the artifact")
	}
	p := cleanPath(req.GetPath())
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.v.entries[p]
	if e == nil {
		return nil, connect.NewError(connect.CodeNotFound, "the wiki has no artifact "+p)
	}
	link := func(target string) *wikiv1.Link {
		return &wikiv1.Link{Path: target, Broken: !s.v.exists(target), Artifact: artifactOf(s.v.entries[target])}
	}
	res := &wikiv1.GetArtifactResponse{Artifact: artifactOf(e)}
	for _, l := range e.desc.Links {
		res.Links = append(res.Links, link(l))
	}
	for _, src := range e.sources {
		res.Sources = append(res.Sources, link(src))
	}
	for _, o := range s.v.sorted() {
		if slices.Contains(o.desc.Links, p) {
			res.Backlinks = append(res.Backlinks, artifactOf(o))
		}
		if slices.Contains(o.sources, p) {
			res.CitedBy = append(res.CitedBy, artifactOf(o))
		}
	}
	return res, nil
}

// ListBrokenLinks implements if-wiki-api.
func (s *Service) ListBrokenLinks(ctx context.Context, _ *wikiv1.ListBrokenLinksRequest) (*wikiv1.ListBrokenLinksResponse, error) {
	if err := checkCaller(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := &wikiv1.ListBrokenLinksResponse{}
	for _, e := range s.v.sorted() {
		var broken []*wikiv1.BrokenLink
		for _, l := range e.desc.Links {
			if !s.v.exists(l) {
				broken = append(broken, &wikiv1.BrokenLink{FromPath: e.path, ToPath: l})
			}
		}
		for _, src := range e.sources {
			if !s.v.exists(src) {
				broken = append(broken, &wikiv1.BrokenLink{FromPath: e.path, ToPath: src, Source: true})
			}
		}
		slices.SortStableFunc(broken, func(a, b *wikiv1.BrokenLink) int { return strings.Compare(a.GetToPath(), b.GetToPath()) })
		res.Links = append(res.Links, broken...)
	}
	return res, nil
}

// ListIngestQueue implements if-wiki-api.
func (s *Service) ListIngestQueue(ctx context.Context, _ *wikiv1.ListIngestQueueRequest) (*wikiv1.ListIngestQueueResponse, error) {
	if err := checkCaller(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cited := s.v.cited()
	res := &wikiv1.ListIngestQueueResponse{}
	for _, e := range s.v.sorted() {
		if e.desc.Source && !cited[e.path] {
			res.Sources = append(res.Sources, artifactOf(e))
		}
	}
	return res, nil
}

// Search implements if-wiki-api.
func (s *Service) Search(ctx context.Context, req *wikiv1.SearchRequest) (*wikiv1.SearchResponse, error) {
	if err := checkCaller(ctx); err != nil {
		return nil, err
	}
	q := queryWords(req.GetQuery())
	if len(q) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the query has no word")
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := &wikiv1.SearchResponse{}
	for _, h := range s.v.search(q) {
		if len(res.Hits) == limit {
			break
		}
		res.Hits = append(res.Hits, &wikiv1.SearchHit{Artifact: artifactOf(h.e), Score: h.score})
	}
	return res, nil
}
