// Package documents is the building block Documents (bb-documents in
// docs/architecture/05-building-blocks.arc42.md). It realizes if-document-api: it creates and
// changes text documents, one Markdown file each under "documents" in the archive (dec-artifacts).
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
//   - A call may name a session. After the commit the hub tells the session through
//     if-session-notice, which appends artifact.created or artifact.changed. Commands are
//     idempotent by command ID (kept in memory): a repeated command returns the first result and
//     tells the session again, so a failed notice can be repeated.
package documents
