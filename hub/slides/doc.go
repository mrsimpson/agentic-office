// Package slides is the building block Slides (bb-slides in
// docs/architecture/05-building-blocks.arc42.md). It realizes the slides interface of the hub
// (if-slides-api, SlidesService in package api) and gives other building blocks of the hub, such
// as the stage, the decks through Service.Load.
//
// How it works:
//
//   - RenderDeck takes a deck (an optional title and at least one slide; every slide needs a
//     title, bullets and notes are optional) and renders it with Render into one HTML document
//     (MediaType). The deck gets the ID "slides/<n>". n starts at 1 and grows by one with every
//     deck. IDs are never used twice. The stage shows a deck by this ID (artifact.focused).
//   - The rendering is deterministic (con-no-intelligence): the same title and slides give the
//     same bytes, also on another hub. The document holds nothing but the deck: no time, no
//     random value, no hub address. Text is plain text; Markdown inside a bullet is shown as it
//     is. Leading and trailing white space is removed in the document; the stored input keeps
//     the text as the client sent it.
//   - The document is self-contained and meant for a screen share: one section per slide, each at
//     least as high as the window, black text on white, large fonts that scale with the width.
//     A deck title becomes a title slide (id "title"); slide n has the id "slide-n", so a fragment
//     like "#slide-2" shows that slide. Notes are in the document but hidden (speaker notes). The
//     document has no scripts and loads nothing: all text is HTML-escaped, and a
//     Content-Security-Policy allows only the inline style sheet.
//   - Storage: every deck is one file in the archive (if-archive-storage), written in one
//     commit: slides/NNNNNN.yaml holds the input (schema, id, title, slides, author, author kind,
//     time, command ID, session). schema is office.slides.v1; the other field names are those of the
//     contract (snake_case, enums by name, times in RFC 3339; a text with line breaks is a
//     literal block). NNNNNN is n with at least six digits. The check of the part (package check,
//     if-slides-check) decides what a valid YAML file is. The part uses it for every file it
//     writes and reads, and Git access uses it for a push, so every rule exists once.
//     RenderDeck returns only after the commit.
//   - The rendered output is a runtime artifact. It is never committed, so no clone of the
//     artifact repository holds a rendered deck. The part keeps the output of every deck in
//     memory, next to the input, and the slides interface returns it. Open renders every deck
//     again from its stored input, so a deck has the same output after a restart. Because the
//     rendering is deterministic, the same hub version gives the same bytes. A later hub version
//     may render a deck differently; that is accepted.
//   - Staging (concept-session-release): a render that names a session needs a participant of the
//     session (if-session-release). While the session is not released, the deck goes into the
//     staging area of the session, and then the stub of a deck (check.Stub) at the same path into
//     the artifact repository through the area (storage.Staging.Stub), by the hub with
//     StubMessage. If the stub cannot be written, the
//     staged file is taken back and the area forgets the path. A released session, or no session,
//     writes into the artifact repository. The part asks the meeting session before it takes its lock.
//   - ChangeDeck and RemoveDeck change or remove a staged deck for a participant of its session
//     who names that session; the caller becomes the author of a change. A released deck, a stub
//     and a deck of another session are refused. While the release moves the area, a write is
//     unavailable. They are idempotent by command ID while the hub runs. RemoveDeck also discards a
//     staged deck that the release kept back because its path was taken
//     (storage.Staging.Discard), with the same check of the participant and the session. A render
//     that names a session whose release is done goes to the artifact repository.
//   - Reads: a staged deck shows its content to the readers of its session (the list of the area)
//     and to tenant admins (by role); everyone else gets its stub, a deck with only id and stub. A
//     removed staged deck is not found for a reader. Load serves the stage of a session: the staged
//     decks of that session, the stubs of the others.
//   - Open reads all decks from the current commit of the archive and from every staging area and
//     renders them, so decks survive a restart. A staged deck without a stub gets its stub; when a push took its path, the deck stays staged and the release keeps it back. Reads use only the memory and do not wait for a commit. A YAML file that
//     is not valid stops the start with an error; other files are ignored.
//   - Reload (if-archive-watch): the part watches the archive and the staging repository of its
//     tenant and reloads the files of its directory that a push or a commit changed, created or
//     removed. It reads
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
//     A reloaded deck is rendered again from its input. Close stops the part from following the
//     archive.
//   - Every render is idempotent by its command_id (dec-idempotent-commands). A repeated request
//     returns the stored deck with duplicate set. The same command_id with another title, other
//     slides or another author fails with AlreadyExists. A new command_id with the same deck
//     gives a new deck with a new ID and the same content.
//   - Every client call needs the caller from the identity token check (if-token-check).
//     author_participant_id must name the participant of the token, or the call fails with
//     PermissionDenied. The hub sets author_kind from the token. Any participant can fetch
//     every released deck. A stage link cannot use the slides interface (PermissionDenied): the stage
//     renders on the hub and loads decks through Service.Load, so the browser never needs them.
//   - One write at a time: a render holds the write lock while its commit runs. The commit
//     does not wait behind session writes: the session logs lie in their own repository.
//   - Error codes: a missing or bad field, or a rendered deck larger than MaxContentBytes, is
//     InvalidArgument; an unknown ID is NotFound; a participant that is not the caller, or a stage
//     link, is PermissionDenied; no valid token is Unauthenticated; a reused command_id is
//     AlreadyExists; a failed write to the archive is Internal, and the client can repeat the
//     command.
//   - The meeting session does not check that a shown artifact exists (session.FocusArtifact). The
//     stage handles an ID that Load does not know.
package slides
