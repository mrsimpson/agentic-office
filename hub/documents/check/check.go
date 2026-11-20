// Package check holds the stub of a document (concept-session-release) and tells a stub from a
// document. It is part of the building block Documents (bb-documents).
//
// A document that waits for the release of its session lies in the staging area of the session,
// and its stub lies at the same path in the artifact repository. Every stub of a document has the
// same content (Stub): Markdown whose front matter names the schema of documents (Schema) and
// holds "stub: true", and a body that says that the document waits for a release. It names no
// session, no time and no author. A stub is recognised byte for byte (IsStub), so the documents
// part and a later check of pushed documents decide the same way.
//
// The signatures use only types that Go has built in.
package check

// Schema is the name of the schema of documents. A stub names it in its front matter.
const Schema = "office.document.v1"

// stub is the content of every stub of a document.
const stub = "---\nschema: " + Schema + "\nstub: true\n---\nThis document waits for the release of the session that wrote it.\n"

// Stub returns the content of the stub of a document: the same for every staged document.
func Stub() []byte { return []byte(stub) }

// IsStub reports whether content is the stub of a document.
func IsStub(content []byte) bool { return string(content) == stub }
