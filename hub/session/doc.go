// Package session is the building block Meeting session (bb-session-service in docs/architecture/05-building-blocks.arc42.md).
//
// It manages sessions and participants and keeps one append-only event log per session
// (concept-event-log). It provides if-session-commands and if-session-stream.
//
// How it works:
//
//   - The log in the Store is the only source of truth. The Service derives what it needs to
//     check commands (participants, paused, left, off the record, ended) from the log, and can rebuild
//     it from the store at any time. The only Store for now is MemoryStore.
//   - A session exists as soon as someone joins or subscribes. There is no command to create one.
//   - Every call needs a caller from the Authorizer (the identity token check, #6). The
//     participant ID in the session is the participant of the token: Join adds that participant,
//     and every other command must name it in participant_id, or it fails with PermissionDenied.
//     A participant joins once; another join while it is in the session (for example from a
//     second client of the same participant) returns the first join with duplicate set. A stage
//     link caller may only subscribe to its own session.
//   - Every command is idempotent by its command_id (dec-idempotent-commands). A repeated
//     command_id returns the stored event with duplicate set, also after the session has ended.
//     Reusing a command_id for another command or another participant fails with AlreadyExists.
//   - Error codes: a missing field is InvalidArgument; an unknown participant, utterance or
//     referenced participant is NotFound; a command that does not fit the state (session ended,
//     participant left, already paused, not paused, already off the record) is FailedPrecondition;
//     marking off the record someone else's utterance, or a participant_id that is not the caller, is
//     PermissionDenied; no valid token is Unauthenticated.
//   - Signals (turn.end, silence.long, addressed) come only from clients through SubmitSignal.
//     The session checks them but does not create signals itself. See SubmitSignal and #14.
//   - EndSession stores the confirmed minutes through the Minutes interface (if-minutes-api,
//     rs-session-ends), then writes session.ended. The minutes are in the archive when
//     EndSession returns. For now the confirmed minutes are the text of the last
//     minutes.updated event (#22). See EndSession.
//   - The events are the leaves of a Merkle tree (dec-merkle-log, package merkle of the
//     interface). The state keeps the leaf hash of every event. GetTreeHead, GetConsistencyProof
//     and GetInclusionProof compute heads and proofs from them; the hub key passed to New signs
//     the heads. session.ended carries the signed head of the log before it. A placeholder
//     carries the leaf hash of its original.
//   - The service writes its sessions to the archive (if-archive-storage, dec-session-persistence)
//     when UseArchive is set and RunArchive runs: at the interval and when a session ends, in
//     the background, at most one write at a time. Each write is one commit with, per changed
//     session, the raw log in the line form of package logfile (ArchiveLogFile, with the
//     originals of utterances that are off the record) and the signed tree head over exactly
//     that log (ArchiveTreeHeadFile). The tree head comes from the state, which keeps the
//     roots of the perfect subtrees, so a head costs O(log n) hashes and one signature per size.
//   - Memory (dec-session-eviction, evictIdle): a session that has finished (session.ended, or a
//     complete import), whose last event is in a successful commit of the archive, that no call
//     reads (state.refs: open streams, event reads), that waits for no peer and that has no write
//     due, leaves memory: the store forgets its log, the state with the Merkle leaves and the
//     stored form of the writer are freed. A call that finds no log in memory reads the archive
//     before it treats a session ID as new (lock, loadStored), verifies the log like the restore
//     does and so gets the same events and the same signed tree head.
//   - Restore after a restart (dec-restore-from-peers, restore.go): UseArchive reads the stored
//     log and the tree head of every session that has not finished, one session at a time, and
//     verifies the log against the head with the hub key. Finished sessions stay in the archive
//     until a call needs them. A log that verifies is the start of its session (IDs, idempotency, participants);
//     the next write appends to the stored lines. The events after the last write are lost.
//     Every Subscribe response that carries the last event of the session has the signed head
//     over the events up to it, so every peer holds a signed head over its whole copy. A peer
//     whose cursor is beyond the end (OutOfRange) offers its copy with OfferLog: the hub checks
//     the signature, that the events give the root hash of the head, and a consistency proof
//     between its own current head and the head of the copy. Then it appends what it misses. A
//     copy that is not longer is a no-op, so the first copy that fits wins. Until a copy is
//     accepted, or for at most ArchiveOptions.RestoreWait, commands of a restored session that
//     has not ended fail with Unavailable, so no new event can take an ID of a lost one. There is
//     no wait if ArchiveStateFile says that the hub stopped after a last write of every session.
//     A stored log that does not verify is not used: its commands fail with DataLoss until a
//     peer offers a whole copy.
//   - Schema (dec-session-schema): a session belongs to the schema it was started with. A session
//     this hub starts has sessionstreamv1.Schema; every tree head the hub signs for it carries the schema,
//     and the signature covers it, so the archive keeps the schema in the tree head file. A restored
//     session keeps the schema of its stored tree head. A stored log of another schema is used only
//     if its signature verifies and the leaves this hub computes give its root hash; then it is
//     readable (events, stream, tree heads with its own schema, proofs), but every new command and
//     every OfferLog fails with FailedPrecondition, and the hub does not wait for peers to fill it
//     in. A repeated command_id still returns its stored event. OfferLog also fails with
//     FailedPrecondition for a copy whose tree head has another schema.
//   - Import (dec-session-import, import.go): the Service also realizes the session import
//     (if-session-import). A page is checked as a whole, then written as ordinary events with the
//     command ID of the page: session.imported first, participant.joined with imported set and the
//     ID ImportedPrefix + key, utterance.final with original_time, and import.completed on the last
//     page. The state knows the importer and whether the import runs; while it runs, every session
//     command fails with FailedPrecondition. import.completed wakes the archive writer like
//     session.ended. After the import the session is like any other.
//   - Subscribe closes the stream after it has sent session.ended. It ends the stream with
//     Unauthenticated when the token of the caller expires or is revoked.
package session
