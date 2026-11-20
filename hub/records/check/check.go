// Package check is the interface if-records-check of the building block Records
// (docs/architecture/05-building-blocks.arc42.md): it checks one file of a record type
// (decision, minutes or note) against the schema that the file names (dec-artifact-sync).
//
// The same check serves two callers, so every rule exists once. The artifacts part asks it before
// the hub accepts a push. The records part asks it when it writes a record and when it reads the
// records at the start.
//
// The signatures use only types that Go has built in. A decision or minutes is a YAML file and
// names its schema in the field "schema". A note is a Markdown file (dec-wiki): its YAML front
// matter names the schema in the field "schema" and holds the other fields; the body after the
// front matter is the text.
package check

import (
	"bytes"
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

// TextKey is the field of the text. A note holds its text in the body, not in the front matter.
const TextKey = "text"

// recordType is what the check knows of a record type.
type recordType struct {
	dir      string // the directory in the archive, for example "notes"
	ext      string // the ending of a file, ".yaml" or ".md"
	markdown bool   // the file is Markdown with front matter, and the body is the text
	session  bool   // a record needs a session
}

// types are the record types by schema.
var types = map[string]recordType{
	SchemaDecision: {dir: DecisionsDir, ext: ".yaml"},
	SchemaMinutes:  {dir: MinutesDir, ext: ".yaml", session: true},
	SchemaNote:     {dir: NotesDir, ext: ".md", markdown: true},
}

func (t recordType) filePath(id uint32) string { return fmt.Sprintf("%s/%06d%s", t.dir, id, t.ext) }

// Ext returns the ending of the files of a schema: ".md" for notes, ".yaml" for the others. It is
// empty if the schema is not one of this part.
func Ext(schema string) string { return types[schema].ext }

// FilePath returns the path of the record with this ID of the schema: the directory of the type,
// the ID with at least six digits, and the ending of the type. It is empty if the schema is not
// one of this part.
func FilePath(schema string, id uint32) string {
	t, ok := types[schema]
	if !ok {
		return ""
	}
	return t.filePath(id)
}

// SplitFrontMatter splits a Markdown file into its YAML front matter and its body. The front
// matter is the text between a first line "---" and the next line "---". ok is false if the file
// has no front matter.
func SplitFrontMatter(content []byte) (front, body []byte, ok bool) {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		rest, ok = bytes.CutPrefix(content, []byte("---\r\n"))
	}
	if !ok {
		return nil, nil, false
	}
	lines := bytes.SplitAfter(rest, []byte("\n"))
	for i, line := range lines {
		if string(bytes.TrimRight(line, "\r\n")) == "---" {
			return bytes.Join(lines[:i], nil), bytes.Join(lines[i+1:], nil), true
		}
	}
	return nil, nil, false
}

// BodyText returns the text that a body holds: the body without the one line break that ends
// the file.
func BodyText(body []byte) string {
	s := string(body)
	if t, ok := strings.CutSuffix(s, "\r\n"); ok {
		return t
	}
	return strings.TrimSuffix(s, "\n")
}

// Fields returns the fields of a record file of the schema as a map: the YAML of a YAML file, or
// the front matter of a Markdown file with the body as the field text. The reason is set when the
// file cannot be read that way.
func Fields(schema string, content []byte) (map[string]any, string) {
	t, ok := types[schema]
	if !ok {
		return nil, fmt.Sprintf("the schema %q is not a schema of the records", schema)
	}
	return t.fields(content)
}

func (t recordType) fields(content []byte) (map[string]any, string) {
	data := content
	var body []byte
	if t.markdown {
		front, b, ok := SplitFrontMatter(content)
		if !ok {
			return nil, "the file has no front matter: it must start with a line ---, then the fields, then a line ---"
		}
		data, body = front, b
	}
	var v map[string]any
	if err := yaml.Unmarshal(data, &v); err != nil {
		if t.markdown {
			return nil, "the front matter is not valid YAML: " + firstLine(err)
		}
		return nil, "the file is not valid YAML: " + firstLine(err)
	}
	if !t.markdown {
		if v == nil {
			return nil, "the file is empty"
		}
		return v, ""
	}
	if v == nil {
		return nil, "the front matter is empty"
	}
	if _, has := v[TextKey]; has {
		return nil, "the front matter must not have the field text; the text is the body after the front matter"
	}
	v[TextKey] = BodyText(body)
	return v, ""
}

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
	t, ok := types[schema]
	if !ok {
		return []string{fmt.Sprintf("the schema %q is not a schema of the records", schema)}
	}
	var rec record
	switch schema {
	case SchemaDecision:
		rec = &decisionv1.Decision{}
	case SchemaMinutes:
		rec = &minutesv1.Minutes{}
	case SchemaNote:
		rec = &notev1.Note{}
	}
	if path.Ext(filePath) != t.ext {
		return []string{fmt.Sprintf("a file of the schema %s must have the ending %s", schema, t.ext)}
	}
	v, reason := t.fields(content)
	if reason != "" {
		return []string{reason}
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
	} else if want := t.filePath(rec.GetId()); path.Clean(filePath) != want {
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
	if t.session && rec.GetSessionId() == "" {
		reasons = append(reasons, "session_id is missing")
	}
	if n, ok := rec.(*notev1.Note); ok {
		reasons = append(reasons, TagProblems(n.GetTags())...)
	}
	return reasons
}

// TagProblems returns what is wrong with the tags of a note: a tag must be one line, not blank,
// without spaces around it, and not repeated.
func TagProblems(tags []string) []string {
	var reasons []string
	seen := map[string]bool{}
	for i, tag := range tags {
		switch {
		case strings.TrimSpace(tag) == "":
			reasons = append(reasons, fmt.Sprintf("tag %d is blank", i+1))
		case strings.ContainsAny(tag, "\r\n"):
			reasons = append(reasons, fmt.Sprintf("tag %d has a line break", i+1))
		case strings.TrimSpace(tag) != tag:
			reasons = append(reasons, fmt.Sprintf("tag %d has spaces around it", i+1))
		case seen[tag]:
			reasons = append(reasons, fmt.Sprintf("tag %d (%q) is repeated", i+1, tag))
		}
		seen[tag] = true
	}
	return reasons
}

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
