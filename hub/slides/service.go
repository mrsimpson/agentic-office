package slides

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/session/release"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides/check"
)

const (
	// Kind is the first part of the ID of a deck, for example "slides/42". It is also the
	// directory of the decks in the archive.
	Kind = check.Kind
	// MaxContentBytes is the largest rendered deck. A larger deck fails with InvalidArgument.
	MaxContentBytes = 1 << 20
)

// maxCreateAttempts is how often a render takes the next number again because a file of that
// number was already in the archive. Every attempt means that a push put a file there meanwhile.
const maxCreateAttempts = 50

// StubMessage is the commit message of every stub. It says nothing about the deck, and it is the
// message of the stubs of the other parts too.
const StubMessage = "Stub: an artifact waits for the release of its session"

// Options are what the slides part of a tenant works with.
type Options struct {
	// Archive is the artifact repository of the tenant (if-archive-storage). Required.
	Archive storage.Archive
	// Watcher reports the changes of the artifact repository (if-archive-watch). Required.
	Watcher watch.Watcher
	// Staging is the staging repository of the tenant (if-archive-storage). Required.
	Staging storage.Staging
	// StagingWatcher reports the changes of the staging repository (if-archive-watch). Required.
	StagingWatcher watch.Watcher
	// Logf logs the files that the part ignores. Nil means log.Printf.
	Logf func(string, ...any)
}

// released is a deck file of the artifact repository: a deck, or the stub of a staged deck.
type released struct {
	deck    *slidesv1.Deck // nil for a stub
	version string         // "" when the part wrote it and has not read it back
}

// staged is a deck file of the staging repository: a deck that waits for the release of its
// session.
type staged struct {
	deck    *slidesv1.Deck
	session string
	version string
}

// changeCmd is what a change or a removal did, for its command_id.
type changeCmd struct {
	n      uint64
	digest string
}

// Service implements the slides interface (if-slides-api): SlidesService for clients and Load
// for other building blocks of the hub, for example the stage.
type Service struct {
	opts     Options
	sessions release.Checker // set by UseSessions

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu sync.Mutex // one write or reload at a time; guards bad and high
	// rmu guards the maps below. A writer holds mu and takes rmu only to change them, so reads do
	// not wait for a commit. A stored deck is never changed in place.
	rmu     sync.RWMutex
	art     map[uint64]released
	stage   map[uint64]staged
	names   map[uint64]string // the number of a path to the session of the area that names it
	cmds    map[string]uint64 // the command_id of a render to the number of its deck
	changes map[string]changeCmd
	bad     map[string]string // path (with a prefix for the repository) to the version that was ignored and logged
	high    uint64            // the highest number of any file ever seen, valid or not
}

var _ slidesv1connect.SlidesServiceHandler = (*Service)(nil)

// Open reads all decks from the artifact repository and the staging repository and renders them.
// The service follows both repositories (if-archive-watch) until Close: it reloads the deck files
// that a commit or a push changed or added. Call UseSessions before the service takes calls.
func Open(ctx context.Context, opts Options) (*Service, error) {
	if opts.Archive == nil || opts.Watcher == nil || opts.Staging == nil || opts.StagingWatcher == nil {
		return nil, errors.New("slides: the archive, the staging repository and their watchers are required")
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	// Watch first, then read: a change in between is reported and read again.
	sub, err := opts.Watcher.Watch(Kind)
	if err != nil {
		return nil, fmt.Errorf("slides: watch the archive: %w", err)
	}
	stageSub, err := opts.StagingWatcher.Watch(Kind)
	if err != nil {
		sub.Close()
		return nil, fmt.Errorf("slides: watch the staging repository: %w", err)
	}
	s := &Service{opts: opts, done: make(chan struct{}),
		art: map[uint64]released{}, stage: map[uint64]staged{}, names: map[uint64]string{}, cmds: map[string]uint64{},
		changes: map[string]changeCmd{}, bad: map[string]string{}}
	if err := s.loadAll(ctx, true); err != nil {
		sub.Close()
		stageSub.Close()
		return nil, err
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	done := make(chan struct{}, 2)
	go func() {
		s.follow(sub, opts.Watcher, "the archive", s.reload, func() error { return s.loadAll(s.ctx, false) })
		done <- struct{}{}
	}()
	go func() {
		s.follow(stageSub, opts.StagingWatcher, "the staging repository", s.reloadStage, func() error { return s.loadAllStaged(s.ctx) })
		done <- struct{}{}
	}()
	go func() { <-done; <-done; close(s.done) }()
	return s, nil
}

// UseSessions gives the part the release status of the sessions of its tenant
// (if-session-release). Call it before the service takes calls.
func (s *Service) UseSessions(c release.Checker) { s.sessions = c }

// Close stops following the archive. The service still answers calls.
func (s *Service) Close() {
	s.cancel()
	<-s.done
}

// follow reloads the files that a repository reports until Close. A subscription that was
// dropped (the part was too slow) is replaced, and then every file is read again, because changes
// may have been missed.
func (s *Service) follow(sub watch.Subscription, w watch.Watcher, what string,
	reload func(context.Context, []watch.Change) error, readAll func() error) {
	for {
		s.pump(sub, reload)
		sub.Close()
		if s.ctx.Err() != nil {
			return
		}
		s.opts.Logf("slides: %s dropped the watcher (%v); watching again and reading all decks", what, sub.Err())
		var err error
		if sub, err = w.Watch(Kind); err != nil {
			s.opts.Logf("slides: cannot watch %s again, its changes are not seen until the hub restarts: %v", what, err)
			return
		}
		if err := readAll(); err != nil {
			s.opts.Logf("%v", err)
		}
	}
}

func (s *Service) pump(sub watch.Subscription, reload func(context.Context, []watch.Change) error) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case changes, ok := <-sub.Changes():
			if !ok {
				return
			}
			if err := reload(s.ctx, changes); err != nil {
				s.opts.Logf("%v", err)
			}
		}
	}
}

