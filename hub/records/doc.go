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
// Service implements all three. Tasks have their own building block (package tasks).
//
// How it works:
//
//   - Every record type is a list of records. Each record has an ID that grows by one per type
//     and starts at 1. The hub never changes a record; a push can.
//   - Storage (dec-yaml-git-storage, dec-wiki): every record is one file in the archive
//     (if-archive-storage): decisions/NNNNNN.yaml, minutes/NNNNNN.yaml and notes/NNNNNN.md,
//     where NNNNNN is the ID with at least six digits. A decision or minutes is YAML with the
//     field names of the contract (snake_case, enums by name, times in RFC 3339), after the field
//     schema that names the schema of the type (office.decision.v1, office.minutes.v1). A text
//     with line breaks is written as a literal block. A note is Markdown: its YAML front matter
//     names the schema (office.note.v1) and holds the fields of the contract that are set (the ID,
//     the command ID, the author, the author kind, the session, the time and the tags), and the
//     body after the front matter is the text, ended by one line break. The check of the part
//     (package check, if-records-check) decides what a valid file is. The part uses it for every
//     file it writes and reads, and Git access uses it for a push, so every rule exists once.
//     Every write is one commit, and a command returns only after its commit, so a record that a
//     client got back is in the repository.
//   - Open reads all records from the current commit of the archive and keeps them in memory, so
//     records survive a restart of the hub. Reads use only the memory. A record file that is not
//     valid stops the start with an error. A file whose name is not an ID with the ending of its
//     type, for example notes/plan.md, is not a record and is ignored.
//   - Reload (if-archive-watch): the part watches the archive of its tenant and reloads the
//     files of its directory (decisions, minutes, notes) that a push or a commit changed, created or removed. It reads
//     them from the archive again, checks each with the check of the part and keeps serving reads
//     from memory. A file that is not valid after a push is logged once for its version and
//     ignored: the part keeps what it had for that path. (The hook of Git access refuses such
//     files, so this is a defence.) A file that is gone is removed from memory. If the archive
//     drops the watcher because the part was too slow, the part watches again and reads all its
//     files. A file that bypasses the hook and is not valid still counts for the next ID, so
//     the part never takes its ID.
//   - Numbers: the next ID is one above the highest ID ever seen, in a file, in memory or in
//     the names that ever lay in the directory in the history of main (Names of
//     if-archive-storage; the part asks at start and when it reads all files again). An ID is
//     never used twice, also after a push removed its file and the hub restarted. The numbers can have gaps, because a push may bring any ID. A part creates its file with "must not
//     exist yet" (storage.NoFile). If a push put a file there first, the create fails inside the
//     archive; the part reads that file, takes the next free ID and tries again, so the
//     caller sees no error.
//     Close stops the part from following the archive.
//   - Every call of a client needs the caller from the identity token check (if-token-check).
//     author_participant_id must name the participant of the token, or the call fails with
//     PermissionDenied. The hub sets author_kind (human or agent) from the token. A stage link
//     cannot read or write records. Any participant may write and read all records; rights by
//     role are open (#14, #15, #22).
//   - session_id on a record is not checked against the meeting sessions. It only links the
//     record to a session, and the list calls can filter by it.
//   - Writes are idempotent by command_id (dec-idempotent-commands), per record type. A
//     repeated request returns the stored record with duplicate set. The same command_id with
//     another text, author or session (or other tags, for a note) fails with AlreadyExists.
//   - Tags of a note: the part removes the spaces around each tag and drops repeats. A blank tag
//     or one with a line break is InvalidArgument.
//   - The list calls return the records with an ID greater than after_id that match session_id,
//     oldest first, at most limit of them: 0 means DefaultLimit (100), and more than MaxLimit
//     (1000) is reduced to MaxLimit. To read the next page, pass the ID of the last record.
//   - Error codes: a missing or bad field is InvalidArgument; a participant that is not the
//     caller, or a stage link, is PermissionDenied; no valid token is Unauthenticated; a reused
//     command_id is AlreadyExists; no stored minutes of a session is NotFound; a failed write
//     to the archive is Internal, and the client can repeat the command.
package records
