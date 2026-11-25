package coreapi

import "strings"

// AgentDefinition is the definition of a team agent (dec-team-agents): the file
// agents/<agent ID>.yaml of the tenant, with the fields of the schema office.agent.v1. It holds
// nothing that depends on one computer: the harness, the working directory and the credentials of
// a model come from the computer of the agent launcher.
type AgentDefinition struct {
	// Agent is the ID of the team agent.
	Agent string
	// Version identifies the content of the file, as the archive read interface names it. The agent
	// launcher confirms a version, and the token as the agent carries it.
	Version string
	// Role tells the agent what it does.
	Role string
	// Allow are the names of the tools that the agent may use besides the tools of the office.
	Allow []string
	// Strategy is the wake strategy: turn-end, llm or decision-model. Empty: turn-end.
	Strategy string
	// Threshold is the probability from which a model's answer counts as yes. Zero: not set.
	Threshold float64
	// Text is the file as it is, to show it to the agent launcher.
	Text string
}

// Summary returns the definition in a form to show to the agent launcher before the first start.
func (d *AgentDefinition) Summary() string {
	var b strings.Builder
	b.WriteString("role:\n")
	for _, l := range strings.Split(strings.TrimRight(d.Role, "\n"), "\n") {
		b.WriteString("  " + l + "\n")
	}
	if len(d.Allow) > 0 {
		b.WriteString("allowed tools besides the office tools: " + strings.Join(d.Allow, ", ") + "\n")
	} else {
		b.WriteString("allowed tools besides the office tools: none\n")
	}
	s := d.Strategy
	if s == "" {
		s = "turn-end"
	}
	b.WriteString("wake strategy: " + s + "\n")
	return b.String()
}
