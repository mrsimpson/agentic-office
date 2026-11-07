// Package tasks is the building block Tasks (bb-tasks in
// docs/architecture/05-building-blocks.arc42.md). It realizes the task interface of the hub
// (if-task-api, TaskService in package api).
//
// For now every task is a local draft (dec-yaml-git-storage). A later sync with the task tool of
// the team replaces the draft (#40); the contract already has an opaque ref and a source for it.
//
// How it works:
//
//   - Every task has a local number that grows by one and starts at 1. Its ref is "local:<n>",
//     and its source is TASK_SOURCE_LOCAL. Clients treat the ref as opaque. GetTask and
//     UpdateTask answer NotFound for every ref that is not a local task, also for other schemes.
//   - Storage: every task is one YAML file in the archive (if-archive-storage),
//     tasks/NNNNNN.yaml, where NNNNNN is the number with at least six digits. The YAML has the
//     field names of the contract (snake_case, enums by name, times in RFC 3339), and the extra
//     field command_ids: the command IDs of the creation and of every update, oldest first. A
//     text with line breaks is a literal block. Creating a task is one commit; every change
//     rewrites the file in a new commit, so Git keeps the history of a task. A command returns
//     only after its commit.
//   - Open reads all tasks from the current commit of the archive and keeps them in memory, so
//     tasks survive a restart. Reads use only the memory. A file that is not valid stops the
//     start with an error; files without the ending .yaml are ignored.
//   - Every call needs the caller from the identity token check (if-token-check).
//     author_participant_id (create) and participant_id (update) must name the participant of
//     the token, or the call fails with PermissionDenied. The hub sets author_kind from the
//     token. A stage link cannot read or write tasks. Any participant may read and change every
//     task; rights by role are open (#14, #15, #22).
//   - assignee_participant_id and session_id are not checked.
//   - Writes are idempotent by command_id (dec-idempotent-commands). All command IDs of the
//     tasks are one set. A repeated create returns the stored task with duplicate set; the same
//     command_id with another author, or with other content while the task is unchanged
//     (revision 1), fails with AlreadyExists. A repeated update returns the
//     current task with duplicate set and changes nothing; its content is not compared. A
//     command_id of another task, or of a create, fails with AlreadyExists.
//   - An update that changes no value creates no commit and keeps the revision.
//   - ListTasks returns the tasks that match all filters, oldest first, at most limit of them: 0
//     means DefaultLimit (100), and more than MaxLimit (1000) is reduced to MaxLimit.
//     next_page_token is set if more tasks match.
//   - One write at a time: a write holds the lock of the tasks while its commit runs. The commit
//     waits for a session write that the archive is carrying out (#45).
//   - Error codes: a missing or bad field is InvalidArgument; a participant that is not the
//     caller, or a stage link, is PermissionDenied; no valid token is Unauthenticated; a reused
//     command_id is AlreadyExists; an unknown ref is NotFound; a failed write to the archive is
//     Internal, and the client can repeat the command.
package tasks
