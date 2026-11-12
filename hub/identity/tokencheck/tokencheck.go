// Package tokencheck is the token check of identity (if-token-check): other building blocks of
// the server learn from it who makes a call, whether the caller is a system admin and in which
// tenants the caller is a member, with which role. The identity interceptor checks the access token of
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

// Role is the role of a user in one tenant.
type Role int

const (
	RoleMember Role = iota + 1
	RoleTenantAdmin
)

// Membership is an active membership of the caller in one tenant.
type Membership struct {
	TenantID string
	Role     Role
}

// Caller is who makes a call, as the token check learned it from the token.
type Caller struct {
	ParticipantID string
	DisplayName   string
	Kind          Kind
	KeyID         string
	// SystemAdmin is set for a system admin. A system admin has no memberships.
	SystemAdmin bool
	// Tenants are the active memberships of the caller at the time of the call.
	Tenants []Membership
	// TenantID is the tenant of the call, set by the identity interceptor for the calls that
	// belong to a tenant. The caller is an active member of it, with TenantRole. A stage link
	// token has the tenant of its link and no role.
	TenantID   string
	TenantRole Role
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

// Member reports whether the caller is an active member of the tenant, and with which role.
func (c Caller) Member(tenantID string) (Role, bool) {
	for _, m := range c.Tenants {
		if m.TenantID == tenantID {
			return m.Role, true
		}
	}
	return 0, false
}
