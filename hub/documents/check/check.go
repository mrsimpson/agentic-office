// Package check is the interface if-documents-check of the building block Documents
// (docs/architecture/05-building-blocks.arc42.md). It holds the stub of a document
// (concept-session-release), tells a stub from a document, and checks a document that names the
// schema of documents (dec-artifact-sync).
//
// A document is Markdown under the directory Dir. Most documents name no schema: they are plain
// Markdown, and the artifacts part accepts them without asking this part. A document may name the
// schema of documents (Schema) in the field "schema" of its front matter; the artifacts part then
// passes it to Validate.
//
// A document that waits for the release of its session lies in the staging area of the session,
// and its stub lies at the same path in the artifact repository. Every stub of a document has the
// same content (Stub): Markdown whose front matter names the schema of documents and holds
// "stub: true", and a body that says that the document waits for a release. It names no session,
// no time and no author. A stub is recognised byte for byte (IsStub), so the documents part and
// the check of pushed documents decide the same way.
//
// The signatures use only types that Go has built in.
package check

import (
	"fmt"
	"path"
	"strings"
)

// Schema is the name of the schema of documents. A stub names it in its front matter.
const Schema = "office.document.v1"

// Dir is the directory of the documents in the archive.
const Dir = "documents"

// stub is the content of every stub of a document.
const stub = "---\nschema: " + Schema + "\nstub: true\n---\nThis document waits for the release of the session that wrote it.\n"

// Stub returns the content of the stub of a document: the same for every staged document.
func Stub() []byte { return []byte(stub) }

// IsStub reports whether content is the stub of a document.
func IsStub(content []byte) bool { return string(content) == stub }

// Schemas returns the names of the schemas that this part owns.
func Schemas() []string { return []string{Schema} }

// PathProblem returns why p is not the path of a document, or "". The path of a document is
// Dir/<name>.md. The caller checks that p is a clean path inside the repository.
func PathProblem(p string) string {
	if !strings.HasPrefix(p, Dir+"/") || path.Ext(p) != ".md" {
		return "the path of a document is " + Dir + "/<name>.md"
	}
	return ""
}

// Validate checks the content of the file at filePath against the schema. It returns the reasons
// in plain words, or nothing when the file is valid. A document of the schema is any Markdown at
// the path of a document; the stub of a document is one of them.
func Validate(schema, filePath string, content []byte) []string {
	if schema != Schema {
		return []string{fmt.Sprintf("the schema %q is not a schema of the documents", schema)}
	}
	if PathProblem(path.Clean(filePath)) != "" {
		return []string{fmt.Sprintf("a file of the schema %s lies in %s/ and ends in .md", schema, Dir)}
	}
	return nil
}
