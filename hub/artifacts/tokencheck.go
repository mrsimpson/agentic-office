package artifacts

import (
	"context"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

// TokenCheck returns the Authorizer that reads the caller from the identity token check
// (if-token-check).
func TokenCheck() Authorizer {
	return AuthorizerFunc(func(ctx context.Context) (Caller, error) {
		c, ok := tokencheck.CallerFrom(ctx)
		if !ok {
			return Caller{}, connect.NewError(connect.CodeUnauthenticated, "no access token")
		}
		return Caller{ParticipantID: c.ParticipantID}, nil
	})
}
