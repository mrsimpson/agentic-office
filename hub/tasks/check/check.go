// Package check is the interface if-tasks-check of the building block Tasks
// (docs/architecture/05-building-blocks.arc42.md): it checks one file of a task against the schema
// that the file names (dec-artifact-sync).
//
// The same check serves two callers, so every rule exists once. The artifacts part asks it before
// the hub accepts a push. The tasks part asks it when it writes a task and when it reads the
// tasks at the start.
//
// The signatures use only types that Go has built in. Every stored task is a YAML file and names
// its schema in the field "schema".
package check

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"

	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
)

// SchemaTask is the schema of a task. The name is the contract of the type and its version.
const SchemaTask = "office.task.v1"

const (
	// SchemaKey is the field of a stored task that names its schema.
	SchemaKey = "schema"
	// CommandsKey is the field of a stored task that holds the command IDs of the task. It is
	// not part of the contract.
	CommandsKey = "command_ids"
	// Dir is the directory of the tasks in the archive.
	Dir = "tasks"
	// LocalScheme is the start of the ref of a local draft.
	LocalScheme = "local:"
)

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string { return []string{SchemaTask} }

// FilePath returns the path of the file of the local task n.
func FilePath(n uint64) string { return fmt.Sprintf("%s/%06d.yaml", Dir, n) }

// LocalRef returns the ref of the local task n.
func LocalRef(n uint64) string { return LocalScheme + strconv.FormatUint(n, 10) }

// LocalNumber returns the number of a local ref such as "local:42".
func LocalNumber(ref string) (uint64, bool) {
	rest, ok := strings.CutPrefix(ref, LocalScheme)
	if !ok || rest == "" || rest[0] == '0' || rest[0] == '+' {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid.
func Validate(schema, filePath string, content []byte) []string {
	if schema != SchemaTask {
		return []string{fmt.Sprintf("the schema %q is not a schema of the tasks", schema)}
	}
	if path.Ext(filePath) != ".yaml" {
		return []string{fmt.Sprintf("a file of the schema %s must have the ending .yaml", schema)}
	}
	var v map[string]any
	if err := yaml.Unmarshal(content, &v); err != nil {
		return []string{"the file is not valid YAML: " + firstLine(err)}
	}
	if v == nil {
		return []string{"the file is empty"}
	}
	var reasons []string
	if got, _ := v[SchemaKey].(string); got != schema {
		reasons = append(reasons, fmt.Sprintf("the field %s must be %q", SchemaKey, schema))
	}
	delete(v, SchemaKey)
	commands, ok := commandIDs(v[CommandsKey])
	if !ok {
		reasons = append(reasons, CommandsKey+" must be a list of command IDs")
	}
	delete(v, CommandsKey)
	raw, err := json.Marshal(v)
	if err != nil {
		return append(reasons, "the file has a value that no task can hold: "+firstLine(err))
	}
	t := &taskv1.Task{}
	// Unknown fields are ignored, so a newer hub can add fields.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, t); err != nil {
		return append(reasons, "the fields do not fit the schema: "+firstLine(err))
	}
	if n, ok := LocalNumber(t.GetRef()); !ok {
		reasons = append(reasons, fmt.Sprintf("ref %q is not a local task", t.GetRef()))
	} else if want := FilePath(n); path.Clean(filePath) != want {
		reasons = append(reasons, fmt.Sprintf("a task with ref %q belongs in %s", t.GetRef(), want))
	}
	if strings.TrimSpace(t.GetTitle()) == "" {
		reasons = append(reasons, "title is missing")
	}
	if t.GetAuthorParticipantId() == "" {
		reasons = append(reasons, "author_participant_id is missing")
	}
	if ok && (len(commands) == 0 || t.GetCommandId() == "" || commands[0] != t.GetCommandId()) {
		reasons = append(reasons, CommandsKey+" must start with the command_id, and the command_id must be set")
	}
	return reasons
}

// commandIDs returns the command IDs of the stored list. A missing list is empty.
func commandIDs(v any) ([]string, bool) {
	if v == nil {
		return nil, true
	}
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	var out []string
	for _, c := range list {
		s, ok := c.(string)
		if !ok || s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
