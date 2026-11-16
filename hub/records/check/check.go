// Package check is the interface if-records-check of the building block Records
// (docs/architecture/05-building-blocks.arc42.md): it checks one file of a record type
// (decision, minutes or note) against the schema that the file names (dec-artifact-sync).
//
// The same check serves two callers, so every rule exists once. The artifacts part asks it before
// the hub accepts a push. The records part asks it when it writes a record and when it reads the
// records at the start.
//
// The signatures use only types that Go has built in. Every stored record is a YAML file and
// names its schema in the field "schema".
package check

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
)

// The schemas of this part. The name is the contract of the type and its version.
const (
	SchemaDecision = "office.decision.v1"
	SchemaMinutes  = "office.minutes.v1"
	SchemaNote     = "office.note.v1"
)

// The directories of the record types in the archive.
const (
	DecisionsDir = "decisions"
	MinutesDir   = "minutes"
	NotesDir     = "notes"
)

// SchemaKey is the field of a stored record that names its schema.
const SchemaKey = "schema"

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string {
	return []string{SchemaDecision, SchemaMinutes, SchemaNote}
}

// FilePath returns the path of the record with this ID in the directory of its type.
func FilePath(dir string, id uint32) string { return fmt.Sprintf("%s/%06d.yaml", dir, id) }

// record is what the check needs of every record type.
type record interface {
	proto.Message
	GetId() uint32
	GetCommandId() string
	GetText() string
	GetAuthorParticipantId() string
	GetSessionId() string
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid.
func Validate(schema, filePath string, content []byte) []string {
	var (
		dir     string
		rec     record
		session bool // the type needs a session
	)
	switch schema {
	case SchemaDecision:
		dir, rec = DecisionsDir, &decisionv1.Decision{}
	case SchemaMinutes:
		dir, rec, session = MinutesDir, &minutesv1.Minutes{}, true
	case SchemaNote:
		dir, rec = NotesDir, &notev1.Note{}
	default:
		return []string{fmt.Sprintf("the schema %q is not a schema of the records", schema)}
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
	raw, err := json.Marshal(v)
	if err != nil {
		return append(reasons, "the file has a value that no record can hold: "+firstLine(err))
	}
	// Unknown fields are ignored, so a newer hub can add fields.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, rec); err != nil {
		return append(reasons, "the fields do not fit the schema: "+firstLine(err))
	}
	if rec.GetId() == 0 {
		reasons = append(reasons, "id is missing")
	} else if want := FilePath(dir, rec.GetId()); path.Clean(filePath) != want {
		reasons = append(reasons, fmt.Sprintf("a record with id %d belongs in %s", rec.GetId(), want))
	}
	if rec.GetCommandId() == "" {
		reasons = append(reasons, "command_id is missing")
	}
	if strings.TrimSpace(rec.GetText()) == "" {
		reasons = append(reasons, "text is missing")
	}
	if rec.GetAuthorParticipantId() == "" {
		reasons = append(reasons, "author_participant_id is missing")
	}
	if session && rec.GetSessionId() == "" {
		reasons = append(reasons, "session_id is missing")
	}
	return reasons
}

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