// numberOf returns the number in the name of a deck file such as "slides/000042.yaml", and false
// if the path is not the file of a deck.
func numberOf(p string) (uint64, bool) {
	if path.Dir(p) != Kind || path.Ext(p) != ".yaml" {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(path.Base(p), ".yaml"), 10, 64)
	return n, err == nil
}

// deckAt returns the deck of number n: the released one, else the staged one. The caller holds
// s.rmu.
func (s *Service) deckAt(n uint64) *slidesv1.Deck {
	if a, ok := s.art[n]; ok && a.deck != nil {
		return a.deck
	}
	if st, ok := s.stage[n]; ok {
		return st.deck
	}
	return nil
}

// unindex removes the command_id of a deck that left number n, unless n still holds a deck with
// it. The caller holds s.rmu for writing.
func (s *Service) unindex(n uint64, cid string) {
	if s.cmds[cid] != n {
		return
	}
	if d := s.deckAt(n); d != nil && d.GetCommandId() == cid {
		return
	}
	delete(s.cmds, cid)
}

// setArt sets the file of the artifact repository with number n. The caller holds s.mu.
func (s *Service) setArt(n uint64, f released) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	old, had := s.art[n]
	s.art[n] = f
	if had && old.deck != nil {
		s.unindex(n, old.deck.GetCommandId())
	}
	if f.deck != nil {
		s.cmds[f.deck.GetCommandId()] = n
	}
	delete(s.bad, "art:"+check.FilePath(n, ".yaml"))
}

// dropArt forgets the file of the artifact repository with number n. The caller holds s.mu.
func (s *Service) dropArt(n uint64) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	old, had := s.art[n]
	delete(s.art, n)
	if had && old.deck != nil {
		s.unindex(n, old.deck.GetCommandId())
	}
	delete(s.bad, "art:"+check.FilePath(n, ".yaml"))
}

// setStaged sets the staged file with number n. The caller holds s.mu.
func (s *Service) setStaged(n uint64, f staged) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	old, had := s.stage[n]
	s.stage[n] = f
	s.names[n] = f.session
	if had {
		s.unindex(n, old.deck.GetCommandId())
	}
	s.cmds[f.deck.GetCommandId()] = n
	delete(s.bad, "stage:"+check.FilePath(n, ".yaml"))
}

// dropStaged forgets the staged file with number n. The caller holds s.mu.
func (s *Service) dropStaged(n uint64) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	old, had := s.stage[n]
	delete(s.stage, n)
	if had {
		s.unindex(n, old.deck.GetCommandId())
	}
	delete(s.bad, "stage:"+check.FilePath(n, ".yaml"))
}

// setName records the session of the area that names number n; "" forgets it. The caller holds
// s.mu.
func (s *Service) setName(n uint64, session string) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	if session == "" {
		delete(s.names, n)
	} else {
		s.names[n] = session
	}
}

