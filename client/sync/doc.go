// Package sync is the building block Artifact sync (bb-sync in
// docs/architecture/05-building-blocks.arc42.md). It keeps a clone of the tenant repository in a
// directory that the participant has set up on purpose (dec-artifact-sync). It never syncs a
// directory by itself.
//
//   - Set-up (Init) works out the address of the repository from the hub address and the tenant
//     (package gitweb), clones the branch main and writes into the own Git configuration of the
//     clone, never into the global one: the participant as the author (display name and
//     "<participant ID>@office.invalid"), a credential helper of the office that gives Git
//     a fresh access token, and a hook that refuses a commit in the main directory.
//   - An update (Update) fetches and moves main to the state of the hub without a merge. It runs
//     when a client process starts, after every reconnect and on request. When the hub rewrote
//     history, nothing of the participant is on main, so main simply moves to the new state. The
//     update stops and reports when the main directory has commits of its own, or when a move
//     would overwrite a file that the participant changed and did not commit.
//   - While a client process runs, Follow follows the changes of the artifacts
//     (if-artifact-changes), optionally for path prefixes, and updates the clone after each event.
//     A client process that starts in a set-up directory, or in a worktree of its clone, follows
//     that clone: "office meeting join" and the MCP tool to join start the follow without a flag.
//
// The main directory is a mirror of main. Participants and agents work in worktrees next to it,
// on local branches, and push with plain Git. For the participant, the sync runs only clone and
// update; for its own update it also reads the state of the repository, like any Git tool. The
// office CLI passes no Git command on.
package sync
