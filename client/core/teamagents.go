package core

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	tenantv1 "github.com/mrsimpson/agentic-office/hub/tenant/api"
)

// DefinitionDir and DefinitionSchema are where and how the definition of a team agent is written
// (dec-team-agents). The hub checks the file by the same rules when it is pushed; the client
// reads it again, so it never runs a definition that it does not understand.
const (
	definitionDir    = "agents"
	definitionSchema = "office.agent.v1"
)

// CreateTeamAgent creates a team agent in the tenant of the client. Only a tenant admin may.
func (c *hubClient) CreateTeamAgent(ctx context.Context, agentID, displayName string) error {
	req := &tenantv1.CreateTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: c.cfg.Tenant, AgentId: agentID, DisplayName: displayName}
	return retry(ctx, func() error {
		_, err := c.tenants.CreateTeamAgent(ctx, req)
		return err
	})
}

// RemoveTeamAgent ends a team agent of the tenant of the client. Only a tenant admin may.
func (c *hubClient) RemoveTeamAgent(ctx context.Context, agentID string) error {
	req := &tenantv1.RemoveTeamAgentRequest{CommandId: coreapi.NewCommandID(), TenantId: c.cfg.Tenant, AgentId: agentID}
	return retry(ctx, func() error {
		_, err := c.tenants.RemoveTeamAgent(ctx, req)
		return err
	})
}

// ListTeamAgents returns the team agents of the tenant of the client, also the removed ones.
func (c *hubClient) ListTeamAgents(ctx context.Context) ([]*tenantv1.TeamAgent, error) {
	var res *tenantv1.ListTeamAgentsResponse
	err := retry(ctx, func() (err error) {
		res, err = c.tenants.ListTeamAgents(ctx, &tenantv1.ListTeamAgentsRequest{TenantId: c.cfg.Tenant})
		return err
	})
	return res.GetAgents(), err
}

// definitionFile is the form of the file of a definition.
type definitionFile struct {
	Schema string   `yaml:"schema"`
	Role   string   `yaml:"role"`
	Allow  []string `yaml:"allow"`
	Wake   struct {
		Strategy  string  `yaml:"strategy"`
		Threshold float64 `yaml:"threshold"`
	} `yaml:"wake"`
}

// AgentDefinition reads the definition of a team agent of the tenant of the client.
func (c *hubClient) AgentDefinition(ctx context.Context, agentID string) (*coreapi.AgentDefinition, error) {
	f, err := c.ReadFile(ctx, definitionDir+"/"+agentID+".yaml")
	if err != nil {
		return nil, fmt.Errorf("the definition of the team agent %s: %w", agentID, err)
	}
	var v definitionFile
	dec := yaml.NewDecoder(bytes.NewReader(f.GetContent()))
	dec.KnownFields(true)
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("the definition of the team agent %s is not valid: %w", agentID, err)
	}
	if v.Schema != definitionSchema || strings.TrimSpace(v.Role) == "" {
		return nil, fmt.Errorf("the definition of the team agent %s is not valid: it needs schema %s and a role", agentID, definitionSchema)
	}
	return &coreapi.AgentDefinition{Agent: agentID, Version: f.GetVersion(), Role: v.Role, Allow: v.Allow,
		Strategy: v.Wake.Strategy, Threshold: v.Wake.Threshold, Text: string(f.GetContent())}, nil
}
