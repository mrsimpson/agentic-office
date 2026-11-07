// Package tokencheck is the token check of identity (if-token-check): other building blocks of
// the server learn from it who makes a call. The identity interceptor checks the access token of
// every call and puts the caller into the context of the call.
package tokencheck

import (
	"context"
	"time"
)

// Kind tells whether the caller is a human device or an agent.
type Kind int

const (
	KindUnknown Kind = iota
	KindHuman
	KindAgent
)

// Caller is who makes a call, as the token check learned it from the token.
type Caller struct {
	ParticipantID string
	DisplayName   string
	Kind          Kind
	KeyID         string
	Admin         bool
	// StageSessionID is set for the token of a stage link. Such a token may only read the
	// session stream of this session.
	StageSessionID string
	// Expires is when the token expires.
	Expires time.Time
	// Done is closed when the token expires or its key is revoked.
	Done <-chan struct{}
}

type callerKey struct{}

// WithCaller returns a context that carries the caller. Only the identity interceptor calls it.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the caller of the call, if the token check found one.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}
