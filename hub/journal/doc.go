// Package journal is the building block Journal (bb-journal in docs/architecture/05-building-blocks.arc42.md).
//
// It stores decisions, tasks, minutes and notes with their origin. It provides if-journal-api:
// JournalService for clients, and Service.Record for other building blocks of the server (the
// meeting session stores the minutes with it when a session ends, rs-session-ends).
//
// How it works:
//
//   - The journal is one append-only list of entries. Each entry has an ID that grows by one and
//     starts at 1. Entries never change.
//   - Every client call needs a caller from the Authorizer (the identity token check). The author
//     of an entry is the participant of the token: author_participant_id must name it, or the
//     call fails with PermissionDenied. The server sets author_kind (human or agent) from the
//     token, at the time of writing. A stage link cannot read or write the journal.
//   - Any participant with a valid token can append entries of every kind and read all entries.
//     Rights by role are open (#14, #15, #22).
//   - session_id on an entry is not checked against the meeting sessions. It only links the
//     entry to a session, and ListEntries can filter by it.
//   - Every append is idempotent by its command_id (dec-idempotent-commands). A repeated request
//     returns the stored entry with duplicate set. The same command_id with another kind, text,
//     author or session fails with AlreadyExists. Command IDs that start with "office/" are
//     reserved for entries that the server writes itself (ReservedPrefix).
//   - ListEntries returns the entries with an ID greater than after_entry_id that match kind
//     and session_id, oldest first. It returns at most limit entries: 0 means DefaultLimit (100),
//     and more than MaxLimit (1000) is reduced to MaxLimit. To read the next page, pass the ID of
//     the last entry as after_entry_id. An empty page means there are no more entries.
//   - Storage is behind the Store interface. MemoryStore keeps entries in memory. FileStore also
//     appends every entry to a file, one JSON object per line in the form of the API. The office
//     server uses a FileStore in its data directory, so the journal survives a restart. We chose
//     this because the journal is the system of record, and the file is the API form: a later
//     store (persistence is OPEN in the architecture) can import it with no conversion.
//   - Error codes: a missing or bad field is InvalidArgument; a participant that is not the
//     caller, or a stage link, is PermissionDenied; no valid token is Unauthenticated; a reused
//     command_id is AlreadyExists.
package journal
