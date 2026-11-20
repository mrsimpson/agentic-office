package check

// Describe and Description belong to the interface if-artifact-check: the description of a file
// for the wiki (concept-wiki, dec-wiki), whichever part owns its type. The part that owns the type
// gives the description; this interface only passes the file on, so no part reads the fields of
// another type (dec-artifact-sync).
//
// The rules:
//
//   - A file that the artifact check refuses (Validate) is not described.
//   - A file that names a schema is described by the part that owns the schema.
//   - A file that names no schema is described by the first part that takes it. Today that is the
//     documents part, for a Markdown file under documents/. Any other file without a schema is
//     not described.
//   - The stub of a staged artifact (concept-session-release) is described as a stub only: Stub
//     is true, and no other field is set apart from Schema.
//   - Source is true for a source of the wiki: a decision, minutes or a note, not a stub. The
//     records part owns these types, and they are what a document cites (dec-wiki).
//   - Status, Owner and Reviews are the state of review of a document: its status, the owner and
//     the reviews, as its front matter names them.
//   - Links and Sources are paths from the top of the repository. Links are the relative links in
//     the text of the file. Sources are the records that a document cites in its front matter.
//     Which types have which fields: see the description interface of each part
//     (if-records-describe, if-tasks-describe, if-slides-describe, if-documents-describe).

import (
	"github.com/mrsimpson/agentic-office/hub/artifacts"
)

// Description is what the hub tells about one file of the artifact repository.
type Description struct {
	// Schema is the schema that the file names, or "" for a document without a schema.
	Schema  string
	Title   string
	Summary string
	Tags    []string
	// Links are the paths, from the top of the repository, that the text of the file links to.
	Links []string
	// Sources are the paths, from the top of the repository, of the records that a document
	// cites. Only documents have sources.
	Sources []string
	// Source is true for a source of the wiki that a document may cite: a decision, minutes or a
	// note that is not a stub (dec-wiki).
	Source bool
	// Status is the status of a document (draft, reviewed or outdated), Owner the participant ID
	// of its owner, and Reviews its reviews in the order of its front matter. Only a document
	// that names the schema of documents has them.
	Status  string
	Owner   string
	Reviews []Review
	// Stub is true for the stub of a staged artifact.
	Stub bool
}

// Review is one review of a document.
type Review struct {
	// ParticipantID is the reviewer.
	ParticipantID string
	// Date is the day of the review, in the form YYYY-MM-DD.
	Date string
}

// Describe describes the file at filePath, a path relative to the repository of the tenant, with
// the given content. ok is false when the file is not valid, or when no part describes it.
func Describe(filePath string, content []byte) (d Description, ok bool) {
	clean, schema, named, err := schemaOf(filePath, content)
	if err != nil {
		return Description{}, false
	}
	var candidates []artifacts.Part
	if named {
		owner, known := artifacts.Owner(schema)
		if !known || len(owner.Validate(schema, clean, content)) > 0 {
			return Description{}, false
		}
		candidates = []artifacts.Part{owner}
	} else {
		candidates = artifacts.Describers()
	}
	for _, p := range candidates {
		if p.Describe == nil {
			continue
		}
		pd, ok := p.Describe(schema, clean, content)
		if !ok {
			continue
		}
		var reviews []Review
		for _, r := range pd.Reviews {
			reviews = append(reviews, Review{ParticipantID: r.ParticipantID, Date: r.Date})
		}
		return Description{
			Schema: schema, Title: pd.Title, Summary: pd.Summary, Tags: pd.Tags,
			Links: pd.Links, Sources: pd.Sources, Source: pd.Source,
			Status: pd.Status, Owner: pd.Owner, Reviews: reviews, Stub: pd.Stub,
		}, true
	}
	return Description{}, false
}
