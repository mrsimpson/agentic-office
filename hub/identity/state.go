package identity

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
)

// The hub repository holds the users and memberships as YAML files (dec-multi-tenancy):
//
//	system/users/<user>.yaml                    one user with its keys
//	system/memberships/<tenant>/<user>.yaml     one membership
const (
	usersDir       = "system/users"
	membershipsDir = "system/memberships"
)

const (
	roleMember      = "member"
	roleTenantAdmin = "tenant_admin"
)

type keyRecord struct {
	KeyID     string `yaml:"key_id"`
	PublicKey string `yaml:"public_key"`
	Revoked   bool   `yaml:"revoked,omitempty"`
}

type userRecord struct {
	DisplayName string       `yaml:"display_name"`
	Kind        string       `yaml:"kind"`
	SystemAdmin bool         `yaml:"system_admin,omitempty"`
	ValidFrom   time.Time    `yaml:"valid_from"`
	ValidUntil  *time.Time   `yaml:"valid_until,omitempty"`
	Keys        []*keyRecord `yaml:"keys"`
}

type period struct {
	Role       string     `yaml:"role"`
	ValidFrom  time.Time  `yaml:"valid_from"`
	ValidUntil *time.Time `yaml:"valid_until,omitempty"`
}

// membershipRecord is the current period of a membership; earlier periods stay in History.
type membershipRecord struct {
	Role       string     `yaml:"role"`
	ValidFrom  time.Time  `yaml:"valid_from"`
	ValidUntil *time.Time `yaml:"valid_until,omitempty"`
	History    []period   `yaml:"history,omitempty"`
}

type memberKey struct{ tenant, user string }

// state is all users and memberships of the hub. It is small, so every change works on a copy
// and checks the rules for the whole state, the same rules as when the hub loads the repository.
type state struct {
	users   map[string]*userRecord
	members map[memberKey]*membershipRecord
}

func newState() *state {
	return &state{users: map[string]*userRecord{}, members: map[memberKey]*membershipRecord{}}
}

func (u *userRecord) active(now time.Time) bool {
	return !now.Before(u.ValidFrom) && (u.ValidUntil == nil || now.Before(*u.ValidUntil))
}

func (m *membershipRecord) active(now time.Time) bool {
	return !now.Before(m.ValidFrom) && (m.ValidUntil == nil || now.Before(*m.ValidUntil))
}

func (u *userRecord) activeKeys() int {
	n := 0
	for _, k := range u.Keys {
		if !k.Revoked {
			n++
		}
	}
	return n
}

func (st *state) clone() *state {
	c := newState()
	for id, u := range st.users {
		cu := *u
		cu.Keys = make([]*keyRecord, len(u.Keys))
		for i, k := range u.Keys {
			ck := *k
			cu.Keys[i] = &ck
		}
		c.users[id] = &cu
	}
	for k, m := range st.members {
		cm := *m
		cm.History = append([]period(nil), m.History...)
		c.members[k] = &cm
	}
	return c
}

func kindName(k identityv1.ActorKind) string {
	switch k {
	case identityv1.ActorKind_ACTOR_KIND_HUMAN:
		return "human"
	case identityv1.ActorKind_ACTOR_KIND_AGENT:
		return "agent"
	}
	return ""
}

func kindOf(name string) identityv1.ActorKind {
	switch name {
	case "human":
		return identityv1.ActorKind_ACTOR_KIND_HUMAN
	case "agent":
		return identityv1.ActorKind_ACTOR_KIND_AGENT
	}
	return identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED
}

