package identity

import (
	"context"
	"strings"
	"time"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
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
	return s.mutate(ctx, "System: create tenant "+id, func(st *state, now time.Time) error {
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
	return s.mutate(ctx, "System: remove tenant "+id, func(st *state, now time.Time) error {
		t := st.tenants[id]
		if t == nil {
			return notFound("no such tenant")
		}
		if !t.active(now) {
			return precondition("the tenant was removed already")
		}
		t.ValidUntil = &now
		for _, u := range st.users {
			if u.Team == id && u.active(now) {
				u.ValidUntil = &now
			}
		}
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

// CreateTeamAgent implements roster.Roster.
func (s *Service) CreateTeamAgent(ctx context.Context, tenantID, id, displayName string) error {
	if !participantIDPattern.MatchString(id) {
		return invalid("agent_id must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if strings.TrimSpace(displayName) == "" || strings.ContainsAny(displayName, "<>\n\r") {
		return invalid("display_name is required, in one line and without < or >")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(ctx, "System: create team agent "+id+" in "+tenantID, func(st *state, now time.Time) error {
		if t := st.tenants[tenantID]; t == nil || !t.active(now) {
			return notFound("no such tenant")
		}
		if st.users[id] != nil {
			return alreadyExists("the participant ID " + id + " exists or was removed: an ID is never used again")
		}
		st.users[id] = &userRecord{DisplayName: displayName, Kind: kindName(identityv1.ActorKind_ACTOR_KIND_AGENT), Team: tenantID, ValidFrom: now}
		return nil
	})
}

// RemoveTeamAgent implements roster.Roster.
func (s *Service) RemoveTeamAgent(ctx context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(ctx, "System: remove team agent "+id+" of "+tenantID, func(st *state, now time.Time) error {
		u := st.users[id]
		if u == nil || u.Team != tenantID {
			return notFound("no such team agent")
		}
		if !u.active(now) {
			return precondition("the team agent was removed already")
		}
		u.ValidUntil = &now
		return nil
	})
}

// TeamAgents implements roster.Roster.
func (s *Service) TeamAgents(_ context.Context, tenantID string) ([]roster.TeamAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st.tenants[tenantID] == nil {
		return nil, notFound("no such tenant")
	}
	var out []roster.TeamAgent
	for _, id := range sortedKeys(s.st.users) {
		u := s.st.users[id]
		if u.Team != tenantID {
			continue
		}
		a := roster.TeamAgent{ID: id, DisplayName: u.DisplayName, TenantID: tenantID, ValidFrom: u.ValidFrom}
		if u.ValidUntil != nil {
			a.ValidUntil = *u.ValidUntil
		}
		out = append(out, a)
	}
	return out, nil
}