// parse checks a deck file with the check of the part, reads it and renders the deck again from
// its input. The caller holds s.mu.
func (s *Service) parse(n uint64, f storage.File) (*slidesv1.Deck, error) {
	if reasons := check.Validate(check.SchemaDeck, f.Path, f.Data); len(reasons) > 0 {
		return nil, fmt.Errorf("%s: %s", f.Path, strings.Join(reasons, "; "))
	}
	d, err := decode(f.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	s.rmu.RLock()
	other, dup := s.cmds[d.GetCommandId()]
	s.rmu.RUnlock()
	if dup && other != n {
		return nil, fmt.Errorf("%s: command ID %q is used by deck %q", f.Path, d.GetCommandId(), check.DeckID(other))
	}
	// The output is not stored: render it again from the stored input.
	content, err := Render(d.GetTitle(), d.GetSlides())
	if err != nil {
		return nil, fmt.Errorf("%s: render: %w", f.Path, err)
	}
	d.MediaType, d.Content = MediaType, content
	return d, nil
}

// load reads one file of the artifact repository: a deck or a stub. It returns an error and
// changes nothing if the file is not valid. The caller holds s.mu. A file that is not valid still
// counts for the next number, so the part never takes its number.
func (s *Service) load(f storage.File) error {
	n, ok := numberOf(f.Path)
	if !ok {
		return nil
	}
	s.high = max(s.high, n)
	s.rmu.RLock()
	a, known := s.art[n]
	s.rmu.RUnlock()
	if known && a.version == f.Version {
		return nil
	}
	if check.IsStub(f.Data) {
		s.setArt(n, released{version: f.Version})
		return nil
	}
	d, err := s.parse(n, f)
	if err != nil {
		return err
	}
	s.setArt(n, released{deck: d, version: f.Version})
	return nil
}

// loadStaged reads one staged file of a session. A stub is never staged. The caller holds s.mu.
func (s *Service) loadStaged(session string, f storage.File) error {
	n, ok := numberOf(f.Path)
	if !ok {
		return nil
	}
	s.high = max(s.high, n)
	s.setName(n, session)
	s.rmu.RLock()
	st, known := s.stage[n]
	s.rmu.RUnlock()
	if known && st.version == f.Version && st.session == session {
		return nil
	}
	if check.IsStub(f.Data) {
		return fmt.Errorf("%s: a stub lies in the staging area of session %s", f.Path, session)
	}
	d, err := s.parse(n, f)
	if err != nil {
		return err
	}
	s.setStaged(n, staged{deck: d, session: session, version: f.Version})
	return nil
}

// lenient logs an error of a file once for its version. key starts with "art:" for a file of the
// artifact repository, which a push brought, and with "stage:" for a staged file.
func (s *Service) lenient(key, version string, err error) {
	if err != nil && s.bad[key] != version {
		s.bad[key] = version
		if strings.HasPrefix(key, "art:") {
			s.opts.Logf("slides: ignore the pushed file %v", err)
		} else {
			s.opts.Logf("slides: ignore the staged file %v", err)
		}
	}
}

// loadAll reads every deck file, in the artifact repository and in every staging area. With
// strict, the first file that is not valid fails it (the start of the hub). Otherwise such a file
// is logged and ignored, and a deck whose file is gone is removed. A staged deck without a stub,
// which a crash between the two commits leaves, gets its stub.
func (s *Service) loadAll(ctx context.Context, strict bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadArt(ctx, strict); err != nil {
		return err
	}
	if err := s.loadStage(ctx, strict); err != nil {
		return err
	}
	s.rmu.RLock()
	var missing []uint64
	for n := range s.stage {
		if _, ok := s.art[n]; !ok {
			missing = append(missing, n)
		}
	}
	s.rmu.RUnlock()
	for _, n := range missing {
		if err := s.writeStub(ctx, n); err != nil {
			s.opts.Logf("slides: write the missing stub of %s: %v", check.FilePath(n, ".yaml"), err)
			continue
		}
		s.setArt(n, released{})
	}
	return nil
}

// writeStub writes the stub of deck n into the artifact repository. The file must not exist yet.
func (s *Service) writeStub(ctx context.Context, n uint64) error {
	_, err := s.opts.Archive.Commit(context.WithoutCancel(ctx), storage.Change{Message: StubMessage,
		Files: []storage.File{{Path: check.FilePath(n, ".yaml"), Data: check.Stub(), Base: storage.NoFile}}})
	return err
}

// loadArt reads the directory of the artifact repository. The caller holds s.mu.
func (s *Service) loadArt(ctx context.Context, strict bool) error {
	files, err := s.opts.Archive.Read(ctx, Kind)
	if err != nil {
		return fmt.Errorf("slides: read %s: %w", Kind, err)
	}
	// The next number never goes below a number that ever lay in the directory, also not below
	// the number of a file that a push removed.
	names, err := s.opts.Archive.Names(ctx, Kind)
	if err != nil {
		return fmt.Errorf("slides: list the names in %s: %w", Kind, err)
	}
	for _, p := range names {
		if n, ok := numberOf(p); ok {
			s.high = max(s.high, n)
		}
	}
	present := map[uint64]bool{}
	for _, f := range files {
		n, ok := numberOf(f.Path)
		if !ok {
			continue
		}
		present[n] = true
		err := s.load(f)
		if strict && err != nil {
			return fmt.Errorf("slides: %w", err)
		}
		s.lenient("art:"+f.Path, f.Version, err)
	}
	s.rmu.RLock()
	var gone []uint64
	for n := range s.art {
		if !present[n] {
			gone = append(gone, n)
		}
	}
	s.rmu.RUnlock()
	for _, n := range gone {
		s.dropArt(n)
	}
	return nil
}

// loadStage reads the directory in every staging area. The caller holds s.mu.
func (s *Service) loadStage(ctx context.Context, strict bool) error {
	areas, err := s.opts.Staging.Areas(ctx)
	if err != nil {
		return fmt.Errorf("slides: read the staging areas: %w", err)
	}
	names := map[uint64]string{}
	for _, a := range areas {
		for _, p := range a.Names {
			if n, ok := numberOf(p); ok {
				s.high = max(s.high, n)
				names[n] = a.Session
			}
		}
	}
	s.rmu.Lock()
	s.names = names
	s.rmu.Unlock()
	present := map[uint64]bool{}
	for _, a := range areas {
		files, err := s.opts.Staging.Read(ctx, a.Session, Kind)
		if errors.Is(err, storage.ErrNoArea) {
			continue // released in the meantime
		}
		if err != nil {
			return fmt.Errorf("slides: read %s in the staging area of %s: %w", Kind, a.Session, err)
		}
		for _, f := range files {
			n, ok := numberOf(f.Path)
			if !ok {
				continue
			}
			present[n] = true
			err := s.loadStaged(a.Session, f)
			if strict && err != nil {
				return fmt.Errorf("slides: %w", err)
			}
			s.lenient("stage:"+f.Path, f.Version, err)
		}
	}
	s.rmu.RLock()
	var gone []uint64
	for n := range s.stage {
		if !present[n] {
			gone = append(gone, n)
		}
	}
	s.rmu.RUnlock()
	for _, n := range gone {
		s.dropStaged(n)
	}
	return nil
}

// loadAllStaged reads every staged file again, after the staging watcher was dropped.
func (s *Service) loadAllStaged(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadStage(ctx, false)
}

// reloadArt reads the files at the paths of the artifact repository again from the current
// commit: a file that is there is loaded, a file that is gone is removed. The caller holds s.mu.
func (s *Service) reloadArt(ctx context.Context, paths []string) error {
	for _, p := range paths {
		n, ok := numberOf(p)
		if !ok {
			continue
		}
		files, err := s.opts.Archive.Read(ctx, p)
		if err != nil {
			return fmt.Errorf("slides: read %s: %w", p, err)
		}
		if len(files) == 0 {
			s.dropArt(n)
			continue
		}
		for _, f := range files {
			s.lenient("art:"+f.Path, f.Version, s.load(f))
		}
	}
	return nil
}

// reloadStaged reads the staged files at the paths again. A path that no area names any more was
// released or never staged. The caller holds s.mu.
func (s *Service) reloadStaged(ctx context.Context, paths []string) error {
	refreshed := false
	for _, p := range paths {
		n, ok := numberOf(p)
		if !ok {
			continue
		}
		s.rmu.RLock()
		session, ok := s.names[n]
		s.rmu.RUnlock()
		if !ok && !refreshed {
			// A path of another writer or of an area the part has not seen yet.
			areas, err := s.opts.Staging.Areas(ctx)
			if err != nil {
				return fmt.Errorf("slides: read the staging areas: %w", err)
			}
			refreshed = true
			for _, a := range areas {
				for _, name := range a.Names {
					if m, ok := numberOf(name); ok {
						s.setName(m, a.Session)
					}
				}
			}
			s.rmu.RLock()
			session, ok = s.names[n]
			s.rmu.RUnlock()
		}
		if !ok {
			s.dropStaged(n)
			continue
		}
		files, err := s.opts.Staging.Read(ctx, session, p)
		if errors.Is(err, storage.ErrNoArea) {
			s.setName(n, "")
			s.dropStaged(n)
			continue
		}
		if err != nil {
			return fmt.Errorf("slides: read %s in the staging area of %s: %w", p, session, err)
		}
		if len(files) == 0 {
			s.dropStaged(n)
			continue
		}
		for _, f := range files {
			s.lenient("stage:"+f.Path, f.Version, s.loadStaged(session, f))
		}
	}
	return nil
}

// paths returns the paths of the changes under the directory of the decks.
func paths(changes []watch.Change) []string {
	var out []string
	for _, ch := range changes {
		if strings.HasPrefix(ch.Path, Kind+"/") {
			out = append(out, ch.Path)
		}
	}
	return out
}

// reload is reloadArt for the changes of a push or a commit in the artifact repository.
func (s *Service) reload(ctx context.Context, changes []watch.Change) error {
	ps := paths(changes)
	if len(ps) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadArt(ctx, ps)
}

// reloadStage is reloadStaged for the changes of a commit in the staging repository.
func (s *Service) reloadStage(ctx context.Context, changes []watch.Change) error {
	ps := paths(changes)
	if len(ps) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadStaged(ctx, ps)
}

// Count returns the number of decks, released and staged.
func (s *Service) Count() int {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	n := 0
	for _, a := range s.art {
		if a.deck != nil {
			n++
		}
	}
	for m := range s.stage {
		if a, ok := s.art[m]; !ok || a.deck == nil {
			n++
		}
	}
	return n
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

// caller returns the caller of the access token and its author kind. A stage link has no
// participant.
func caller(ctx context.Context) (tokencheck.Caller, slidesv1.AuthorKind, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return c, 0, connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return c, 0, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the slides")
	}
	switch c.Kind {
	case tokencheck.KindHuman:
		return c, slidesv1.AuthorKind_AUTHOR_KIND_HUMAN, nil
	case tokencheck.KindAgent:
		return c, slidesv1.AuthorKind_AUTHOR_KIND_AGENT, nil
	}
	return c, slidesv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED, nil
}

// author checks a write of a client: the fields and that the author is the caller.
func author(ctx context.Context, commandID, authorID string) (tokencheck.Caller, slidesv1.AuthorKind, error) {
	switch {
	case commandID == "":
		return tokencheck.Caller{}, 0, invalid("command_id is required")
	case authorID == "":
		return tokencheck.Caller{}, 0, invalid("author_participant_id is required")
	}
	c, k, err := caller(ctx)
	if err != nil {
		return c, 0, err
	}
	if authorID != c.ParticipantID {
		return c, 0, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}
	return c, k, nil
}

// checkSlides checks the slides of a render or a change and renders them.
func checkSlides(title string, slides []*slidesv1.Slide) ([]byte, error) {
	if len(slides) == 0 {
		return nil, invalid("the deck has no slides")
	}
	for _, sl := range slides {
		if strings.TrimSpace(sl.GetTitle()) == "" {
			return nil, invalid("every slide needs a title")
		}
	}
	content, err := Render(title, slides)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "render the deck: "+err.Error())
	}
	if len(content) > MaxContentBytes {
		return nil, invalid("the deck is too large")
	}
	return content, nil
}

