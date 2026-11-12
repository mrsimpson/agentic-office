// Package roster is the tenant roster of identity (if-tenant-roster): the in-process interface
// through which Tenants creates and ends tenants and lists them. Identity keeps the tenant
// records in the hub repository, next to the users and memberships (concept-multi-tenancy).
//
// Errors are Connect errors, so a caller can return them to its client unchanged: InvalidArgument
// for a bad field, AlreadyExists for a tenant ID that is taken (also by an ended tenant: a tenant
// ID is never used again), NotFound for an unknown user or tenant, FailedPrecondition for a change
// that breaks a rule of the user API (for example, the first admin is a system admin).
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

// Roster is the tenant roster.
type Roster interface {
	// CreateTenant creates a tenant and makes firstAdmin its tenant admin in one commit.
	CreateTenant(ctx context.Context, id, displayName, firstAdmin string) error
	// RemoveTenant ends the tenant and all its memberships in one commit and keeps the records.
	RemoveTenant(ctx context.Context, id string) error
	// Tenants returns all tenants, also the ended ones, ordered by ID.
	Tenants(ctx context.Context) ([]Tenant, error)
}
