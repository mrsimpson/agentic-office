package artifacts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// SchemaKey is the field that names the schema of a file.
const SchemaKey = "schema"

// SchemaOf returns the clean path of a file and the schema that the file names, if any. The error
// says in plain words why the path or the content is not an artifact.
func SchemaOf(filePath string, content []byte) (clean, schema string, named bool, err error) {
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
