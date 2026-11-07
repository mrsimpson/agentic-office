// Package artifacts is the building block Artifact tools (bb-artifact-service in docs/architecture/05-building-blocks.arc42.md).
//
// It renders slides and stores them as artifacts. It provides if-artifact-api: ArtifactService for
// clients, and Service.Load for other building blocks of the server (the stage loads the shown
// artifact with it). Business plan checks are not built yet (#3).
//
// How it works:
//
//   - RenderSlides takes a deck (an optional title and at least one slide; every slide needs a
//     title, bullets and notes are optional) and renders it with RenderSlides into one HTML
//     document (SlidesMediaType). The service stores the document as a new artifact with the ID
//     "slides/<n>". n starts at 1 and grows by one with every artifact. IDs are never used twice.
//   - The rendering is deterministic (con-no-intelligence): the same title and slides give the
//     same bytes, also on another server. The document holds nothing but the deck: no time, no
//     random value, no server address. Text is plain text; Markdown inside a bullet is shown as
//     it is. Leading and trailing white space is removed.
//   - The document is self-contained and meant for a screen share: one section per slide, each at
//     least as high as the window, black text on white, large fonts that scale with the width.
//     A deck title becomes a title slide (id "title"); slide n has the id "slide-n", so a fragment
//     like "#slide-2" shows that slide. Notes are in the document but hidden (speaker notes). The
//     document has no scripts and loads nothing: all text is HTML-escaped, and a
//     Content-Security-Policy allows only the inline style sheet.
//   - Every render is idempotent by its command_id (dec-idempotent-commands). A repeated request
//     returns the stored artifact with duplicate set. The same command_id with another deck or
//     author fails with AlreadyExists. A new command_id with the same deck gives a new artifact
//     with a new ID and the same content.
//   - Every client call needs a caller from the Authorizer (the identity token check). The author
//     is the participant of the token: author_participant_id must name it, or the call fails with
//     PermissionDenied. Any participant can fetch every artifact. A stage link cannot use the
//     artifact API (PermissionDenied): the stage renders on the server and loads artifacts
//     through Service.Load, so the browser never needs them.
//   - Storage is behind the Store interface. MemoryStore keeps artifacts in memory. FileStore also
//     appends every artifact to a file, one JSON object per line with the command ID, the input
//     and the artifact in the JSON form of the API. The office hub uses a FileStore in its data
//     directory. We chose this because the session logs name artifacts by ID, so IDs must not
//     start again at 1 after a restart, and it is the same simple form as the journal. A later
//     store can import the file with no conversion.
//   - Error codes: a missing or bad field, or a rendered deck larger than MaxContentBytes, is
//     InvalidArgument; an unknown ID is NotFound; a participant that is not the caller, or a stage
//     link, is PermissionDenied; no valid token is Unauthenticated; a reused command_id is
//     AlreadyExists.
//   - The meeting session does not check that a shown artifact exists (session.ShowArtifact). The
//     stage must handle an ID that Load does not know.
package artifacts
