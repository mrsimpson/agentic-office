// Package check is the interface if-slides-check of the building block Slides
// (docs/architecture/05-building-blocks.arc42.md): it checks one file of a slide deck against the
// schema that the file names (dec-artifact-sync).
//
// The same check serves two callers, so every rule exists once. The artifacts part asks it before
// the hub accepts a push. The slides part asks it when it writes a deck and when it reads the
// decks at the start.
//
// The check covers the YAML file of a deck, which holds its input. The rendered HTML file next to
// it is output and has no schema.
//
// The signatures use only types that Go has built in. Every stored deck is a YAML file and names
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

	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

// SchemaDeck is the schema of a slide deck. The name is the contract of the type and its version.
const SchemaDeck = "office.slides.v1"

const (
	// SchemaKey is the field of a stored deck that names its schema.
	SchemaKey = "schema"
	// Kind is the first part of the ID of a deck, for example "slides/42". It is also the
	// directory of the decks in the archive.
	Kind = "slides"
)

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string { return []string{SchemaDeck} }

// FilePath returns the path of a file of deck n in the archive, with the ending ext.
func FilePath(n uint64, ext string) string { return fmt.Sprintf("%s/%06d%s", Kind, n, ext) }

// DeckID returns the ID of deck n.
func DeckID(n uint64) string { return Kind + "/" + strconv.FormatUint(n, 10) }

// Number returns the number of a deck ID such as "slides/42".
func Number(id string) (uint64, bool) {
	rest, ok := strings.CutPrefix(id, Kind+"/")
	if !ok || rest == "" || rest[0] == '0' || rest[0] == '+' {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid.
func Validate(schema, filePath string, content []byte) []string {
	if schema != SchemaDeck {
		return []string{fmt.Sprintf("the schema %q is not a schema of the slides", schema)}
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
	// The rendered output is the HTML file next to the YAML file, not a field of it.
	delete(v, "media_type")
	delete(v, "content")
	raw, err := json.Marshal(v)
	if err != nil {
		return append(reasons, "the file has a value that no deck can hold: "+firstLine(err))
	}
	d := &slidesv1.Deck{}
	// Unknown fields are ignored, so a newer hub can add fields.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, d); err != nil {
		return append(reasons, "the fields do not fit the schema: "+firstLine(err))
	}
	if n, ok := Number(d.GetId()); !ok {
		reasons = append(reasons, fmt.Sprintf("id %q is not the ID of a deck", d.GetId()))
	} else if want := FilePath(n, ".yaml"); path.Clean(filePath) != want {
		reasons = append(reasons, fmt.Sprintf("the deck %q belongs in %s", d.GetId(), want))
	}
	if d.GetCommandId() == "" {
		reasons = append(reasons, "command_id is missing")
	}
	if d.GetAuthorParticipantId() == "" {
		reasons = append(reasons, "author_participant_id is missing")
	}
	if len(d.GetSlides()) == 0 {
		reasons = append(reasons, "the deck has no slides")
	}
	for i, s := range d.GetSlides() {
		if strings.TrimSpace(s.GetTitle()) == "" {
			reasons = append(reasons, fmt.Sprintf("slide %d has no title", i+1))
		}
	}
	return reasons
}

func firstLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}
