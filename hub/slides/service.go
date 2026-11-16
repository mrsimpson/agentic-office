package slides

import (
	"context"
	"fmt"
	"log"
	"path"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
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

// Service implements the slides interface (if-slides-api): SlidesService for clients and Load
// for other building blocks of the hub, for example the stage.
type Service struct {
	arch storage.Archive
	logf func(string, ...any)

	watcher watch.Watcher
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu sync.Mutex // one write or reload at a time
	// rmu guards byNum, byCmd and versions. A writer holds mu and takes rmu only to change them,
	// so reads do not wait for a commit.
	rmu      sync.RWMutex
	byNum    map[uint64]*slidesv1.Deck
	byCmd    map[string]*slidesv1.Deck
	versions map[uint64]string // the version of the file that each deck was read from
	// bad and high are guarded by mu.
	bad  map[string]string // path to the version of a file that was ignored and logged
	high uint64            // the highest number of any file ever seen, valid or not
}

var _ slidesv1connect.SlidesServiceHandler = (*Service)(nil)

// Open reads all decks from the archive and returns the service that stores decks there. The
// service follows the archive through w (if-archive-watch) until Close: it reloads the deck
// files that a push changed or added. logf logs the files it ignores; nil means log.Printf.
func Open(ctx context.Context, arch storage.Archive, w watch.Watcher, logf func(string, ...any)) (*Service, error) {
	if logf == nil {
		logf = log.Printf
	}
	// Watch first, then read: a change in between is reported and read again.
	sub, err := w.Watch(Kind)
	if err != nil {
		return nil, fmt.Errorf("slides: watch the archive: %w", err)
	}
	s := &Service{arch: arch, logf: logf, watcher: w, done: make(chan struct{}),
		byNum: map[uint64]*slidesv1.Deck{}, byCmd: map[string]*slidesv1.Deck{}, versions: map[uint64]string{}, bad: map[string]string{}}
	if err := s.loadAll(ctx, true); err != nil {
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

// run reloads the files that the archive reports until Close. A subscription that was dropped
// (the part was too slow) is replaced, and then every file is read again, because changes may
// have been missed.
func (s *Service) run(sub watch.Subscription) {
	defer close(s.done)
	for {
		s.pump(sub)
		sub.Close()
		if s.ctx.Err() != nil {
			return
		}
		s.logf("slides: the archive dropped the watcher (%v); watching again and reading all decks", sub.Err())
		var err error
		if sub, err = s.watcher.Watch(Kind); err != nil {
			s.logf("slides: cannot watch the archive again, pushed files are not seen until the hub restarts: %v", err)
			return
		}
		if err := s.loadAll(s.ctx, false); err != nil {
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

// numberOf returns the number in the name of a deck file such as "slides/000042.yaml".
func numberOf(p string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSuffix(path.Base(p), ".yaml"), 10, 64)
	return n, err == nil
}

// load reads one file into the service. It returns an error and changes nothing if the file is
// not valid. The caller holds s.mu. A file that is not valid still counts for the next number, so
// the part never takes its number.
func (s *Service) load(f storage.File) error {
	if path.Ext(f.Path) != ".yaml" {
		return nil
	}
	if n, ok := numberOf(f.Path); ok {
		s.high = max(s.high, n)
		s.rmu.RLock()
		v, known := s.versions[n]
		s.rmu.RUnlock()
		if known && v == f.Version {
			return nil
		}
	}
	// The check of the part: the same one that decides about a push.
	if reasons := check.Validate(check.SchemaDeck, f.Path, f.Data); len(reasons) > 0 {
		return fmt.Errorf("%s: %s", f.Path, strings.Join(reasons, "; "))
	}
	d, err := decode(f.Data)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	n, _ := check.Number(d.GetId())
	if other := s.byCmd[d.GetCommandId()]; other != nil && other.GetId() != d.GetId() {
		return fmt.Errorf("%s: command ID %q is used by deck %q", f.Path, d.GetCommandId(), other.GetId())
	}
	// The output is not stored: render it again from the stored input.
	content, err := Render(d.GetTitle(), d.GetSlides())
	if err != nil {
		return fmt.Errorf("%s: render: %w", f.Path, err)
	}
	d.MediaType, d.Content = MediaType, content
	s.rmu.Lock()
	s.forgetLocked(n)
	s.byNum[n] = d
	s.byCmd[d.GetCommandId()] = d
	s.versions[n] = f.Version
	s.rmu.Unlock()
	delete(s.bad, f.Path)
	return nil
}

// loadLenient is load for a file that a push brought: a file that is not valid is logged once for
// its version and ignored. The deck that the path had before stays.
func (s *Service) loadLenient(f storage.File) {
	if err := s.load(f); err != nil && s.bad[f.Path] != f.Version {
		s.bad[f.Path] = f.Version
		s.logf("slides: ignore the pushed file %v", err)
	}
}

// forgetLocked removes the deck with number n. The caller holds s.rmu for writing.
func (s *Service) forgetLocked(n uint64) {
	if d := s.byNum[n]; d != nil {
		if s.byCmd[d.GetCommandId()] == d {
			delete(s.byCmd, d.GetCommandId())
		}
		delete(s.byNum, n)
	}
	delete(s.versions, n)
}

// forget removes the deck with number n. The caller holds s.mu.
func (s *Service) forget(n uint64) {
	s.rmu.Lock()
	s.forgetLocked(n)
	s.rmu.Unlock()
	delete(s.bad, check.FilePath(n, ".yaml"))
}

// loadAll reads every file of the directory. With strict, the first file that is not valid fails
// it (the start of the hub). Otherwise such a file is logged and ignored, and a deck whose file is
// gone is removed.
func (s *Service) loadAll(ctx context.Context, strict bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.arch.Read(ctx, Kind)
	if err != nil {
		return fmt.Errorf("slides: read %s: %w", Kind, err)
	}
	// The next number never goes below a number that ever lay in the directory, also not below
	// the number of a file that a push removed.
	names, err := s.arch.Names(ctx, Kind)
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
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		if n, ok := numberOf(f.Path); ok {
			present[n] = true
		}
		if strict {
			if err := s.load(f); err != nil {
				return fmt.Errorf("slides: %w", err)
			}
		} else {
			s.loadLenient(f)
		}
	}
	s.rmu.RLock()
	var gone []uint64
	for n := range s.byNum {
		if !present[n] {
			gone = append(gone, n)
		}
	}
	s.rmu.RUnlock()
	for _, n := range gone {
		s.forget(n)
	}
	return nil
}

// reloadPaths reads the files at the paths again from the current commit: a file that is there is
// loaded, a file that is gone is removed. The caller holds s.mu.
func (s *Service) reloadPaths(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if path.Ext(p) != ".yaml" {
			continue
		}
		files, err := s.arch.Read(ctx, p)
		if err != nil {
			return fmt.Errorf("slides: read %s: %w", p, err)
		}
		if len(files) == 0 {
			if n, ok := numberOf(p); ok {
				s.forget(n)
			}
			continue
		}
		for _, f := range files {
			s.loadLenient(f)
		}
	}
	return nil
}

// reload reads the deck files that the changes name.
func (s *Service) reload(ctx context.Context, changes []watch.Change) error {
	var paths []string
	for _, ch := range changes {
		if strings.HasPrefix(ch.Path, Kind+"/") {
			paths = append(paths, ch.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadPaths(ctx, paths)
}

// Count returns the number of decks.
func (s *Service) Count() int {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	return len(s.byNum)
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

// caller returns the participant of the access token and its author kind. A stage link has no
// participant.
func caller(ctx context.Context) (string, slidesv1.AuthorKind, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return "", 0, connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return "", 0, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the slides")
	}
	switch c.Kind {
	case tokencheck.KindHuman:
		return c.ParticipantID, slidesv1.AuthorKind_AUTHOR_KIND_HUMAN, nil
	case tokencheck.KindAgent:
		return c.ParticipantID, slidesv1.AuthorKind_AUTHOR_KIND_AGENT, nil
	}
	return c.ParticipantID, slidesv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED, nil
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

// RenderDeck renders a deck and stores its input as a new deck "slides/<n>", in one commit of the
// archive. The output stays in memory. It is idempotent by command_id.
func (s *Service) RenderDeck(ctx context.Context, req *slidesv1.RenderDeckRequest) (*slidesv1.RenderDeckResponse, error) {
	switch {
	case req.GetCommandId() == "":
		return nil, invalid("command_id is required")
	case req.GetAuthorParticipantId() == "":
		return nil, invalid("author_participant_id is required")
	case len(req.GetSlides()) == 0:
		return nil, invalid("the deck has no slides")
	}
	for _, sl := range req.GetSlides() {
		if strings.TrimSpace(sl.GetTitle()) == "" {
			return nil, invalid("every slide needs a title")
		}
	}
	pid, kind, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetAuthorParticipantId() != pid {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := Render(req.GetTitle(), req.GetSlides())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "render the deck: "+err.Error())
	}
	if len(content) > MaxContentBytes {
		return nil, invalid("the deck is too large")
	}
	// The file is created with "must not exist yet". If a push put a file with this number there
	// first, the part reads the file, takes the next free number and tries again.
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		s.rmu.RLock()
		prev := s.byCmd[req.GetCommandId()]
		s.rmu.RUnlock()
		if prev != nil {
			if prev.GetTitle() != req.GetTitle() || prev.GetAuthorParticipantId() != req.GetAuthorParticipantId() ||
				!sameSlides(prev.GetSlides(), req.GetSlides()) {
				return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another deck")
			}
			return &slidesv1.RenderDeckResponse{Deck: prev, Duplicate: true}, nil
		}
		n := s.high + 1
		d := &slidesv1.Deck{Id: check.DeckID(n), Title: req.GetTitle(), AuthorParticipantId: pid, AuthorKind: kind,
			CreateTime: timestamppb.Now(), CommandId: req.GetCommandId(), MediaType: MediaType, Content: content}
		for _, sl := range req.GetSlides() {
			d.Slides = append(d.Slides, proto.Clone(sl).(*slidesv1.Slide))
		}
		err := s.write(ctx, n, d)
		if storage.IsConflict(err) {
			// A push came first. Read what is there; its number is taken from now on.
			if err := s.reloadPaths(context.WithoutCancel(ctx), []string{check.FilePath(n, ".yaml")}); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err.Error())
			}
			s.high = max(s.high, n)
			continue
		}
		if err != nil {
			return nil, err
		}
		s.high = n
		s.rmu.Lock()
		s.forgetLocked(n)
		s.byNum[n] = d
		s.byCmd[d.GetCommandId()] = d
		s.rmu.Unlock()
		return &slidesv1.RenderDeckResponse{Deck: d}, nil
	}
	return nil, connect.NewError(connect.CodeInternal, "store the deck in the archive: too many files were pushed meanwhile; repeat the command")
}

// write stores the input of deck n in one commit. The rendered output is not stored. The file
// must not exist yet; a conflict is returned as it is.
func (s *Service) write(ctx context.Context, n uint64, d *slidesv1.Deck) error {
	data, err := encode(d)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "encode the deck: "+err.Error())
	}
	if reasons := check.Validate(check.SchemaDeck, check.FilePath(n, ".yaml"), data); len(reasons) > 0 {
		return connect.NewError(connect.CodeInternal, "the deck is not valid: "+strings.Join(reasons, "; "))
	}
	title := d.GetTitle()
	if strings.TrimSpace(title) == "" {
		title = d.GetSlides()[0].GetTitle()
	}
	// A cancelled commit could leave the repository locked, so the commit is not cancelled with
	// the call.
	if _, err := s.arch.Commit(context.WithoutCancel(ctx), storage.Change{
		Message: fmt.Sprintf("Slides %s: %s", d.GetId(), summary(title)),
		Files:   []storage.File{{Path: check.FilePath(n, ".yaml"), Data: data, Base: storage.NoFile}},
	}); err != nil {
		if storage.IsConflict(err) {
			return err
		}
		return connect.NewError(connect.CodeInternal, "store the deck in the archive: "+err.Error())
	}
	return nil
}

// summary returns the first line of a text, short enough for a commit message.
func summary(text string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if rs := []rune(t); len(rs) > 60 {
		t = string(rs[:60]) + "…"
	}
	return t
}

// GetDeck returns the deck with the ID of the request.
func (s *Service) GetDeck(ctx context.Context, req *slidesv1.GetDeckRequest) (*slidesv1.GetDeckResponse, error) {
	if req.GetId() == "" {
		return nil, invalid("id is required")
	}
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	d, err := s.Load(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &slidesv1.GetDeckResponse{Deck: d}, nil
}

// Load returns the deck with this ID for another building block of the hub, for example the
// stage, which renders the page on the hub. There is no token check: the caller checks its own
// access. An unknown ID fails with NotFound. The caller must not change the deck.
func (s *Service) Load(_ context.Context, id string) (*slidesv1.Deck, error) {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	n, ok := check.Number(id)
	d := s.byNum[n]
	if !ok || d == nil {
		return nil, connect.NewError(connect.CodeNotFound, "no deck "+id)
	}
	return d, nil
}
