// Package session is the building block Meeting session (bb-session-service in docs/architecture/05-building-blocks.arc42.md).
//
// It manages sessions and participants and keeps one append-only event log per session
// (concept-event-log). It provides if-session-commands and if-session-stream, and the release
// status of sessions to the other parts of the hub (if-session-release, package release).
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
//     rs-session-ends), writes the deadline of the release, then writes session.ended. The minutes are in the archive when
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
//   - Activity (dec-agent-activity, activity.go): the Service also realizes the session activity
//     (if-session-activity). It keeps the latest activity of each participant in memory only,
//     never in the Store, the archive or a stream of the log. A report lives ActivityTTL. The
//     activity of a participant ends with its participant.left, all activities end with
//     session.ended (endActivities, under the lock of the session, so a report cannot slip in
//     after). WatchActivity sends the activities at once and after every change or expiry. A
//     tool is a short name only; the hub refuses free text. The activities are lost when the hub
//     restarts; the agent runner renews its report anyway.
//   - Who reads a session (mayRead in list.go, concept-multi-tenancy): Subscribe, GetEvents, the
//     tree heads and proofs and WatchActivity answer only everyone who ever joined the session, the
//     participant who imported it, a tenant admin, and a stage link of the session. Everybody
//     else gets NotFound, as for a session that does not exist.
//   - Session list (dec-web-portal, list.go): the Service also realizes the session list
//     (if-session-list). It summarizes every session in memory from the Store, and every ended
//     session that only the archive holds from its stored log, after that log verified against its
//     signed tree head; the summaries of finished stored sessions are kept, so each stored log is
//     read once. A session that leaves memory during a list is read from the archive. A caller
//     sees the sessions it joined or imported, a tenant admin (Caller.TenantAdmin) every session.
//   - Where the archive lies: the session uses only the session-log repository of the tenant
//     (storage.Archives.SessionLogs) for everything it stores, restores, lists and reads. The
//     artifact repository of the tenant never holds a session log, because the log holds the
//     original of sentences that are off the record (dec-off-the-record).
//   - Check a stored session: logfile.UnmarshalLines and logfile.UnmarshalTreeHead read the two
//     files, and merkle.VerifyEvents(events, head, hubKey) checks them (the hub key comes from
//     GetHubKey). "git log -- sessions/<name>/log.jsonl" in the session-log repository of the tenant
//     lists the writes of a session; every commit extends the log of the one before. The file
//     sessions/+state.yaml says whether the archive is complete: "complete: true" when the hub
//     stopped after a last write of every session, false from the next start on. No session name
//     starts with "+".
//   - The imported log proves which participant imported it and that it has not changed since;
//     the signed tree heads do not prove that the imported speakers said it.
//   - Open points (#47): a session that the hub never wrote to the archive is unknown after a
//     crash until a peer offers a whole copy, and a new join before that starts a new log; a peer
//     that comes back only after the wait has a copy that forks; the CLI and the MCP server do
//     not repeat a command that fails with Unavailable by themselves.
//   - Release (concept-session-release, dec-staging-repository, releases.go): UseRelease gives the
//     service the staging repository of the tenant. A session opens its staging area with its
//     first join or import page, and every participant who joins, and the importer, becomes a
//     reader before its event is written. EndSession writes the deadline into the area once: the
//     end plus ReleaseOptions.GracePeriod, or the end itself if no human took part (joined as a
//     human, or imported as a human; imported speakers do not count); a session without humans is
//     released before EndSession answers. ReleaseSession and ObjectToRelease take only a human who
//     took part (state.tookPart, also one who left): an agent gets PermissionDenied, anyone else
//     NotFound. The release is session.released in the log first (actor empty for the hub, command
//     ID HubReleaseCommandID), then storage.Staging.Release moves the area with that author; an
//     objection is release.objected, and the area records it. RunRelease is the one worker per
//     tenant: it keeps the deadlines in memory, and UseRelease rebuilds them from
//     storage.Staging.Areas and the logs, so a release that a restart cut off and a deadline that
//     passed while the hub was down are due at start. A failed release is tried again after a
//     few seconds. Before a human releases, findQuotes searches the staged files for the
//     sentences off the record, as raw text (case and white space ignored, at least three words).
//     While it finds any, ReleaseSession writes no event and answers with the places and a quote
//     token: an HMAC, keyed from the signing key of the hub, over the session and each place with
//     the sentence it quotes. A request whose accept_quotes equals the token of a new search
//     releases; nothing is changed. When a deadline comes while a session with humans has quotes,
//     releaseDue holds it instead: release.objected with an empty actor and the command ID
//     HubHoldCommandID, and the area records it. GetRelease returns the status, the staged paths
//     (storage.Staging.List) and the release commit (storage.Staging.Released).
//   - NoteArtifact accepts every participant who took part (state.tookPart), the same rule as
//     release.Checker.TookPart, so the importer writes for its session without joining.
//   - After session.ended (dec-session-end-milestone), a command fails, except release, object,
//     NoteArtifact and, until the release, MarkOffTheRecord. run writes such an event (and
//     session.ended itself) to the archive before it answers (persistNow), because no peer follows
//     an ended session. Subscribe stops at session.ended and sends the tree head over the log up
//     to it; GetEvents returns the events after it too.
//   - Subscribe closes the stream after it has sent session.ended. It ends the stream with
//     Unauthenticated when the token of the caller expires or is revoked.
package session
