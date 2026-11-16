// Package tasks is the building block Tasks (bb-tasks in
// docs/architecture/05-building-blocks.arc42.md). It realizes the task interface of the hub
// (if-task-api, TaskService in package api).
//
// For now every task is a local draft (dec-yaml-git-storage). A later sync with the task tool of
// the team replaces the draft (#40); the contract already has an opaque ref and a source for it.
//
// How it works:
//
//   - Every task has a local number that starts at 1 and grows by one for every new task. Its ref is "local:<n>",
//     and its source is TASK_SOURCE_LOCAL. Clients treat the ref as opaque. GetTask and
//     UpdateTask answer NotFound for every ref that is not a local task, also for other schemes.
//   - Storage: every task is one YAML file in the archive (if-archive-storage),
//     tasks/NNNNNN.yaml, where NNNNNN is the number with at least six digits. The YAML starts
//     with the field schema (office.task.v1), has the field names of the contract (snake_case,
//     enums by name, times in RFC 3339), and the extra field command_ids: the command IDs of the creation and of every update, oldest first. A
//     text with line breaks is a literal block. Creating a task is one commit; every change
//     rewrites the file in a new commit, so Git keeps the history of a task. A command returns
//     only after its commit. The check of the part (package check, if-tasks-check) decides what
//     a valid file is. The part uses it for every file it writes and reads, and Git access uses
//     it for a push, so every rule exists once.
//   - Open reads all tasks from the current commit of the archive and keeps them in memory, so
//     tasks survive a restart. Reads use only the memory. A file that is not valid stops the
//     start with an error; files without the ending .yaml are ignored.
//   - Reload (if-archive-watch): the part watches the archive of its tenant and reloads the
//     files of its directory that a push or a commit changed, created or removed. It reads
//     them from the archive again, checks each with the check of the part and keeps serving reads
//     from memory. A file that is not valid after a push is logged once for its version and
//     ignored: the part keeps what it had for that path. (The hook of Git access refuses such
//     files, so this is a defence.) A file that is gone is removed from memory. If the archive
//     drops the watcher because the part was too slow, the part watches again and reads all its
//     files. A file that bypasses the hook and is not valid still counts for the next number, so
//     the part never takes its number.
//   - Numbers: the next number is one above the highest number ever seen, in a file, in memory or
//     in the names that ever lay in the directory in the history of main (Names of
//     if-archive-storage; the part asks at start and when it reads all files again). A number is
//     never used twice, also after a push removed its file and the hub restarted. The numbers can have gaps, because a push may bring any number. A part creates its file with "must not
//     exist yet" (storage.NoFile). If a push put a file there first, the create fails inside the
//     archive; the part reads that file, takes the next free number and tries again, so the
//     caller sees no error.
//     An update reads the task file from the archive and writes the changed file with the version
//     that it read as Base. If a push changed the file in between, the write fails inside the
//     archive; the part reads the file again, applies the fields of the request to the new state
//     and writes again, up to a fixed number of tries. The caller sees no error, and a field that
//     the push changed and the request does not set stays as pushed. If every try fails, the
//     update fails with Aborted and changes nothing.
//     Close stops the part from following the archive.
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
//     does not wait behind session writes: the session logs lie in their own repository.
//   - Error codes: a missing or bad field is InvalidArgument; a participant that is not the
//     caller, or a stage link, is PermissionDenied; no valid token is Unauthenticated; a reused
//     command_id is AlreadyExists; an unknown ref is NotFound; a failed write to the archive is
//     Internal, and the client can repeat the command; an update that a stream of pushes kept
//     from succeeding is Aborted, and the client can repeat it.
package tasks