// storageAuthor is the author of a change in the staging area: the participant, with its display
// name when it has one.
func storageAuthor(c tokencheck.Caller) storage.Author {
	name := c.DisplayName
	if name == "" {
		name = c.ParticipantID
	}
	return storage.Author{Name: name, Email: c.ParticipantID + "@office.invalid"}
}

// errNotParticipant is the answer for a session that the caller may not name. A session that does
// not exist gets the same answer.
func errNotParticipant() error {
	return connect.NewError(connect.CodePermissionDenied, "only a participant of the session may name it")
}

// tookPart asks the meeting session whether the caller took part in a session.
func (s *Service) tookPart(ctx context.Context, sessionID, pid string) error {
	if s.sessions == nil {
		return connect.NewError(connect.CodeInternal, "slides: no release status of the sessions")
	}
	ok, err := s.sessions.TookPart(ctx, sessionID, pid)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if !ok {
		return errNotParticipant()
	}
	return nil
}

// errReleased tells a caller of releasing that the session is released and its area is gone.
var errReleased = errors.New("released")

// releasing returns errReleased when the session is released and its staging area is gone, and an
// error when the session is released but its area still exists: the move of the area runs, so a
// write waits.
func (s *Service) releasing(ctx context.Context, sessionID string) error {
	st, err := s.sessions.State(ctx, sessionID)
	if errors.Is(err, release.ErrNoSession) {
		return errNotParticipant()
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if st.Status != release.Released {
		return nil
	}
	if _, err := s.opts.Staging.Area(ctx, sessionID); err == nil {
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	} else if !errors.Is(err, storage.ErrNoArea) {
		return connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
	}
	return errReleased
}

// target is where a new deck goes: the staging area of session, or the artifact repository when
// session is empty.
type target struct {
	session string
	author  storage.Author
}

// route returns where a new deck that names a session goes (concept-session-release). Only a
// participant may name a session. It never runs while the part holds a lock.
func (s *Service) route(ctx context.Context, sessionID string, c tokencheck.Caller) (target, error) {
	if sessionID == "" {
		return target{}, nil
	}
	if err := s.tookPart(ctx, sessionID, c.ParticipantID); err != nil {
		return target{}, err
	}
	switch err := s.releasing(ctx, sessionID); {
	case errors.Is(err, errReleased):
		return target{}, nil
	case err != nil:
		return target{}, err
	}
	return target{session: sessionID, author: storageAuthor(c)}, nil
}

// reader tells whether the caller may read the staged decks of a session.
type reader func(session string) bool

// readerOf returns whether the caller may read the staged decks of a session: a tenant admin, by
// role, or a reader of the staging area of the session. It asks each area once per call.
func (s *Service) readerOf(ctx context.Context, c tokencheck.Caller) reader {
	if c.TenantRole == tokencheck.RoleTenantAdmin {
		return func(string) bool { return true }
	}
	known := map[string]bool{}
	return func(session string) bool {
		if v, ok := known[session]; ok {
			return v
		}
		a, err := s.opts.Staging.Area(ctx, session)
		if err != nil && !errors.Is(err, storage.ErrNoArea) {
			s.opts.Logf("slides: read the readers of session %q: %v", session, err)
		}
		v := err == nil && slices.Contains(a.Readers, c.ParticipantID)
		known[session] = v
		return v
	}
}

// shown returns a deck as a client sees it: a staged deck has staged set.
func shown(d *slidesv1.Deck, isStaged bool) *slidesv1.Deck {
	if !isStaged {
		return d
	}
	c := proto.Clone(d).(*slidesv1.Deck)
	c.Staged = true
	return c
}

// byCommand returns the deck of a render and whether it is staged. The caller holds s.rmu.
func (s *Service) byCommand(cid string) (*slidesv1.Deck, bool, bool) {
	n, ok := s.cmds[cid]
	if !ok {
		return nil, false, false
	}
	if a, ok := s.art[n]; ok && a.deck != nil {
		return a.deck, false, true
	}
	if st, ok := s.stage[n]; ok {
		return st.deck, true, true
	}
	return nil, false, false
}

func (s *Service) found(cid string) (*slidesv1.Deck, bool, bool) {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	return s.byCommand(cid)
}

// sameSlides reports whether two lists of slides are equal.
func sameSlides(a, b []*slidesv1.Slide) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !proto.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// RenderDeck renders a deck and stores its input as a new deck "slides/<n>", in one commit: in the
// staging area of the session that it names while the session is not released, else in the
// artifact repository. The output stays in memory. It is idempotent by command_id.
func (s *Service) RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error) {
	who, kind, err := author(ctx, req.GetCommandId(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	content, err := checkSlides(req.GetTitle(), req.GetSlides())
	if err != nil {
		return nil, err
	}
	same := func(prev *slidesv1.Deck) bool {
		return prev.GetTitle() == req.GetTitle() && prev.GetAuthorParticipantId() == req.GetAuthorParticipantId() &&
			prev.GetSessionId() == req.GetSessionId() && sameSlides(prev.GetSlides(), req.GetSlides())
	}
	if prev, isStaged, ok := s.found(req.GetCommandId()); ok {
		if !same(prev) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another deck")
		}
		return &slidesv1.RenderDeckResponse{Deck: shown(prev, isStaged), Duplicate: true}, nil
	}
	// Ask the meeting session before the lock: it never waits for this part.
	to, err := s.route(ctx, req.GetSessionId(), who)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// A cancelled commit could leave the repository locked, so the commits are not cancelled with
	// the call.
	ctx = context.WithoutCancel(ctx)
	// The file is created with "must not exist yet". If a push put a file with this number there
	// first, the part reads the file, takes the next free number and tries again.
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		if prev, isStaged, ok := s.found(req.GetCommandId()); ok {
			if !same(prev) {
				return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another deck")
			}
			return &slidesv1.RenderDeckResponse{Deck: shown(prev, isStaged), Duplicate: true}, nil
		}
		n := s.high + 1
		d := &slidesv1.Deck{Id: check.DeckID(n), Title: req.GetTitle(), AuthorParticipantId: who.ParticipantID, AuthorKind: kind,
			CreateTime: timestamppb.Now(), CommandId: req.GetCommandId(), SessionId: req.GetSessionId(), MediaType: MediaType, Content: content}
		for _, sl := range req.GetSlides() {
			d.Slides = append(d.Slides, proto.Clone(sl).(*slidesv1.Slide))
		}
		retry, err := s.add(ctx, n, d, to)
		if retry {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &slidesv1.RenderDeckResponse{Deck: shown(d, to.session != "")}, nil
	}
	return nil, connect.NewError(connect.CodeInternal, "store the deck in the archive: too many files were pushed meanwhile; repeat the command")
}

// add stores the new deck n at its target. It returns true when the number was taken meanwhile and
// the caller takes the next one. A deck for a staging area goes into the area first, so that the
// area knows the name, then its stub into the artifact repository. The caller holds s.mu.
func (s *Service) add(ctx context.Context, n uint64, d *slidesv1.Deck, to target) (bool, error) {
	p := check.FilePath(n, ".yaml")
	data, err := s.encode(p, d)
	if err != nil {
		return false, err
	}
	file := []storage.File{{Path: p, Data: data, Base: storage.NoFile}}
	message := fmt.Sprintf("Slides %s: %s", d.GetId(), summary(deckTitle(d)))
	taken := func() (bool, error) {
		// A push came first. Read what is there; its number is taken from now on.
		if err := s.reloadArt(ctx, []string{p}); err != nil {
			return false, connect.NewError(connect.CodeInternal, err.Error())
		}
		s.high = max(s.high, n)
		return true, nil
	}
	if to.session == "" {
		_, err = s.opts.Archive.Commit(ctx, storage.Change{Message: message, Files: file})
		if storage.IsConflict(err) {
			return taken()
		}
		if err != nil {
			return false, connect.NewError(connect.CodeInternal, "store the deck in the archive: "+err.Error())
		}
		s.high = n
		s.setArt(n, released{deck: d})
		return false, nil
	}
	_, err = s.opts.Staging.Commit(ctx, to.session, storage.Change{Message: message, Author: to.author, Files: file})
	if storage.IsConflict(err) {
		return taken()
	}
	if err != nil {
		return false, storeErr(err)
	}
	s.high = n
	s.setName(n, to.session)
	if err := s.writeStub(ctx, n); err != nil {
		// Take the staged file back. A push that put a file at this path first keeps it.
		if _, rerr := s.opts.Staging.Commit(ctx, to.session, storage.Change{Message: fmt.Sprintf("Slides %s: taken back", d.GetId()),
			Files: []storage.File{{Path: p, Remove: true}}}); rerr != nil {
			s.opts.Logf("slides: take back the staged %s: %v", p, rerr)
		}
		if storage.IsConflict(err) {
			return taken()
		}
		return false, connect.NewError(connect.CodeInternal, "store the stub of the deck in the archive: "+err.Error())
	}
	s.setStaged(n, staged{deck: d, session: to.session})
	s.setArt(n, released{})
	return false, nil
}

// encode returns the checked file of a deck.
func (s *Service) encode(p string, d *slidesv1.Deck) ([]byte, error) {
	data, err := encode(d)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "encode the deck: "+err.Error())
	}
	if reasons := check.Validate(check.SchemaDeck, p, data); len(reasons) > 0 {
		return nil, connect.NewError(connect.CodeInternal, "the deck is not valid: "+strings.Join(reasons, "; "))
	}
	return data, nil
}

