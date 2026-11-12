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
//   - Storage: every deck is two files in the archive (if-archive-storage), written in one
//     commit: slides/NNNNNN.yaml holds the input (id, title, slides, author, author kind, time,
//     command ID) with the field names of the contract (snake_case, enums by name, times in RFC
//     3339; a text with line breaks is a literal block), and slides/NNNNNN.html holds the
//     rendered document. NNNNNN is n with at least six digits. RenderDeck returns only after the
//     commit.
//   - The output is stored, not rendered again on read. So a deck stays exactly what was shown
//     in a meeting, even if a later hub renders differently, and a reader of the archive can open
//     the HTML without the hub. Because rendering is deterministic, the input alone is enough to
//     get the same document from the same hub version.
//   - Open reads all decks from the current commit of the archive and keeps them in memory, so
//     decks survive a restart. Reads use only the memory and do not wait for a commit. A file
//     that is not valid, or a YAML file without its HTML file, stops the start with an error;
//     other files are ignored.
//   - Every render is idempotent by its command_id (dec-idempotent-commands). A repeated request
//     returns the stored deck with duplicate set. The same command_id with another title, other
//     slides or another author fails with AlreadyExists. A new command_id with the same deck
//     gives a new deck with a new ID and the same content.
//   - Every client call needs the caller from the identity token check (if-token-check).
//     author_participant_id must name the participant of the token, or the call fails with
//     PermissionDenied. The hub sets author_kind from the token. Any participant can fetch
//     every deck. A stage link cannot use the slides interface (PermissionDenied): the stage
//     renders on the hub and loads decks through Service.Load, so the browser never needs them.
//   - One write at a time: a render holds the write lock while its commit runs. The commit waits
//     for a session write that the archive is carrying out (#45).
//   - Error codes: a missing or bad field, or a rendered deck larger than MaxContentBytes, is
//     InvalidArgument; an unknown ID is NotFound; a participant that is not the caller, or a stage
//     link, is PermissionDenied; no valid token is Unauthenticated; a reused command_id is
//     AlreadyExists; a failed write to the archive is Internal, and the client can repeat the
//     command.
//   - The meeting session does not check that a shown artifact exists (session.FocusArtifact). The
//     stage handles an ID that Load does not know.
package slides
