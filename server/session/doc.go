// Package session is the building block Meeting session (bb-session-service in docs/architecture/05-building-blocks.arc42.md).
//
// It manages sessions and participants and keeps one append-only event log per session
// (concept-event-log). It provides if-session-commands and if-session-stream.
//
// How it works:
//
//   - The log in the Store is the only source of truth. The Service derives what it needs to
//     check commands (participants, paused, left, retracted, ended) from the log, and can rebuild
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
//     participant left, already paused, not paused, already retracted) is FailedPrecondition;
//     retracting someone else's utterance, or a participant_id that is not the caller, is
//     PermissionDenied; no valid token is Unauthenticated.
//   - Signals (turn.end, silence.long, addressed) come only from clients through SubmitSignal.
//     The session checks them but does not create signals itself. See SubmitSignal and #14.
//   - EndSession stores the confirmed minutes in the journal through the Journal interface
//     (if-journal-api, rs-session-ends), then writes session.ended. For now the confirmed minutes
//     are the text of the last minutes.updated event (#22). See EndSession.
//   - Subscribe closes the stream after it has sent session.ended. It ends the stream with
//     Unauthenticated when the token of the caller expires or is revoked.
package session
