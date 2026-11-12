package identity

import (
	"context"
	"crypto/ed25519"
	"sort"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	userv1 "github.com/mrsimpson/agentic-office/hub/identity/userapi"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
)

var _ userv1connect.UserServiceHandler = (*Service)(nil)

func notFound(msg string) error      { return connect.NewError(connect.CodeNotFound, msg) }
func alreadyExists(msg string) error { return connect.NewError(connect.CodeAlreadyExists, msg) }

// who returns the caller of a user API call. A stage link token is no participant.
func who(ctx context.Context) (Caller, error) {
	c, _ := CallerFrom(ctx)
	if c.ParticipantID == "" {
		return c, denied("only a participant may call the user API")
	}
	return c, nil
}

func systemAdmin(ctx context.Context) (Caller, error) {
	c, err := who(ctx)
	if err != nil {
		return c, err
	}
	if !c.SystemAdmin {
		return c, denied("only a system admin may do this")
	}
	return c, nil
}

// tenantManager lets a system admin or the tenant admin of the tenant pass.
func tenantManager(ctx context.Context, tenant string) (Caller, error) {
	c, err := who(ctx)
	if err != nil {
		return c, err
	}
	if c.SystemAdmin {
		return c, nil
	}
	if role, ok := c.Member(tenant); ok && role == tokencheck.RoleTenantAdmin {
		return c, nil
	}
	return c, denied("only a system admin or a tenant admin of the tenant may do this")
}