// storeErr maps an error of a write to the staging area.
func storeErr(err error) error {
	switch {
	case errors.Is(err, storage.ErrNoArea):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the release status of the session and its staging area disagree: the session has no staging area")
	case errors.Is(err, storage.ErrAreaClosed):
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	}
	return connect.NewError(connect.CodeInternal, "store the deck in the archive: "+err.Error())
}

func deckTitle(d *slidesv1.Deck) string {
	if strings.TrimSpace(d.GetTitle()) != "" {
		return d.GetTitle()
	}
	return d.GetSlides()[0].GetTitle()
}

// summary returns the first line of a text, short enough for a commit message.
func summary(text string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if rs := []rune(t); len(rs) > 60 {
		t = string(rs[:60]) + "…"
	}
	return t
}

// entryState is what a deck number is, as the clients see it before the reader check.
type entryState int

const (
	entryNone     entryState = iota
	entryReleased            // a deck in the artifact repository
	entryStaged              // a staged deck; readers of its session see it
	entryRemoved             // a staged deck that was removed; its stub stays until the release
	entryStub                // a stub whose staged deck the part does not know
)

// locate returns what deck n is, its deck and the session of its area.
func (s *Service) locate(n uint64) (entryState, *slidesv1.Deck, string) {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	a, inArt := s.art[n]
	if inArt && a.deck != nil {
		return entryReleased, a.deck, ""
	}
	if st, ok := s.stage[n]; ok {
		return entryStaged, st.deck, st.session
	}
	if sid := s.names[n]; sid != "" && inArt {
		return entryRemoved, nil, sid
	}
	if inArt {
		return entryStub, nil, ""
	}
	return entryNone, nil, ""
}

