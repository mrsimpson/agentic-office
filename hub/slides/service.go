package slides

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
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

// Service implements the slides interface (if-slides-api): SlidesService for clients and Load
// for other building blocks of the hub, for example the stage.
type Service struct {
	arch storage.Archive

	mu sync.Mutex // one write at a time
	// rmu guards all and byCmd. A writer holds mu and takes rmu only to add a deck, so reads do
	// not wait for a commit.
	rmu   sync.RWMutex
	all   []*slidesv1.Deck // index n-1 holds the deck slides/n
	byCmd map[string]*slidesv1.Deck
}

var _ slidesv1connect.SlidesServiceHandler = (*Service)(nil)

// Open reads all decks from the archive and returns the service that stores decks there.
func Open(ctx context.Context, arch storage.Archive) (*Service, error) {
	s := &Service{arch: arch, byCmd: map[string]*slidesv1.Deck{}}
	files, err := arch.Read(ctx, Kind)
	if err != nil {
		return nil, fmt.Errorf("slides: read %s: %w", Kind, err)
	}
	byNum := map[uint64]*slidesv1.Deck{}
	for _, f := range files {
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		// The check of the part: the same one that decides about a push.
		if reasons := check.Validate(check.SchemaDeck, f.Path, f.Data); len(reasons) > 0 {
			return nil, fmt.Errorf("slides: %s: %s", f.Path, strings.Join(reasons, "; "))
		}
		d, err := decode(f.Data)
		if err != nil {
			return nil, fmt.Errorf("slides: %s: %w", f.Path, err)
		}
		n, _ := check.Number(d.GetId())
		switch {
		case byNum[n] != nil:
			return nil, fmt.Errorf("slides: %s: deck %q is twice", f.Path, d.GetId())
		case s.byCmd[d.GetCommandId()] != nil:
			return nil, fmt.Errorf("slides: %s: command ID %q is used twice", f.Path, d.GetCommandId())
		}
		// The output is not stored: render it again from the stored input.
		content, err := Render(d.GetTitle(), d.GetSlides())
		if err != nil {
			return nil, fmt.Errorf("slides: %s: render: %w", f.Path, err)
		}
		d.MediaType, d.Content = MediaType, content
		byNum[n] = d
		s.byCmd[d.GetCommandId()] = d
	}
	// The numbers have no gaps: a deck is never deleted.
	for n := uint64(1); n <= uint64(len(byNum)); n++ {
		if byNum[n] == nil {
			return nil, fmt.Errorf("slides: %s is missing", check.FilePath(n, ".yaml"))
		}
		s.all = append(s.all, byNum[n])
	}
	return s, nil
}

// Count returns the number of decks.
func (s *Service) Count() int {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	return len(s.all)
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
	if prev := s.byCmd[req.GetCommandId()]; prev != nil {
		if prev.GetTitle() != req.GetTitle() || prev.GetAuthorParticipantId() != req.GetAuthorParticipantId() ||
			!sameSlides(prev.GetSlides(), req.GetSlides()) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another deck")
		}
		return &slidesv1.RenderDeckResponse{Deck: prev, Duplicate: true}, nil
	}
	content, err := Render(req.GetTitle(), req.GetSlides())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "render the deck: "+err.Error())
	}
	if len(content) > MaxContentBytes {
		return nil, invalid("the deck is too large")
	}
	n := uint64(len(s.all)) + 1
	d := &slidesv1.Deck{Id: check.DeckID(n), Title: req.GetTitle(), AuthorParticipantId: pid, AuthorKind: kind,
		CreateTime: timestamppb.Now(), CommandId: req.GetCommandId(), MediaType: MediaType, Content: content}
	for _, sl := range req.GetSlides() {
		d.Slides = append(d.Slides, proto.Clone(sl).(*slidesv1.Slide))
	}
	if err := s.write(ctx, n, d); err != nil {
		return nil, err
	}
	s.rmu.Lock()
	s.all = append(s.all, d)
	s.byCmd[d.GetCommandId()] = d
	s.rmu.Unlock()
	return &slidesv1.RenderDeckResponse{Deck: d}, nil
}

// write stores the input of deck n in one commit. The rendered output is not stored.
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
		Files:   []storage.File{{Path: check.FilePath(n, ".yaml"), Data: data}},
	}); err != nil {
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
	if !ok || n > uint64(len(s.all)) {
		return nil, connect.NewError(connect.CodeNotFound, "no deck "+id)
	}
	return s.all[n-1], nil
}
