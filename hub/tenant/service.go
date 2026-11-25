// Package tenant is the building block Tenants (bb-tenants in docs/architecture/05-building-blocks.arc42.md).
//
// It provides if-tenant-api (TenantService): a system admin creates, lists and removes tenants, and
// a tenant admin creates and removes the team agents of the tenant (dec-team-agents); every member
// lists them. Tenants owns the schema of the definition of a team agent (package check, if-tenants-check).
// It does not keep the tenant records or the memberships: Identity owns them, and Tenants asks
// Identity through the tenant roster (if-tenant-roster), and it creates the repository of each
// tenant through the archive (if-archive-storage). So creating a tenant with its first
// tenant admin, and ending a tenant with all its memberships, are one commit in the hub
// repository.
package tenant

import (
	"context"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/roster"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
	"github.com/mrsimpson/agentic-office/hub/tenant/api/tenantv1connect"
)

// Service implements TenantService (if-tenant-api).
type Service struct {
	roster roster.Roster
	repos  storage.Archives

	mu       sync.Mutex
	commands map[string]command
}

type command struct {
	req proto.Message
	res proto.Message
}

var _ tenantv1connect.TenantServiceHandler = (*Service)(nil)

// New returns a Service that works through the tenant roster and creates the repositories of the
// tenants.
func New(r roster.Roster, repos storage.Archives) *Service {
	return &Service{roster: r, repos: repos, commands: map[string]command{}}
}

// systemAdmin lets only a system admin pass.
func systemAdmin(ctx context.Context) error {
	c, _ := tokencheck.CallerFrom(ctx)
	if c.ParticipantID == "" || !c.SystemAdmin {
		return connect.NewError(connect.CodePermissionDenied, "only a system admin may manage tenants")
	}
	return nil
}

// run repeats the stored answer for a repeated command_id and otherwise runs do.
func (s *Service) run(commandID string, req, res proto.Message, do func() error) (proto.Message, error) {
	if commandID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "command_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.commands[commandID]; ok {
		if !proto.Equal(c.req, req) {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another request")
		}
		return c.res, nil
	}
	if err := do(); err != nil {
		return nil, err
	}
	s.commands[commandID] = command{req: req, res: res}
	return res, nil
}

func (s *Service) CreateTenant(ctx context.Context, req *tenantv1.CreateTenantRequest) (*tenantv1.CreateTenantResponse, error) {
	if err := systemAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.run(req.GetCommandId(), req, &tenantv1.CreateTenantResponse{}, func() error {
		// The repository first: creating it twice does no harm, so a failed or repeated create
		// does not leave a tenant without a repository.
		if err := storage.CheckTenantID(req.GetTenantId()); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err.Error())
		}
		if err := s.repos.CreateTenant(ctx, req.GetTenantId()); err != nil {
			return connect.NewError(connect.CodeInternal, err.Error())
		}
		return s.roster.CreateTenant(ctx, req.GetTenantId(), req.GetDisplayName(), req.GetFirstAdminUserId())
	})
	if err != nil {
		return nil, err
	}
	return res.(*tenantv1.CreateTenantResponse), nil
}

func (s *Service) RemoveTenant(ctx context.Context, req *tenantv1.RemoveTenantRequest) (*tenantv1.RemoveTenantResponse, error) {
	if err := systemAdmin(ctx); err != nil {
		return nil, err
	}
	res, err := s.run(req.GetCommandId(), req, &tenantv1.RemoveTenantResponse{}, func() error {
		return s.roster.RemoveTenant(ctx, req.GetTenantId())
	})
	if err != nil {
		return nil, err
	}
	return res.(*tenantv1.RemoveTenantResponse), nil
}

func (s *Service) ListTenants(ctx context.Context, _ *tenantv1.ListTenantsRequest) (*tenantv1.ListTenantsResponse, error) {
	if err := systemAdmin(ctx); err != nil {
		return nil, err
	}
	ts, err := s.roster.Tenants(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	res := &tenantv1.ListTenantsResponse{}
	for _, t := range ts {
		pt := &tenantv1.Tenant{TenantId: t.ID, DisplayName: t.DisplayName, ValidFrom: timestamppb.New(t.ValidFrom)}
		if !t.Active() {
			pt.ValidUntil = timestamppb.New(t.ValidUntil)
		}
		res.Tenants = append(res.Tenants, pt)
	}
	return res, nil
}

// member returns the caller of a call for a tenant. A caller who is no member of the tenant gets
// the answer for a tenant that does not exist, and so does a team agent or a stage link. With
// admin set, only a tenant admin passes.
func member(ctx context.Context, tenantID string, admin bool) error {
	c, _ := tokencheck.CallerFrom(ctx)
	role, ok := c.Member(tenantID)
	if c.ParticipantID == "" || c.TeamTenant != "" || !ok {
		return connect.NewError(connect.CodeNotFound, "no such tenant")
	}
	if admin && role != tokencheck.RoleTenantAdmin {
		return connect.NewError(connect.CodePermissionDenied, "only a tenant admin may manage the team agents")
	}
	return nil
}

func (s *Service) CreateTeamAgent(ctx context.Context, req *tenantv1.CreateTeamAgentRequest) (*tenantv1.CreateTeamAgentResponse, error) {
	if err := member(ctx, req.GetTenantId(), true); err != nil {
		return nil, err
	}
	res, err := s.run(req.GetCommandId(), req, &tenantv1.CreateTeamAgentResponse{}, func() error {
		return s.roster.CreateTeamAgent(ctx, req.GetTenantId(), req.GetAgentId(), req.GetDisplayName())
	})
	if err != nil {
		return nil, err
	}
	return res.(*tenantv1.CreateTeamAgentResponse), nil
}

func (s *Service) RemoveTeamAgent(ctx context.Context, req *tenantv1.RemoveTeamAgentRequest) (*tenantv1.RemoveTeamAgentResponse, error) {
	if err := member(ctx, req.GetTenantId(), true); err != nil {
		return nil, err
	}
	res, err := s.run(req.GetCommandId(), req, &tenantv1.RemoveTeamAgentResponse{}, func() error {
		return s.roster.RemoveTeamAgent(ctx, req.GetTenantId(), req.GetAgentId())
	})
	if err != nil {
		return nil, err
	}
	return res.(*tenantv1.RemoveTeamAgentResponse), nil
}

func (s *Service) ListTeamAgents(ctx context.Context, req *tenantv1.ListTeamAgentsRequest) (*tenantv1.ListTeamAgentsResponse, error) {
	if err := member(ctx, req.GetTenantId(), false); err != nil {
		return nil, err
	}
	as, err := s.roster.TeamAgents(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	res := &tenantv1.ListTeamAgentsResponse{}
	for _, a := range as {
		pa := &tenantv1.TeamAgent{AgentId: a.ID, DisplayName: a.DisplayName, ValidFrom: timestamppb.New(a.ValidFrom)}
		if !a.Active() {
			pa.ValidUntil = timestamppb.New(a.ValidUntil)
		}
		res.Agents = append(res.Agents, pa)
	}
	return res, nil
}
