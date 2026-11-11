package session

import (
	"context"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

// TokenCheck returns the Authorizer that reads the caller from the identity token check
// (if-token-check). The session owns its own participant kind, so it maps the kind of the actor
// here.
func TokenCheck() Authorizer {
	return AuthorizerFunc(func(ctx context.Context) (Caller, error) {
		c, ok := tokencheck.CallerFrom(ctx)
		if !ok {
			return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no access token")
		}
		return Caller{ParticipantID: c.ParticipantID, DisplayName: c.DisplayName, Kind: participantKind(c.Kind),
			StageSessionID: c.StageSessionID, Done: c.Done}, nil
	})
}

func participantKind(k tokencheck.Kind) sessionstreamv1.ParticipantKind {
	switch k {
	case tokencheck.KindHuman:
		return sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN
	case tokencheck.KindAgent:
		return sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT
	}
	return sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_UNSPECIFIED
}
