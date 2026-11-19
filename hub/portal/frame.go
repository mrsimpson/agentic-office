package portal

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/mrsimpson/agentic-office/hub/portal/web"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
)

// user is the user of a session, as the auth API knows it (WhoAmI).
type user struct {
	ParticipantID string
	Name          string
	SystemAdmin   bool
	Memberships   []*identityv1.TenantMembership
}

func userOf(w *identityv1.WhoAmIResponse) *user {
	name := w.GetDisplayName()
	if name == "" {
		name = w.GetParticipantId()
	}
	return &user{ParticipantID: w.GetParticipantId(), Name: name, SystemAdmin: w.GetSystemAdmin(), Memberships: w.GetMemberships()}
}

// membership returns the active membership of the user in a tenant.
func (u *user) membership(tenant string) (*identityv1.TenantMembership, bool) {
	for _, m := range u.Memberships {
		if m.GetTenantId() == tenant {
			return m, true
		}
	}
	return nil, false
}

func roleName(m *identityv1.TenantMembership) string {
	if m.GetTenantAdmin() {
		return "tenant admin"
	}
	return "member"
}

// navLink and navGroup are the navigation of the frame.
type navLink struct {
	Href, Label string
	On          bool
}

type navGroup struct {
	Group string
	Links []navLink
}

// nav returns the navigation for the user: its tenants, the pages of the open tenant, and the
// administration for a system admin. It shows only what the key may see (dec-web-portal).
func (u *user) nav(active, tenant string) []navGroup {
	link := func(href, label string) navLink { return navLink{Href: href, Label: label, On: href == active} }
	var out []navGroup
	if len(u.Memberships) > 0 {
		g := navGroup{Group: "Tenants"}
		for _, m := range u.Memberships {
			g.Links = append(g.Links, link(web.TenantPath(m.GetTenantId()), m.GetTenantId()))
		}
		out = append(out, g)
	}
	if m, ok := u.membership(tenant); ok {
		g := navGroup{Group: tenant, Links: []navLink{link(web.TenantPath(tenant), "Overview")}}
		if m.GetTenantAdmin() {
			g.Links = append(g.Links, link(web.MembersPath(tenant), "Members"))
		}
		out = append(out, g)
	}
	if u.SystemAdmin {
		out = append(out, navGroup{Group: "Administration", Links: []navLink{link(web.KeysPath, "Users and keys"),
			link(web.TenantsPath, "Tenants"), link(web.AdminsPath, "System admins")}})
	}
	return out
}

// tenantRow is a tenant of the user on the start page.
type tenantRow struct {
	TenantId, Role, Href string
}

// home is the start page: the tenants of the user. A user with one tenant and no administration
// goes straight to it.
func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	if len(c.me.Memberships) == 1 && !c.me.SystemAdmin {
		http.Redirect(w, r, web.TenantPath(c.me.Memberships[0].GetTenantId()), http.StatusSeeOther)
		return
	}
	var rows []tenantRow
	for _, m := range c.me.Memberships {
		rows = append(rows, tenantRow{TenantId: m.GetTenantId(), Role: roleName(m), Href: web.TenantPath(m.GetTenantId())})
	}
	h.page(w, c.me, web.HomePath, "", map[string]any{"Title": "Your tenants", "Section": "Portal", "Mode": "home",
		"Tenants": rows, "SystemAdmin": c.me.SystemAdmin})
}

// memberRow is an active member of a tenant.
type memberRow struct {
	UserId, Role, Since string
}

// tenant serves the pages of one tenant. Only a member sees them; for everybody else the tenant
// does not exist, as in the interfaces.
func (h *Handler) tenant(w http.ResponseWriter, r *http.Request, escaped, page string) {
	c, ok := h.client(w, r)
	if !ok {
		return
	}
	tenant, err := url.PathUnescape(escaped)
	m, member := c.me.membership(tenant)
	if err != nil || !member {
		h.fail(w, http.StatusNotFound, "Not found", "There is no such tenant for you.")
		return
	}
	switch page {
	case "":
		h.page(w, c.me, web.TenantPath(tenant), tenant, map[string]any{"Title": tenant, "Section": "Tenant", "Mode": "tenant",
			"Tenant": tenant, "Role": roleName(m)})
	case "members":
		if !m.GetTenantAdmin() {
			h.fail(w, http.StatusForbidden, "Not allowed", "Only a tenant admin sees the members of the tenant.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		res, err := c.users.ListMemberships(ctx, &userv1.ListMembershipsRequest{TenantId: tenant})
		if err != nil {
			h.denied(w, err)
			return
		}
		var rows []memberRow
		now := time.Now()
		for _, ms := range res.GetMemberships() {
			if ms.GetValidUntil() != nil && !now.Before(ms.GetValidUntil().AsTime()) {
				continue
			}
			role := "member"
			if ms.GetRole() == userv1.Role_ROLE_TENANT_ADMIN {
				role = "tenant admin"
			}
			rows = append(rows, memberRow{UserId: ms.GetUserId(), Role: role, Since: ms.GetValidFrom().AsTime().UTC().Format("2006-01-02")})
		}
		h.page(w, c.me, web.MembersPath(tenant), tenant, map[string]any{"Title": "Members", "Section": tenant, "Mode": "members",
			"Members": rows})
	default:
		h.fail(w, http.StatusNotFound, "Not found", "There is no such page.")
	}
}
