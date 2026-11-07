package journal

import (
	"context"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	journalv1 "github.com/mrsimpson/agentic-office/hub/journal/api"
)

// TokenCheck returns the Authorizer that reads the caller from the identity token check
// (if-token-check). The journal owns its own author kind, so it maps the kind of the actor here.
func TokenCheck() Authorizer {
	return AuthorizerFunc(func(ctx context.Context) (Caller, error) {
		c, ok := tokencheck.CallerFrom(ctx)
		if !ok {
			return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no access token")
		}
		return Caller{ParticipantID: c.ParticipantID, Kind: authorKind(c.Kind)}, nil
	})
}

func authorKind(k tokencheck.Kind) journalv1.AuthorKind {
	switch k {
	case tokencheck.KindHuman:
		return journalv1.AuthorKind_AUTHOR_KIND_HUMAN
	case tokencheck.KindAgent:
		return journalv1.AuthorKind_AUTHOR_KIND_AGENT
	}
	return journalv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED
}