// view returns deck id as a caller with the reader check may sees it.
func (s *Service) view(id string, may reader) (*slidesv1.Deck, error) {
	n, ok := check.Number(id)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, "no deck "+id)
	}
	state, d, session := s.locate(n)
	switch state {
	case entryReleased:
		return d, nil
	case entryStaged:
		if may(session) {
			return shown(d, true), nil
		}
	case entryRemoved:
		if may(session) {
			return nil, connect.NewError(connect.CodeNotFound, "the deck "+id+" was removed")
		}
	case entryNone:
		return nil, connect.NewError(connect.CodeNotFound, "no deck "+id)
	}
	return &slidesv1.Deck{Id: check.DeckID(n), Stub: true}, nil
}

// GetDeck returns the deck with the ID of the request, as the caller may see it.
func (s *Service) GetDeck(ctx context.Context, req *slidesv1.GetDeckRequest) (*slidesv1.GetDeckResponse, error) {
	if req.GetId() == "" {
		return nil, invalid("id is required")
	}
	who, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.view(req.GetId(), s.readerOf(ctx, who))
	if err != nil {
		return nil, err
	}
	return &slidesv1.GetDeckResponse{Deck: d}, nil
}

// Load returns the deck with this ID for the stage of a session, which renders the page on the
// hub: a staged deck of that session with its content, a staged deck of another session as its
// stub. There is no token check: the caller checks its own access. An unknown ID fails with
// NotFound. The caller must not change the deck.
func (s *Service) Load(_ context.Context, session, id string) (*slidesv1.Deck, error) {
	return s.view(id, func(sid string) bool { return session != "" && sid == session })
}

