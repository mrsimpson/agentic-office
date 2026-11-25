// Package roster is the tenant roster of identity (if-tenant-roster): the in-process interface
// through which Tenants creates and ends tenants and lists them. Identity keeps the tenant
// records in the hub repository, next to the users and memberships (concept-multi-tenancy).
//
// Errors are Connect errors, so a caller can return them to its client unchanged: InvalidArgument
// for a bad field, AlreadyExists for a tenant ID that is taken (also by an ended tenant: a tenant
// ID is never used again), NotFound for an unknown user or tenant, FailedPrecondition for a change
// that breaks a rule of the user API (for example, the first admin is a system admin). A team agent
// ID that is taken, by a user or by an ended team agent, is AlreadyExists.
package roster

import (
	"context"
	"time"
)

// Tenant is the record of a tenant.
type Tenant struct {
	ID          string
	DisplayName string
	ValidFrom   time.Time
	// ValidUntil is the zero time while the tenant is active.
	ValidUntil time.Time
}

// Active reports whether the tenant has not ended.
func (t Tenant) Active() bool { return t.ValidUntil.IsZero() }

// TeamAgent is the record of a team agent (dec-team-agents): an agent of a tenant without a key.
type TeamAgent struct {
	ID          string
	DisplayName string
	TenantID    string
	ValidFrom   time.Time
	// ValidUntil is the zero time while the team agent is active.
	ValidUntil time.Time
}

// Active reports whether the team agent has not ended.
func (a TeamAgent) Active() bool { return a.ValidUntil.IsZero() }

// Roster is the tenant roster.
type Roster interface {
	// CreateTenant creates a tenant and makes firstAdmin its tenant admin in one commit.
	CreateTenant(ctx context.Context, id, displayName, firstAdmin string) error
	// RemoveTenant ends the tenant and all its memberships in one commit and keeps the records.
	RemoveTenant(ctx context.Context, id string) error
	// Tenants returns all tenants, also the ended ones, ordered by ID.
	Tenants(ctx context.Context) ([]Tenant, error)
	// CreateTeamAgent creates the team agent id in the tenant: an agent without a key that is no
	// member. The ID is a participant ID, so it is unique in the whole hub, and never used again.
	CreateTeamAgent(ctx context.Context, tenantID, id, displayName string) error
	// RemoveTeamAgent ends the team agent of the tenant and keeps its record.
	RemoveTeamAgent(ctx context.Context, tenantID, id string) error
	// TeamAgents returns the team agents of the tenant, also the ended ones, ordered by ID. An
	// unknown tenant is NotFound.
	TeamAgents(ctx context.Context, tenantID string) ([]TeamAgent, error)
}
