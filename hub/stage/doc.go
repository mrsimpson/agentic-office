// Package stage is the building block Stage (bb-stage in docs/architecture/05-building-blocks.arc42.md).
//
// It renders one web page per session from the session stream. It realizes if-stage-web, whose
// contract is package web: the addresses, the page, its regions and the event stream. The page is
// shown in a meeting by screen share (dec-stage-screenshare).
//
// How it works:
//
//   - The token must be a stage link token for this session (Identity creates the link). No
//     token, or an unknown or expired token, gives 401 with an error page. A participant token or
//     a link for another session gives 403. The error pages show no session data.
//   - The stage is a view of everything that happens inside a session. For every open page it
//     subscribes to the session stream in-process, through the token check, with the token of
//     the link. So it has exactly the rights of the link. It builds its view from the events and
//     from the staging area of its own session, and has no state or logic of its own.
//   - The staging area: the stage lists the files that the session staged as artifacts next to
//     those that the log names, whichever part wrote them. It reads the area again when the
//     staging repository changes (if-archive-watch). A deck file "slides/000042.yaml" is the
//     deck "slides/42"; the stored minutes are not listed because the minutes are the first tab
//     already. The session comes from the stage link and from nothing else: the stage reads no
//     staging area of another session.
//   - The page is rendered on the server with an empty view. A small inline script opens the
//     event stream. Every event of the stream is named after a region of the page (status,
//     origin, participants, hands, artifact, minutes, utterances) and carries the inner HTML of that
//     region, rendered on the server. The script only replaces the region. The first message
//     sends every region; later messages send only regions that changed, so a shown artifact is
//     not reloaded when someone speaks. A reconnect starts from the beginning of the log.
//   - Imported sessions (dec-session-import, risk-imported-conversation): when the log starts with
//     session.imported, the origin region names the source, the title and the participant who
//     imported it, says that the office did not see the conversation happen, and says while the
//     import is not complete. The status says "Imported" instead of "Live". A live session has an
//     empty origin region.
//   - Participants: every participant with its name, "agent" for agents, and a capture status:
//     imported (a speaker of an imported conversation), captured (joined with transcription),
//     paused, not captured, or left.
//   - Raised hands: a hand stays raised until its participant speaks (the next utterance.final
//     of that participant), raises it again (the new topic replaces the old one), or leaves. All
//     hands go down when the session ends. There is no hand.lowered event; the rule is open in
//     #24.
//   - The shown artifact: the last artifact.focused. The part of its ID before the first slash
//     names its type. The stage loads a deck ("slides/<n>") in-process from the slides interface
//     and embeds the HTML document in a sandboxed iframe (srcdoc, no scripts). An unknown deck, an
//     ID of another type or another media type gives a note instead.
//   - Staged artifacts (concept-session-release): the stage reads as the stage of its session. A
//     staged file or deck of its own session shows its content, read from the staging area; a
//     staged file of another session shows its stub, and a staged deck of another session a note.
//   - Minutes: the text of the last minutes.updated. Utterances: the last 500 that are not
//     off the record, with corrections applied, in a transcript that scrolls. An imported utterance shows its original time
//     (UTC). A off the record utterance disappears.
//   - Activities (dec-agent-activity): next to the stream of the log, the stage watches the
//     activities of the participants (if-session-activity) with the same token: "working",
//     "working: <tool>", "waiting", or "answered only in its own chat" when an agent ended its
//     turn with a reply that nobody heard. They are not part of the log. If the watch fails, the
//     stage shows the session without them.
//   - After session.ended the status shows that the session has ended, and the stream sends "end"
//     and closes. When the link expires, an open stream shows that and also sends "end".
//   - The stage link may carry an address in the parameter "portal", chosen by the client that
//     created it. If it is a path on this server (one slash at the start, no backslash, no
//     control character), the header shows it as the plain link "Open in portal". Any other value
//     shows no link. The link carries no token and sends no referrer. The stage knows nothing of
//     the portal; the portal asks for its own sign-in.
//   - Static files (dec-ui-base): the page links two style sheets of the same origin, the base of
//     the design system and its own, at /stage/assets/<name>. They need no token because they
//     hold no session data. The name of a style sheet holds a hash of its content, so the answer is
//     cached for a year and a changed file has a new address. An unknown name answers 404. The
//     notice of the third-party licences is served there too.
//   - No external assets, no frontend build. A Content-Security-Policy allows style sheets of the
//     same origin, inline styles (artifacts in a frame bring their own), the one inline script (by
//     hash) and the event stream of the same origin.
package stage