// change runs a command that changes the state. It repeats the stored answer for a repeated
// command_id, applies apply to a copy of the state, checks the rules and writes the commit. The
// answer is stored with the command.
func (s *Service) change(ctx context.Context, c Caller, commandID string, req proto.Message, res proto.Message,
	message string, apply func(st *state, now time.Time) error) (proto.Message, error) {
	if commandID == "" {
		return nil, invalid("command_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, err := s.repeat(commandID, c.ParticipantID, req); err != nil {
		return nil, err
	} else if prev != nil {
		return prev, nil
	}
	if err := s.mutate(ctx, message, apply); err != nil {
		return nil, err
	}
	s.commands[commandID] = command{caller: c.ParticipantID, req: req, res: res}
	return res, nil
}

// mutate applies apply to a copy of the state, checks the rules and writes the commit. The caller
// holds s.mu.
func (s *Service) mutate(ctx context.Context, message string, apply func(st *state, now time.Time) error) error {
	now := time.Now().UTC().Truncate(time.Second)
	next := s.st.clone()
	if err := apply(next, now); err != nil {
		return err
	}
	if err := next.validate(now); err != nil {
		return precondition(err.Error())
	}
	if err := s.commit(ctx, next, message); err != nil {
		return connect.NewError(connect.CodeInternal, err.Error())
	}
	s.dropUnusable(now)
	return nil
}

// dropUnusable ends the tokens and challenges of keys that cannot log in any more. The caller
// holds s.mu.
func (s *Service) dropUnusable(now time.Time) {
	for h, t := range s.tokens {
		if t.stageSession != "" {
			continue
		}
		if _, _, ok := s.usable(t.keyID, now); !ok {
			t.end()
			delete(s.tokens, h)
		}
	}
	for ch, v := range s.challenges {
		if _, _, ok := s.usable(v.keyID, now); !ok {
			delete(s.challenges, ch)
		}
	}
}

func validPublicKey(b []byte) error {
	if len(b) != ed25519.PublicKeySize {
		return invalid("public_key must be a raw Ed25519 public key of 32 bytes")
	}
	return nil
}

func newKeyRecord(pub []byte) *keyRecord {
	return &keyRecord{KeyID: auth.KeyID(pub), PublicKey: auth.FormatPublicKey(pub)}
}

func (s *Service) CreateUser(ctx context.Context, req *userv1.CreateUserRequest) (*userv1.CreateUserResponse, error) {
	c, err := systemAdmin(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case !participantIDPattern.MatchString(req.GetUserId()):
		return nil, invalid("user_id must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	case req.GetDisplayName() == "" || kindName(req.GetKind()) == "":
		return nil, invalid("display_name and kind (human or agent) are required")
	}
	if err := validPublicKey(req.GetPublicKey()); err != nil {
		return nil, err
	}
	res := &userv1.CreateUserResponse{KeyId: auth.KeyID(req.GetPublicKey())}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: create user "+req.GetUserId(), func(st *state, now time.Time) error {
		if st.users[req.GetUserId()] != nil {
			return alreadyExists("user " + req.GetUserId() + " exists or was removed: a user ID is never used again")
		}
		st.users[req.GetUserId()] = &userRecord{DisplayName: req.GetDisplayName(), Kind: kindName(req.GetKind()),
			ValidFrom: now, Keys: []*keyRecord{newKeyRecord(req.GetPublicKey())}}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.CreateUserResponse), nil
}

func (s *Service) RemoveUser(ctx context.Context, req *userv1.RemoveUserRequest) (*userv1.RemoveUserResponse, error) {
	c, err := systemAdmin(ctx)
	if err != nil {
		return nil, err
	}
	res := &userv1.RemoveUserResponse{}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: remove user "+req.GetUserId(), func(st *state, now time.Time) error {
		u := st.users[req.GetUserId()]
		if u == nil {
			return notFound("no such user")
		}
		if !u.active(now) {
			return precondition("the user was removed already")
		}
		u.ValidUntil, u.SystemAdmin = &now, false
		for mk, m := range st.members {
			if mk.user == req.GetUserId() && m.active(now) {
				m.ValidUntil = &now
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.RemoveUserResponse), nil
}

func (s *Service) ListUsers(ctx context.Context, _ *userv1.ListUsersRequest) (*userv1.ListUsersResponse, error) {
	if _, err := systemAdmin(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := &userv1.ListUsersResponse{}
	for _, id := range sortedKeys(s.st.users) {
		u := s.st.users[id]
		pu := &userv1.User{UserId: id, DisplayName: u.DisplayName, Kind: kindOf(u.Kind), SystemAdmin: u.SystemAdmin,
			ValidFrom: timestamppb.New(u.ValidFrom)}
		if u.ValidUntil != nil {
			pu.ValidUntil = timestamppb.New(*u.ValidUntil)
		}
		for _, k := range u.Keys {
			pu.Keys = append(pu.Keys, &userv1.Key{KeyId: k.KeyID, Revoked: k.Revoked})
		}
		res.Users = append(res.Users, pu)
	}
	return res, nil
}

func (s *Service) SetSystemAdmin(ctx context.Context, req *userv1.SetSystemAdminRequest) (*userv1.SetSystemAdminResponse, error) {
	c, err := systemAdmin(ctx)
	if err != nil {
		return nil, err
	}
	res := &userv1.SetSystemAdminResponse{}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: set system admin of "+req.GetUserId(), func(st *state, now time.Time) error {
		u := st.users[req.GetUserId()]
		if u == nil || !u.active(now) {
			return notFound("no such active user")
		}
		u.SystemAdmin = req.GetSystemAdmin()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.SetSystemAdminResponse), nil
}

func (s *Service) AddKey(ctx context.Context, req *userv1.AddKeyRequest) (*userv1.AddKeyResponse, error) {
	c, err := who(ctx)
	if err != nil {
		return nil, err
	}
	if !c.SystemAdmin && c.ParticipantID != req.GetUserId() {
		return nil, denied("a user adds keys for itself; a system admin adds keys for anyone")
	}
	if err := validPublicKey(req.GetPublicKey()); err != nil {
		return nil, err
	}
	res := &userv1.AddKeyResponse{KeyId: auth.KeyID(req.GetPublicKey())}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: add a key to "+req.GetUserId(), func(st *state, now time.Time) error {
		u := st.users[req.GetUserId()]
		if u == nil || !u.active(now) {
			return notFound("no such active user")
		}
		for _, other := range st.users {
			for _, k := range other.Keys {
				if k.KeyID == res.KeyId {
					switch {
					case other != u:
						return alreadyExists("the key belongs to another user")
					case k.Revoked:
						return precondition("the key was revoked and cannot be added again; create a new key")
					}
					return nil
				}
			}
		}
		u.Keys = append(u.Keys, newKeyRecord(req.GetPublicKey()))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.AddKeyResponse), nil
}

func (s *Service) RevokeKey(ctx context.Context, req *userv1.RevokeKeyRequest) (*userv1.RevokeKeyResponse, error) {
	c, err := who(ctx)
	if err != nil {
		return nil, err
	}
	res := &userv1.RevokeKeyResponse{}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: revoke key "+req.GetKeyId(), func(st *state, now time.Time) error {
		for id, u := range st.users {
			for _, k := range u.Keys {
				if k.KeyID != req.GetKeyId() {
					continue
				}
				if !c.SystemAdmin && c.ParticipantID != id {
					return denied("a user revokes its own keys; a system admin revokes any key")
				}
				k.Revoked = true
				return nil
			}
		}
		return notFound("key_id is not a registered key")
	})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.RevokeKeyResponse), nil
}

