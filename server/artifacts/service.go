package artifacts

import (
	"context"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/api/office/v1/officev1connect"
)

const (
	// KindSlides is the first part of the ID of a slide deck, for example "slides/42".
	KindSlides = "slides"
	// MaxContentBytes is the largest rendered artifact. A larger deck fails with InvalidArgument.
	MaxContentBytes = 1 << 20
)

// Caller is who makes a call, as the token check (if-token-check) learned it from the token.
type Caller struct {
	// ParticipantID is empty for the token of a stage link.
	ParticipantID string
}

// Authorizer returns the caller of a call. It fails with Unauthenticated if the call carries no
// valid token. In office-server it reads the result of the identity token check.
type Authorizer interface {
	Caller(ctx context.Context) (Caller, error)
}

// AuthorizerFunc is a function that is an Authorizer.
type AuthorizerFunc func(ctx context.Context) (Caller, error)

func (f AuthorizerFunc) Caller(ctx context.Context) (Caller, error) { return f(ctx) }

// Service implements the artifact API (if-artifact-api): ArtifactService for clients and Load for
// other building blocks inside the server, for example the stage.
type Service struct {
	store Store
	auth  Authorizer
	mu    sync.Mutex // serialises writes
}

var _ officev1connect.ArtifactServiceHandler = (*Service)(nil)

// New returns a Service that keeps its artifacts in store. If auth is nil, every call of a
// client fails with Unauthenticated.
func New(store Store, auth Authorizer) *Service {
	if auth == nil {
		auth = AuthorizerFunc(func(context.Context) (Caller, error) {
			return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no token check configured")
		})
	}
	return &Service{store: store, auth: auth}
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

// RenderSlides renders a deck to HTML and stores it as a new artifact "slides/<n>". It is
// idempotent by command_id.
func (s *Service) RenderSlides(ctx context.Context, req *officev1.RenderSlidesRequest) (*officev1.RenderSlidesResponse, error) {
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
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetAuthorParticipantId() != c.ParticipantID {
		return nil, connect.NewError(connect.CodePermissionDenied,
			"author_participant_id must be the participant of the access token")
	}
	input := proto.Clone(req).(*officev1.RenderSlidesRequest)
	input.CommandId = ""

	s.mu.Lock()
	defer s.mu.Unlock()
	prev, err := s.store.ByCommand(req.GetCommandId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read artifacts: "+err.Error())
	}
	if prev != nil {
		if !proto.Equal(prev.Input, input) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another artifact")
		}
		return &officev1.RenderSlidesResponse{Artifact: prev.Artifact, Duplicate: true}, nil
	}
	content, err := RenderSlides(req.GetTitle(), req.GetSlides())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "render slides: "+err.Error())
	}
	if len(content) > MaxContentBytes {
		return nil, invalid("the deck is too large")
	}
	rec, err := s.store.Add(KindSlides, &Record{CommandID: req.GetCommandId(), Input: input, Artifact: &officev1.Artifact{
		MediaType: SlidesMediaType, Content: content, AuthorParticipantId: c.ParticipantID, CreatedAt: timestamppb.Now(),
	}})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "store artifact: "+err.Error())
	}
	return &officev1.RenderSlidesResponse{Artifact: rec.Artifact}, nil
}

// GetArtifact returns the artifact with the ID of the request.
func (s *Service) GetArtifact(ctx context.Context, req *officev1.GetArtifactRequest) (*officev1.GetArtifactResponse, error) {
	if req.GetId() == "" {
		return nil, invalid("id is required")
	}
	if _, err := s.caller(ctx); err != nil {
		return nil, err
	}
	a, err := s.Load(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &officev1.GetArtifactResponse{Artifact: a}, nil
}

// Load returns the artifact with this ID for another building block of the server, for example
// the stage, which renders artifacts on the server. There is no token check: the caller checks
// its own access. An unknown ID fails with NotFound.
func (s *Service) Load(_ context.Context, id string) (*officev1.Artifact, error) {
	rec, err := s.store.Get(id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read artifacts: "+err.Error())
	}
	if rec == nil {
		return nil, connect.NewError(connect.CodeNotFound, "no artifact "+id)
	}
	return rec.Artifact, nil
}

// caller returns the participant of the access token. A stage link has no participant.
func (s *Service) caller(ctx context.Context) (Caller, error) {
	c, err := s.auth.Caller(ctx)
	if err != nil {
		return Caller{}, err
	}
	if c.ParticipantID == "" {
		return Caller{}, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the artifact API")
	}
	return c, nil
}
