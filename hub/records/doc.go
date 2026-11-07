// Package records is the building block Records (bb-records in
// docs/architecture/05-building-blocks.arc42.md).
//
// It keeps three types of records of the team, each behind its own interface
// (dec-records-per-type):
//
//   - decisions (if-decision-api, DecisionService): record and list decisions;
//   - minutes (if-minutes-api, MinutesService): read the minutes of ended sessions. The meeting
//     session stores them with Service.StoreMinutes when a session ends (rs-session-ends);
//     clients cannot write minutes here;
//   - notes (if-note-api, NoteService): add and list free notes, with or without a session.
//
// Service implements all three. Tasks are not records of this block (#38).
//
// How it works:
//
//   - Every record type is a list of records. Each record has an ID that grows by one per type
//     and starts at 1. Records never change.
//   - Storage (dec-yaml-git-storage): every record is one YAML file in the archive
//     (if-archive-storage): decisions/NNNNNN.yaml, minutes/NNNNNN.yaml and notes/NNNNNN.yaml,
//     where NNNNNN is the ID with at least six digits. The YAML has the field names of the
//     contract (snake_case, enums by name, times in RFC 3339). A text with line breaks is
//     written as a literal block. Every write is one commit, and a command returns only after
//     its commit, so a record that a client got back is in the repository.
//   - Open reads all records from the current commit of the archive and keeps them in memory, so
//     records survive a restart of the hub. Reads use only the memory. A file that is not valid
//     stops the start with an error; files without the ending .yaml are ignored.
//   - Every call of a client needs the caller from the identity token check (if-token-check).
//     author_participant_id must name the participant of the token, or the call fails with
//     PermissionDenied. The hub sets author_kind (human or agent) from the token. A stage link
//     cannot read or write records. Any participant may write and read all records; rights by
//     role are open (#14, #15, #22).
//   - session_id on a record is not checked against the meeting sessions. It only links the
//     record to a session, and the list calls can filter by it.
//   - Writes are idempotent by command_id (dec-idempotent-commands), per record type. A
//     repeated request returns the stored record with duplicate set. The same command_id with
//     another text, author or session fails with AlreadyExists.
//   - The list calls return the records with an ID greater than after_id that match session_id,
//     oldest first, at most limit of them: 0 means DefaultLimit (100), and more than MaxLimit
//     (1000) is reduced to MaxLimit. To read the next page, pass the ID of the last record.
//   - Error codes: a missing or bad field is InvalidArgument; a participant that is not the
//     caller, or a stage link, is PermissionDenied; no valid token is Unauthenticated; a reused
//     command_id is AlreadyExists; no stored minutes of a session is NotFound; a failed write
//     to the archive is Internal, and the client can repeat the command.
package records