func roleName(r userv1.Role) string {
	switch r {
	case userv1.Role_ROLE_MEMBER:
		return roleMember
	case userv1.Role_ROLE_TENANT_ADMIN:
		return roleTenantAdmin
	}
	return ""
}

func roleOf(name string) userv1.Role {
	switch name {
	case roleMember:
		return userv1.Role_ROLE_MEMBER
	case roleTenantAdmin:
		return userv1.Role_ROLE_TENANT_ADMIN
	}
	return userv1.Role_ROLE_UNSPECIFIED
}

func (s *Service) SetMembership(ctx context.Context, req *userv1.SetMembershipRequest) (*userv1.SetMembershipResponse, error) {
	if !participantIDPattern.MatchString(req.GetTenantId()) {
		return nil, invalid("tenant_id must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
	}
	if roleName(req.GetRole()) == "" {
		return nil, invalid("role must be member or tenant admin")
	}
	c, err := tenantManager(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	res := &userv1.SetMembershipResponse{}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: set the membership of "+req.GetUserId()+" in "+req.GetTenantId(),
		func(st *state, now time.Time) error {
			if t := st.tenants[req.GetTenantId()]; t == nil || !t.active(now) {
				return notFound("no such tenant")
			}
			u := st.users[req.GetUserId()]
			if u == nil || !u.active(now) {
				return notFound("no such active user")
			}
			mk := memberKey{req.GetTenantId(), req.GetUserId()}
			role := roleName(req.GetRole())
			switch m := st.members[mk]; {
			case m == nil:
				st.members[mk] = &membershipRecord{Role: role, ValidFrom: now}
			case m.active(now):
				m.Role = role
			default:
				// A returning member: the ended period goes to the history.
				m.History = append(m.History, period{Role: m.Role, ValidFrom: m.ValidFrom, ValidUntil: m.ValidUntil})
				m.Role, m.ValidFrom, m.ValidUntil = role, now, nil
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.SetMembershipResponse), nil
}

func (s *Service) RemoveMembership(ctx context.Context, req *userv1.RemoveMembershipRequest) (*userv1.RemoveMembershipResponse, error) {
	c, err := tenantManager(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	res := &userv1.RemoveMembershipResponse{}
	out, err := s.change(ctx, c, req.GetCommandId(), req, res, "System: end the membership of "+req.GetUserId()+" in "+req.GetTenantId(),
		func(st *state, now time.Time) error {
			m := st.members[memberKey{req.GetTenantId(), req.GetUserId()}]
			if m == nil || !m.active(now) {
				return notFound("no such active membership")
			}
			m.ValidUntil = &now
			return nil
		})
	if err != nil {
		return nil, err
	}
	return out.(*userv1.RemoveMembershipResponse), nil
}

func (s *Service) ListMemberships(ctx context.Context, req *userv1.ListMembershipsRequest) (*userv1.ListMembershipsResponse, error) {
	c, err := who(ctx)
	if err != nil {
		return nil, err
	}
	if _, member := c.Member(req.GetTenantId()); !c.SystemAdmin && !member {
		// The same answer as for a tenant that does not exist.
		return nil, notFound("no such tenant")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := &userv1.ListMembershipsResponse{}
	for mk, m := range s.st.members {
		if mk.tenant != req.GetTenantId() {
			continue
		}
		pm := &userv1.Membership{TenantId: mk.tenant, UserId: mk.user, Role: roleOf(m.Role), ValidFrom: timestamppb.New(m.ValidFrom)}
		if m.ValidUntil != nil {
			pm.ValidUntil = timestamppb.New(*m.ValidUntil)
		}
		res.Memberships = append(res.Memberships, pm)
	}
	sort.Slice(res.Memberships, func(i, j int) bool { return res.Memberships[i].UserId < res.Memberships[j].UserId })
	return res, nil
}
