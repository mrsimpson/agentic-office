package slides

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
)

const (
	// Kind is the first part of the ID of a deck, for example "slides/42". It is also the
	// directory of the decks in the archive.
	Kind = "slides"
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
	contents := map[string][]byte{}
	for _, f := range files {
		if path.Ext(f.Path) == ".html" {
			contents[strings.TrimSuffix(f.Path, ".html")] = f.Data
		}
	}
	byNum := map[uint64]*slidesv1.Deck{}
	for _, f := range files {
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		d, err := decode(f.Data)
		if err != nil {
			return nil, fmt.Errorf("slides: %s: %w", f.Path, err)
		}
		n, ok := number(d.GetId())
		switch {
		case !ok:
			return nil, fmt.Errorf("slides: %s: %q is not the ID of a deck", f.Path, d.GetId())
		case f.Path != filePath(n, ".yaml"):
			return nil, fmt.Errorf("slides: %s: deck %q belongs in %s", f.Path, d.GetId(), filePath(n, ".yaml"))
		case byNum[n] != nil:
			return nil, fmt.Errorf("slides: %s: deck %q is twice", f.Path, d.GetId())
		case d.GetCommandId() == "":
			return nil, fmt.Errorf("slides: %s: command_id is missing", f.Path)
		case s.byCmd[d.GetCommandId()] != nil:
			return nil, fmt.Errorf("slides: %s: command ID %q is used twice", f.Path, d.GetCommandId())
		}
		content, ok := contents[strings.TrimSuffix(f.Path, ".yaml")]
		if !ok {
			return nil, fmt.Errorf("slides: %s is missing", filePath(n, ".html"))
		}
		d.MediaType, d.Content = MediaType, content
		byNum[n] = d
		s.byCmd[d.GetCommandId()] = d
	}
	// The numbers have no gaps: a deck is never deleted.
	for n := uint64(1); n <= uint64(len(byNum)); n++ {
		if byNum[n] == nil {
			return nil, fmt.Errorf("slides: %s is missing", filePath(n, ".yaml"))
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

// filePath returns the path of a file of deck n in the archive, with the ending ext.
func filePath(n uint64, ext string) string { return fmt.Sprintf("%s/%06d%s", Kind, n, ext) }

func deckID(n uint64) string { return Kind + "/" + strconv.FormatUint(n, 10) }

// number returns the number of a deck ID such as "slides/42".
func number(id string) (uint64, bool) {
	rest, ok := strings.CutPrefix(id, Kind+"/")
	if !ok || rest == "" || rest[0] == '0' || rest[0] == '+' {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
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

// RenderDeck renders a deck and stores its input and output as a new deck "slides/<n>", in one
// commit of the archive. It is idempotent by command_id.
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
	d := &slidesv1.Deck{Id: deckID(n), Title: req.GetTitle(), AuthorParticipantId: pid, AuthorKind: kind,
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

// write stores the input and the output of deck n in one commit.
func (s *Service) write(ctx context.Context, n uint64, d *slidesv1.Deck) error {
	data, err := encode(d)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "encode the deck: "+err.Error())
	}
	title := d.GetTitle()
	if strings.TrimSpace(title) == "" {
		title = d.GetSlides()[0].GetTitle()
	}
	// A cancelled commit could leave the repository locked, so the commit is not cancelled with
	// the call.
	if _, err := s.arch.Commit(context.WithoutCancel(ctx), storage.Change{
		Message: fmt.Sprintf("Slides %s: %s", d.GetId(), summary(title)),
		Files: []storage.File{{Path: filePath(n, ".yaml"), Data: data},
			{Path: filePath(n, ".html"), Data: bytes.Clone(d.GetContent())}},
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
	n, ok := number(id)
	if !ok || n > uint64(len(s.all)) {
		return nil, connect.NewError(connect.CodeNotFound, "no deck "+id)
	}
	return s.all[n-1], nil
}
