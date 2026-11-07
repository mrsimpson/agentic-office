// Package replicator is the building block Log replicator (bb-replicator in docs/architecture/05-building-blocks.arc42.md).
//
// It copies the session stream into one local JSONL file per session and participant. Provides
// if-local-log. The file format is hub/session/api/logfile (the line form of the session stream): one compact canonical JSON line per event, with
// the dotted type and the numeric event ID.
//
// How it works:
//
//   - Open takes an exclusive lock (flock) on the file, so only one replicator writes it. A second
//     one fails with ErrRunning. Running tells whether the lock is taken.
//   - The last complete line is the cursor (concept-event-log). An incomplete last line (a crash
//     during a write) is cut off. Each line is written with one write call.
//   - Run subscribes from the cursor. After a drop it reconnects with a backoff: 100 ms, doubling
//     up to MaxBackoff (1 s), with a little jitter; at once after "unauthenticated". It keeps
//     trying until ctx ends. The 1 s bound keeps the catch-up after a drop within the 2 s of
//     qs-resume-after-drop.
//   - Next to the log, the replicator keeps the latest signed tree head that came with the
//     stream (TreeHeadPath, default <log>.tree-head.yaml, replaced as a whole file). The hub
//     sends one with the last event it has, so the head covers the whole local log.
//   - If the hub does not know the cursor (out_of_range), it lost events, for example in a
//     restart (dec-restore-from-peers). The replicator asks the hub for its size and offers the
//     events after it with the stored head (OfferLog). It retries until the hub has the cursor
//     again, from its copy or from another peer. Then it checks that the hub's tree head at the
//     cursor has the root hash of the local log and follows the stream again. If the roots
//     differ, or the hub rejects the copy, Run fails (ErrFork): the local log cannot go on.
//   - Run stops after it has written session.ended, or participant.left of its own participant
//     (the participant left, for example with "office meeting leave").
//   - Stopping when the parent process dies is the job of the process that runs the replicator:
//     the CLI runs it as "office replicator run --parent-pid"; the MCP server runs it in its own
//     process, so it ends with it.
package replicator