// digest is the content of a change or a removal, for its idempotency.
func digest(parts ...any) string { return fmt.Sprintf("%q", parts) }

// ChangeDeck replaces the title and the slides of a staged deck for a participant of its session.
func (s *Service) ChangeDeck(ctx context.Context, req *slidesv1.ChangeDeckRequest) (*slidesv1.ChangeDeckResponse, error) {
	who, kind, err := author(ctx, req.GetCommandId(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	content, err := checkSlides(req.GetTitle(), req.GetSlides())
	if err != nil {
		return nil, err
	}
	var slides []string
	for _, sl := range req.GetSlides() {
		b, _ := proto.Marshal(sl)
		slides = append(slides, string(b))
	}
	d, dup, err := s.edit(ctx, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("change", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId(), req.GetTitle(), slides),
		func(d *slidesv1.Deck) *slidesv1.Deck {
			d.Title, d.Slides, d.AuthorParticipantId, d.AuthorKind, d.CreateTime = req.GetTitle(), nil, who.ParticipantID, kind, timestamppb.Now()
			for _, sl := range req.GetSlides() {
				d.Slides = append(d.Slides, proto.Clone(sl).(*slidesv1.Slide))
			}
			d.MediaType, d.Content = MediaType, content
			return d
		})
	if err != nil {
		return nil, err
	}
	return &slidesv1.ChangeDeckResponse{Deck: d, Duplicate: dup}, nil
}

// RemoveDeck removes a staged deck for a participant of its session.
func (s *Service) RemoveDeck(ctx context.Context, req *slidesv1.RemoveDeckRequest) (*slidesv1.RemoveDeckResponse, error) {
	who, _, err := author(ctx, req.GetCommandId(), req.GetAuthorParticipantId())
	if err != nil {
		return nil, err
	}
	_, dup, err := s.edit(ctx, who, req.GetId(), req.GetSessionId(), req.GetCommandId(),
		digest("remove", req.GetId(), req.GetSessionId(), req.GetAuthorParticipantId()), nil)
	if err != nil {
		return nil, err
	}
	return &slidesv1.RemoveDeckResponse{Duplicate: dup}, nil
}

// changed returns the result of an earlier change or removal of the command, and false if there
// was none. A command_id that was used with other content is AlreadyExists.
func (s *Service) changed(cid, dg string) (*slidesv1.Deck, bool, error) {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	prev, ok := s.changes[cid]
	if !ok {
		return nil, false, nil
	}
	if prev.digest != dg {
		return nil, false, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another change")
	}
	if a, ok := s.art[prev.n]; ok && a.deck != nil {
		return a.deck, true, nil
	}
	if st, ok := s.stage[prev.n]; ok {
		return shown(st.deck, true), true, nil
	}
	return nil, true, nil
}

// edit changes a staged deck, or removes it when mutate is nil, for a participant of its session.
// It returns the stored result of a repeated command and true.
func (s *Service) edit(ctx context.Context, who tokencheck.Caller, id, sessionID, cid, dg string,
	mutate func(*slidesv1.Deck) *slidesv1.Deck) (*slidesv1.Deck, bool, error) {
	if prev, ok, err := s.changed(cid, dg); err != nil || ok {
		return prev, ok, err
	}
	if id == "" {
		return nil, false, invalid("id is required")
	}
	n, ok := check.Number(id)
	if !ok {
		return nil, false, connect.NewError(connect.CodeNotFound, "there is no deck "+id)
	}
	state, _, session := s.locate(n)
	switch state {
	case entryNone:
		return nil, false, connect.NewError(connect.CodeNotFound, "there is no deck "+id)
	case entryReleased:
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("the deck %s is released; this interface changes only a staged deck, so render a new one", id))
	case entryStub:
		return nil, false, connect.NewError(connect.CodeFailedPrecondition, fmt.Sprintf("the deck %s is a stub, and no interface changes a stub", id))
	}
	if err := s.tookPart(ctx, session, who.ParticipantID); err != nil {
		return nil, false, connect.NewError(connect.CodePermissionDenied,
			fmt.Sprintf("only a participant of the session of the deck %s may change or remove it", id))
	}
	if sessionID != session {
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("the deck %s waits for the release of session %s; a change or a removal must name that session", id, session))
	}
	if state == entryRemoved {
		if mutate == nil {
			return nil, true, nil
		}
		return nil, false, connect.NewError(connect.CodeNotFound, "the deck "+id+" was removed")
	}
	switch err := s.releasing(ctx, session); {
	case errors.Is(err, errReleased):
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("the deck %s is released; this interface changes only a staged deck, so render a new one", id))
	case err != nil:
		return nil, false, err
	}
	d, err := s.change(ctx, n, session, cid, dg, storageAuthor(who), mutate)
	return d, false, err
}

