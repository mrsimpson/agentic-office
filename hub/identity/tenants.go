package identity

import (
	"context"
	"time"

	"github.com/mrsimpson/agentic-office/hub/identity/roster"
)

var _ roster.Roster = (*Service)(nil)

func tenantOf(id string, t *tenantRecord) roster.Tenant {
	out := roster.Tenant{ID: id, DisplayName: t.DisplayName, ValidFrom: t.ValidFrom}
	if t.ValidUntil != nil {
		out.ValidUntil = *t.ValidUntil
	}
	return out
}

// CreateTenant implements roster.Roster.
func (s *Service) CreateTenant(ctx context.Context, id, displayName, firstAdmin string) error {
	if !participantIDPattern.MatchString(id) {
		return invalid("tenant_id must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if displayName == "" {
		return invalid("display_name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(ctx, "Create tenant "+id, func(st *state, now time.Time) error {
		if st.tenants[id] != nil {
			return alreadyExists("tenant " + id + " exists or was removed: a tenant ID is never used again")
		}
		if u := st.users[firstAdmin]; u == nil || !u.active(now) {
			return notFound("the first tenant admin is not an active user")
		}
		st.tenants[id] = &tenantRecord{DisplayName: displayName, ValidFrom: now}
		st.members[memberKey{id, firstAdmin}] = &membershipRecord{Role: roleTenantAdmin, ValidFrom: now}
		return nil
	})
}

// RemoveTenant implements roster.Roster.
func (s *Service) RemoveTenant(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(ctx, "Remove tenant "+id, func(st *state, now time.Time) error {
		t := st.tenants[id]
		if t == nil {
			return notFound("no such tenant")
		}
		if !t.active(now) {
			return precondition("the tenant was removed already")
		}
		t.ValidUntil = &now
		for mk, m := range st.members {
			if mk.tenant == id && m.active(now) {
				m.ValidUntil = &now
			}
		}
		return nil
	})
}

// Tenants implements roster.Roster.
func (s *Service) Tenants(context.Context) ([]roster.Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []roster.Tenant
	for _, id := range sortedKeys(s.st.tenants) {
		out = append(out, tenantOf(id, s.st.tenants[id]))
	}
	return out, nil
}
