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
//   - Every command is idempotent by its command_id (dec-idempotent-commands). A repeated
//     command_id returns the stored event with duplicate set, also after the session has ended.
//     Reusing a command_id for another command or another participant fails with AlreadyExists.
//   - Error codes: a missing field is InvalidArgument; an unknown participant, utterance or
//     referenced participant is NotFound; a command that does not fit the state (session ended,
//     participant left, already paused, not paused, already retracted) is FailedPrecondition;
//     retracting someone else's utterance is PermissionDenied.
//   - Signals (turn.end, silence.long, addressed) come only from clients through SubmitSignal.
//     The session checks them but does not create signals itself. See SubmitSignal and #14.
//   - Subscribe closes the stream after it has sent session.ended.
//   - There is no auth yet. The Authorizer is the place for the token check (#6).
package session
