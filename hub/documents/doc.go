// Package documents is the building block Documents (bb-documents in
// docs/architecture/05-building-blocks.arc42.md). It realizes if-document-api: it creates, changes
// and removes text documents, one Markdown file each under "documents" in the archive
// (dec-artifacts).
//
// How it works:
//
//   - Every call needs the token of a participant. The archive records that participant as the
//     author of the commit: the name is the display name of the token, the address is built from
//     the participant ID.
//   - A create writes a file that must not exist yet. A change names the version that its author
//     read and is refused with aborted when the file has another version by then.
//   - The version is the Git object ID of the content, so the hub can tell it without reading the
//     archive again.
//   - A call may name a session. Only a participant of it may (if-session-release). After the
//     commit the hub tells the session through if-session-notice, which appends artifact.created
//     or artifact.changed. Commands are idempotent by command ID (kept in memory): a repeated
//     command returns the first result and tells the session again, so a failed notice can be
//     repeated. A removal adds no event.
//   - Staging (concept-session-release): a create that names a session that is not released
//     writes the document into the staging area of the session, then the stub of a document
//     (package check) at the same path into the artifact repository, with the hub as the author
//     and StubMessage. If the stub cannot be written, the staged file is taken back. A path whose
//     staged document the same session removed keeps its stub, and a create there writes only the
//     staged file. A session that is released, or no session, writes into the artifact
//     repository.
//   - The part keeps no documents in memory. At each write it reads what lies at the path: a
//     document in the artifact repository (released), a staged document of the area that names
//     the path, a removed staged document, or a stub that no area names. A document in the
//     artifact repository wins over a staged one.
//   - A change or a removal of a staged document must name its session, and the caller must have
//     taken part in it. While the release of the session moves the area, a write is unavailable
//     and the client repeats it. A change of a released document goes to the artifact repository
//     and is not held; a removal of a released document is refused (Git removes it). No call
//     changes a stub, and no call writes the content of a stub.
//   - The part asks the meeting session before it takes its lock, and it reads the path again
//     under the lock: a write whose path changed meanwhile is aborted.
//   - Open writes the missing stub of every staged document, which a crash between the two commits
//     of a create leaves.
//   - The documents are read through the archive read interface (if-archive-read), which shows a
//     staged document only to the readers of its session.
package documents
