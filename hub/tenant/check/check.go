// Package check is the interface if-tenants-check of the building block Tenants
// (docs/architecture/05-building-blocks.arc42.md): it checks the definition of a team agent
// against its schema (dec-team-agents, dec-artifact-sync).
//
// A definition is a YAML file at agents/<agent ID>.yaml in the repository of the tenant. It is
// configuration of the tenant, not an artifact: it is never staged, never described for the wiki
// and never changed by an agent. The same check serves two callers: the artifacts part asks it
// before the hub accepts a push, and the client reads the definition by the same rules.
//
// The file has these fields. Any other field is invalid, so a definition cannot carry a command,
// a working directory, an address or a credential, which stay on the computer of the agent
// launcher:
//
//	schema:  office.agent.v1
//	role:    the text that tells the agent what it does (required, at most MaxRole characters)
//	allow:   a list of names of the tools that the agent may use besides the tools of the
//	         office; each one line, not blank, no spaces around it, not repeated
//	wake:    how the agent is woken
//	  strategy:  turn-end, llm or decision-model (default turn-end)
//	  threshold: a number from 0 to 1 for the strategies that ask a model
//
// The signatures use only types that Go has built in.
package check

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Schema is the name of the schema of a definition. It names the type and its version.
const Schema = "office.agent.v1"

// Dir is the directory of the definitions in the repository of a tenant.
const Dir = "agents"

// MaxRole is the longest role, in characters.
const MaxRole = 20000

// The fields of a definition.
const (
	FieldSchema    = "schema"
	FieldRole      = "role"
	FieldAllow     = "allow"
	FieldWake      = "wake"
	FieldStrategy  = "strategy"
	FieldThreshold = "threshold"
)

// agentID is the form of an agent ID, which is a participant ID.
var agentID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string { return []string{Schema} }

// Strategies returns the values of the field strategy.
func Strategies() []string { return []string{"turn-end", "llm", "decision-model"} }

// FilePath returns the path of the definition of the agent.
func FilePath(agent string) string { return Dir + "/" + agent + ".yaml" }

// AgentOf returns the agent ID that a path of a definition names, and false if the path is not
// the path of a definition.
func AgentOf(filePath string) (string, bool) {
	rest, ok := strings.CutPrefix(path.Clean(filePath), Dir+"/")
	if !ok || strings.Contains(rest, "/") {
		return "", false
	}
	id, ok := strings.CutSuffix(rest, ".yaml")
	return id, ok && agentID.MatchString(id)
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid.
func Validate(schema, filePath string, content []byte) []string {
	if schema != Schema {
		return []string{fmt.Sprintf("the schema %q is not a schema of the tenants", schema)}
	}
	if _, ok := AgentOf(filePath); !ok {
		return []string{fmt.Sprintf("a file of the schema %s is at %s/<agent ID>.yaml; the agent ID has 1 to 64 letters, digits, '.', '_' or '-'", schema, Dir)}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return []string{"the file is not valid YAML: " + firstLine(err)}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return []string{"the file must be a list of fields, one per line"}
	}
	var reasons []string
	seen := map[string]bool{}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i].Value, m.Content[i+1]
		if seen[key] {
			reasons = append(reasons, fmt.Sprintf("the field %s is given twice", key))
			continue
		}
		seen[key] = true
		switch key {
		case FieldSchema:
			if val.Kind != yaml.ScalarNode || val.Value != Schema {
				reasons = append(reasons, fmt.Sprintf("the field %s must be %q", FieldSchema, Schema))
			}
		case FieldRole:
			reasons = append(reasons, checkRole(val)...)
		case FieldAllow:
			reasons = append(reasons, checkAllow(val)...)
		case FieldWake:
			reasons = append(reasons, checkWake(val)...)
		default:
			reasons = append(reasons, fmt.Sprintf("the field %q is not a field of the schema %s (the fields are %s, %s, %s, %s)",
				key, Schema, FieldSchema, FieldRole, FieldAllow, FieldWake))
		}
	}
	if !seen[FieldRole] {
		reasons = append(reasons, "the field "+FieldRole+" is missing")
	}
	return reasons
}

func checkRole(n *yaml.Node) []string {
	if n.Kind != yaml.ScalarNode || n.Tag != "!!str" {
		return []string{"the field " + FieldRole + " must be a text"}
	}
	if strings.TrimSpace(n.Value) == "" {
		return []string{"the field " + FieldRole + " must not be blank"}
	}
	if len([]rune(n.Value)) > MaxRole {
		return []string{fmt.Sprintf("the field %s has more than %d characters", FieldRole, MaxRole)}
	}
	return nil
}

func checkAllow(n *yaml.Node) []string {
	if n.Kind != yaml.SequenceNode {
		return []string{"the field " + FieldAllow + " must be a list of tool names"}
	}
	var reasons []string
	seen := map[string]bool{}
	for _, it := range n.Content {
		switch {
		case it.Kind != yaml.ScalarNode || it.Tag != "!!str":
			reasons = append(reasons, "every entry of "+FieldAllow+" must be a tool name")
		case it.Value == "" || it.Value != strings.TrimSpace(it.Value) || strings.ContainsAny(it.Value, "\n\r"):
			reasons = append(reasons, fmt.Sprintf("the entry %q of %s must be one line, not blank, without spaces around it", it.Value, FieldAllow))
		case seen[it.Value]:
			reasons = append(reasons, fmt.Sprintf("the entry %q of %s is given twice", it.Value, FieldAllow))
		default:
			seen[it.Value] = true
		}
	}
	return reasons
}

func checkWake(n *yaml.Node) []string {
	if n.Kind != yaml.MappingNode {
		return []string{"the field " + FieldWake + " must be a list of fields, one per line"}
	}
	var reasons []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i].Value, n.Content[i+1]
		switch key {
		case FieldStrategy:
			ok := false
			for _, s := range Strategies() {
				ok = ok || (val.Kind == yaml.ScalarNode && val.Value == s)
			}
			if !ok {
				reasons = append(reasons, fmt.Sprintf("%s.%s must be one of %s", FieldWake, FieldStrategy, strings.Join(Strategies(), ", ")))
			}
		case FieldThreshold:
			var f float64
			if err := val.Decode(&f); err != nil || math.IsNaN(f) || f < 0 || f > 1 {
				reasons = append(reasons, fmt.Sprintf("%s.%s must be a number from 0 to 1", FieldWake, FieldThreshold))
			}
		default:
			reasons = append(reasons, fmt.Sprintf("the field %q is not a field of %s (the fields are %s, %s)", key, FieldWake, FieldStrategy, FieldThreshold))
		}
	}
	return reasons
}

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