// validate checks the rules of the user API for the whole state. A rule that fails is a
// FailedPrecondition for a change and a reason not to start for the loaded repository.
func (st *state) validate(now time.Time) error {
	keyOwner := map[string]string{}
	activeAdmins := 0
	for _, id := range sortedKeys(st.users) {
		u := st.users[id]
		if !participantIDPattern.MatchString(id) {
			return fmt.Errorf("user %q: the ID must be 1 to 64 letters, digits, '.', '_' or '-'", id)
		}
		if kindOf(u.Kind) == identityv1.ActorKind_ACTOR_KIND_UNSPECIFIED {
			return fmt.Errorf("user %s: the kind must be human or agent", id)
		}
		for _, k := range u.Keys {
			pub, err := auth.ParsePublicKey(k.PublicKey)
			if err != nil || auth.KeyID(pub) != k.KeyID {
				return fmt.Errorf("user %s: key %s is broken", id, k.KeyID)
			}
			if other, ok := keyOwner[k.KeyID]; ok {
				return fmt.Errorf("key %s belongs to %s and to %s: a key belongs to one user only", k.KeyID, other, id)
			}
			keyOwner[k.KeyID] = id
		}
		if !u.active(now) {
			if u.SystemAdmin {
				return fmt.Errorf("user %s is removed and still a system admin", id)
			}
			continue
		}
		if u.activeKeys() == 0 {
			return fmt.Errorf("user %s has no key that is not revoked: remove the user instead", id)
		}
		if u.SystemAdmin {
			if kindOf(u.Kind) != identityv1.ActorKind_ACTOR_KIND_HUMAN {
				return fmt.Errorf("system admin %s must be a human", id)
			}
			activeAdmins++
		}
	}
	if activeAdmins == 0 {
		return fmt.Errorf("the hub needs at least one active system admin")
	}
	tenantAdmins, tenantMembers := map[string]int{}, map[string]int{}
	for mk, m := range st.members {
		if !participantIDPattern.MatchString(mk.tenant) {
			return fmt.Errorf("membership of %s: the tenant ID %q is not valid", mk.user, mk.tenant)
		}
		u := st.users[mk.user]
		if u == nil {
			return fmt.Errorf("membership of %s in %s: there is no such user", mk.user, mk.tenant)
		}
		if m.Role != roleMember && m.Role != roleTenantAdmin {
			return fmt.Errorf("membership of %s in %s: the role must be member or tenant_admin", mk.user, mk.tenant)
		}
		if !m.active(now) {
			continue
		}
		if !u.active(now) {
			return fmt.Errorf("user %s is removed and still a member of %s", mk.user, mk.tenant)
		}
		if u.SystemAdmin {
			return fmt.Errorf("system admin %s cannot be a member of tenant %s: a system admin never is a tenant member", mk.user, mk.tenant)
		}
		tenantMembers[mk.tenant]++
		if m.Role == roleTenantAdmin {
			tenantAdmins[mk.tenant]++
		}
	}
	for t := range tenantMembers {
		if tenantAdmins[t] == 0 {
			return fmt.Errorf("tenant %s needs at least one tenant admin", t)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// files returns the YAML files of the state by path.
func (st *state) files() (map[string][]byte, error) {
	out := map[string][]byte{}
	for id, u := range st.users {
		b, err := marshal(u)
		if err != nil {
			return nil, err
		}
		out[usersDir+"/"+id+".yaml"] = b
	}
	for mk, m := range st.members {
		b, err := marshal(m)
		if err != nil {
			return nil, err
		}
		out[membershipsDir+"/"+mk.tenant+"/"+mk.user+".yaml"] = b
	}
	return out, nil
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}

// parseFiles builds the state from the files of the hub repository (the users and the
// memberships directories).
func parseFiles(users, memberships []fileEntry) (*state, error) {
	st := newState()
	for _, f := range users {
		id := strings.TrimSuffix(f.name, ".yaml")
		var u userRecord
		if err := strictUnmarshal(f.data, &u); err != nil {
			return nil, fmt.Errorf("%s: %w", f.path, err)
		}
		st.users[id] = &u
	}
	for _, f := range memberships {
		// membershipsDir/<tenant>/<user>.yaml
		rest := strings.TrimPrefix(f.path, membershipsDir+"/")
		tenant, name, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(name, "/") || !strings.HasSuffix(name, ".yaml") {
			return nil, fmt.Errorf("%s: want %s/<tenant>/<user>.yaml", f.path, membershipsDir)
		}
		var m membershipRecord
		if err := strictUnmarshal(f.data, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", f.path, err)
		}
		st.members[memberKey{tenant, strings.TrimSuffix(name, ".yaml")}] = &m
	}
	return st, nil
}

type fileEntry struct {
	path, name string
	data       []byte
}

// strictUnmarshal fails on a field that it does not know, so a typo in a file is not ignored.
func strictUnmarshal(b []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	return dec.Decode(v)
}

func publicKeyOf(k *keyRecord) ed25519.PublicKey {
	pub, _ := auth.ParsePublicKey(k.PublicKey)
	return pub
}