// change writes a new version of a staged deck of a session, or removes it when mutate is nil. The
// caller has checked the session; change checks again that the deck is staged there.
func (s *Service) change(ctx context.Context, n uint64, session, cid, dg string, author storage.Author,
	mutate func(*slidesv1.Deck) *slidesv1.Deck) (*slidesv1.Deck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	p := check.FilePath(n, ".yaml")
	s.rmu.RLock()
	st, ok := s.stage[n]
	s.rmu.RUnlock()
	if !ok || st.session != session {
		return nil, connect.NewError(connect.CodeAborted, "the deck changed meanwhile; repeat the command")
	}
	base := st.version
	if base == "" {
		files, err := s.opts.Staging.Read(ctx, session, p)
		if err != nil {
			return nil, storeErr(err)
		}
		if len(files) != 1 {
			return nil, connect.NewError(connect.CodeAborted, "the deck changed meanwhile; repeat the command")
		}
		base = files[0].Version
	}
	file := storage.File{Path: p, Base: base}
	var d *slidesv1.Deck
	message := fmt.Sprintf("Slides %s removed", st.deck.GetId())
	if mutate == nil {
		file.Remove = true
	} else {
		d = mutate(proto.Clone(st.deck).(*slidesv1.Deck))
		data, err := s.encode(p, d)
		if err != nil {
			return nil, err
		}
		file.Data = data
		message = fmt.Sprintf("Slides %s changed", st.deck.GetId())
	}
	_, err := s.opts.Staging.Commit(ctx, session, storage.Change{Message: message, Author: author, Files: []storage.File{file}})
	if storage.IsConflict(err) {
		s.lenient("stage:"+p, "", s.reloadStaged(ctx, []string{p}))
		return nil, connect.NewError(connect.CodeAborted, "the deck changed meanwhile; repeat the command")
	}
	if err != nil {
		return nil, storeErr(err)
	}
	s.rmu.Lock()
	s.changes[cid] = changeCmd{n: n, digest: dg}
	s.rmu.Unlock()
	if mutate == nil {
		s.dropStaged(n)
		return nil, nil
	}
	s.setStaged(n, staged{deck: d, session: session})
	return shown(d, true), nil
}
