// Package stage is the building block Stage (bb-stage in docs/architecture/05-building-blocks.arc42.md).
//
// It renders one web page per session from the session stream and provides if-stage-web. The
// page is shown in a meeting by screen share (dec-stage-screenshare).
//
// Addresses:
//
//	GET /stage/<escaped session>?token=<token>         the page
//	GET /stage/<escaped session>/events?token=<token>  server-sent events for the page
//
// How it works:
//
//   - The token must be a stage link token for this session (Identity creates the link). No
//     token, or an unknown or expired token, gives 401 with an error page. A participant token or
//     a link for another session gives 403. The error pages show no session data.
//   - The stage is only a view of the session log. For every open page it subscribes to the
//     session stream in-process, through the token check, with the token of the link. So it has
//     exactly the rights of the link. It builds its view from the events and has no state or
//     logic of its own.
//   - The page is rendered on the server with an empty view. A small inline script opens the
//     event stream. Every event of the stream is named after a region of the page (status,
//     participants, hands, artifact, minutes, utterances) and carries the inner HTML of that
//     region, rendered on the server. The script only replaces the region. The first message
//     sends every region; later messages send only regions that changed, so a shown artifact is
//     not reloaded when someone speaks. A reconnect starts from the beginning of the log.
//   - Participants: every participant with its name, "agent" for agents, and a capture status:
//     captured (joined with transcription), paused, not captured, or left.
//   - Raised hands: a hand stays raised until its participant speaks (the next utterance.final
//     of that participant), raises it again (the new topic replaces the old one), or leaves. All
//     hands go down when the session ends. There is no hand.lowered event; the rule is open in
//     #24.
//   - The shown artifact: the last artifact.shown. The stage loads it in-process from the artifact
//     tools and embeds the HTML document in a sandboxed iframe (srcdoc, no scripts). An unknown
//     ID or another media type gives a note instead.
//   - Minutes: the text of the last minutes.updated. Utterances: the last eight that are not
//     retracted, with corrections applied. A retracted utterance disappears.
//   - After session.ended the status shows that the session has ended, and the stream sends "end"
//     and closes. When the link expires, an open stream shows that and also sends "end".
//   - No external assets, no frontend build. A Content-Security-Policy allows only the inline
//     styles, the one inline script (by hash) and the event stream of the same origin.
package stage
