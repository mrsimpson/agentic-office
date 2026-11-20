// Package check is the interface if-artifact-check of the building block Artifacts: it checks one
// file against the schema that the file names, whichever part owns the schema
// (dec-artifact-sync).
//
// The signature uses only types that Go has built in, so a caller needs no type of the hub.
//
// The rules:
//
//   - An artifact is a Markdown file (.md) or a YAML file (.yaml or .yml). A file with another
//     ending is invalid.
//   - A YAML file names its schema in the top-level field "schema". A Markdown file names it in
//     the field "schema" of its YAML front matter, which is the YAML between a first line "---"
//     and the next line "---".
//   - A file that names a schema is checked by the part that owns the schema. A schema that no
//     part owns is invalid.
//   - A file that names no schema is valid when it is valid Markdown (any text in UTF-8) or valid
//     YAML.
//   - A file is the stub of a staged artifact (concept-session-release) when it names a schema and
//     the part that owns the schema recognises it as the stub of its type (IsStub). Git access
//     asks this for the old state of every path that a push changes or removes.
package check

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/artifacts"
)

// SchemaKey is the field that names the schema of a file.
const SchemaKey = "schema"

// Validate checks the file at filePath, a path relative to the repository of the tenant, with the
// given content. It returns the reasons in plain words, or nothing when the file is valid.
func Validate(filePath string, content []byte) []string {
	clean, schema, named, err := schemaOf(filePath, content)
	if err != nil {
		return []string{err.Error()}
	}
	if !named {
		return nil
	}
	owner, ok := artifacts.Owner(schema)
	if !ok {
		return []string{fmt.Sprintf("the schema %q is not known; the schemas are %s", schema, strings.Join(artifacts.Schemas(), ", "))}
	}
	return owner.Validate(schema, clean, content)
}

// IsStub reports whether the file at filePath with the given content is the stub of a staged
// artifact (concept-session-release): the file names a schema, and the part that owns the schema
// recognises the content as the stub of its type. A file that is not valid is never a stub.
func IsStub(filePath string, content []byte) bool {
	clean, schema, named, err := schemaOf(filePath, content)
	if err != nil || !named {
		return false
	}
	owner, ok := artifacts.Owner(schema)
	return ok && owner.IsStub(schema, content) && len(owner.Validate(schema, clean, content)) == 0
}

// schemaOf returns the clean path of a file and the schema that the file names, if any. The error
// says in plain words why the path or the content is not an artifact.
func schemaOf(filePath string, content []byte) (clean, schema string, named bool, err error) {
	clean = path.Clean(filePath)
	if filePath == "" || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return clean, "", false, fmt.Errorf("the path %q is not a path inside the repository", filePath)
	}
	switch ext := path.Ext(clean); ext {
	case ".md":
		if !utf8.Valid(content) {
			return clean, "", false, errors.New("a Markdown file must be text in UTF-8")
		}
		schema, named, err = markdownSchema(content)
	case ".yaml", ".yml":
		schema, named, err = yamlSchema(content)
	default:
		err = fmt.Errorf("a file must end in .md, .yaml or .yml, not %q", ext)
	}
	return clean, schema, named, err
}

// markdownSchema returns the schema that the front matter of a Markdown file names, if any.
func markdownSchema(content []byte) (schema string, named bool, err error) {
	rest, ok := bytes.CutPrefix(content, []byte("---\n"))
	if !ok {
		rest, ok = bytes.CutPrefix(content, []byte("---\r\n"))
	}
	if !ok {
		return "", false, nil
	}
	lines := bytes.SplitAfter(rest, []byte("\n"))
	for i, line := range lines {
		if string(bytes.TrimRight(line, "\r\n")) == "---" {
			schema, named, err = yamlSchema(bytes.Join(lines[:i], nil))
			if err != nil {
				err = errors.New("the front matter of the Markdown file: " + err.Error())
			}
			return schema, named, err
		}
	}
	// No closing line: the file only starts with a rule, so it is plain Markdown.
	return "", false, nil
}

// yamlSchema returns the schema that the top-level field "schema" of a YAML file names, if any.
// It also finds out whether the content is valid YAML.
func yamlSchema(content []byte) (schema string, named bool, err error) {
	dec := yaml.NewDecoder(bytes.NewReader(content))
	first := true
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				return schema, named, nil
			}
			line, _, _ := strings.Cut(err.Error(), "\n")
			return "", false, errors.New("the file is not valid YAML: " + line)
		}
		if !first {
			continue
		}
		first = false
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		raw, has := m[SchemaKey]
		if !has {
			continue
		}
		s, ok := raw.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", false, fmt.Errorf("the field %s must be the name of a schema", SchemaKey)
		}
		schema, named = s, true
	}
}
