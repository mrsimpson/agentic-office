package stage

// Fixtures of the dev preview: the views of fictional sessions, built from events through the
// real view code, so a fixture shows what the stage shows for such a log. Plain Go data; no file
// is read.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	sessionactivityv1 "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

// previewSession is one fixture page: a session and what its page is about.
type previewSession struct {
	name, title, session string
	// portal is the address of the portal page of the session, which the stage links to.
	portal string
	view   *view
}

// previewError is one fixture of the error page.
type previewError struct {
	name, title, heading, message string
	status                        int
}

func previewErrors() []previewError {
	return []previewError{
		{"stage-error.html", "Error page: the stage link is unknown or expired", "Stage link not valid",
			"This stage link is unknown or has expired. Ask a participant for a new link: office stage link.", http.StatusUnauthorized},
		{"stage-error-other-session.html", "Error page: the stage link is for another session", "Stage link for another session",
			"This stage link is for another session.", http.StatusForbidden},
	}
}

// fixtureLog builds the view of a session from events, 20 seconds apart.
type fixtureLog struct {
	v  *view
	id uint32
	at time.Time
}

func newFixtureLog() *fixtureLog {
	return &fixtureLog{v: newView(), at: time.Date(2026, 11, 27, 9, 30, 0, 0, time.UTC)}
}

func (l *fixtureLog) add(actor string, set func(*sessionstreamv1.Event)) {
	l.id++
	l.at = l.at.Add(20 * time.Second)
	ev := &sessionstreamv1.Event{Id: l.id, Time: timestamppb.New(l.at), ActorParticipantId: actor}
	set(ev)
	l.v.apply(ev)
}

func (l *fixtureLog) join(id, name string, agent, captured bool) {
	kind := sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN
	if agent {
		kind = sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT
	}
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ParticipantJoined{ParticipantJoined: &sessionstreamv1.ParticipantJoined{
			ParticipantId: id, DisplayName: name, Kind: kind, Captured: captured}}
	})
}

func (l *fixtureLog) joinImported(id, name string) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ParticipantJoined{ParticipantJoined: &sessionstreamv1.ParticipantJoined{
			ParticipantId: id, DisplayName: name, Kind: sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, Imported: true}}
	})
}

func (l *fixtureLog) pause(id string) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ParticipantPaused{ParticipantPaused: &sessionstreamv1.ParticipantPaused{ParticipantId: id}}
	})
}

func (l *fixtureLog) leave(id string) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ParticipantLeft{ParticipantLeft: &sessionstreamv1.ParticipantLeft{ParticipantId: id}}
	})
}

func (l *fixtureLog) say(id, text string) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_UtteranceFinal{UtteranceFinal: &sessionstreamv1.UtteranceFinal{ParticipantId: id, Text: text}}
	})
}

func (l *fixtureLog) sayImported(id, text string, original time.Time) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_UtteranceFinal{UtteranceFinal: &sessionstreamv1.UtteranceFinal{
			ParticipantId: id, Text: text, OriginalTime: timestamppb.New(original)}}
	})
}

func (l *fixtureLog) hand(id, topic string) {
	l.add(id, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_HandRaised{HandRaised: &sessionstreamv1.HandRaised{ParticipantId: id, Topic: topic}}
	})
}

func (l *fixtureLog) minutes(actor, text string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_MinutesUpdated{MinutesUpdated: &sessionstreamv1.MinutesUpdated{Text: text}}
	})
}

func (l *fixtureLog) created(actor, path string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ArtifactCreated{ArtifactCreated: &sessionstreamv1.ArtifactCreated{Path: path, Version: "v1"}}
	})
}

func (l *fixtureLog) focus(actor, path string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ArtifactFocused{ArtifactFocused: &sessionstreamv1.ArtifactFocused{ArtifactId: path}}
	})
}

func (l *fixtureLog) imported(actor, source, title string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_SessionImported{SessionImported: &sessionstreamv1.SessionImported{Source: source, Title: title}}
	})
}

func (l *fixtureLog) importDone(actor string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_ImportCompleted{ImportCompleted: &sessionstreamv1.ImportCompleted{}}
	})
}

func (l *fixtureLog) end(actor string) {
	l.add(actor, func(e *sessionstreamv1.Event) {
		e.Payload = &sessionstreamv1.Event_SessionEnded{SessionEnded: &sessionstreamv1.SessionEnded{}}
	})
}

// live marks the view as caught up with its stream, as the stage does after the first events.
func (l *fixtureLog) live() *view {
	l.v.live = true
	return l.v
}

func working(id, tool string) *sessionactivityv1.Activity {
	return &sessionactivityv1.Activity{ParticipantId: id, State: sessionactivityv1.ActivityState_ACTIVITY_STATE_WORKING, Tool: tool}
}

func waiting(id string) *sessionactivityv1.Activity {
	return &sessionactivityv1.Activity{ParticipantId: id, State: sessionactivityv1.ActivityState_ACTIVITY_STATE_IDLE}
}

func unspoken(id string) *sessionactivityv1.Activity {
	return &sessionactivityv1.Activity{ParticipantId: id, State: sessionactivityv1.ActivityState_ACTIVITY_STATE_IDLE, UnspokenReply: true}
}

const longName = "Grace Brewster Murray Hopper-von Neumann of the Alexandria Institute for Applied Computation"

const longPath = "documents/architecture/decision-records/2026-q4-long-running-migration-plan-for-the-reporting-database-and-its-consumers.md"

const fixtureMinutes = `# Weekly planning

## Decisions

- Ship the **stage redesign** behind a preview first.
- Move the reporting database in two steps; the first step needs no downtime.

## Open points

1. Who reviews the migration plan? (Grace, until Friday)
2. Which tenants get the new wiki home first?

| Topic | Owner | Due |
|---|---|---|
| Migration plan | Grace | 2026-12-04 |
| Wiki home | Ada | 2026-12-11 |

A line of code: ` + "`office stage link`" + `. A link to [the plan](https://example.org/plan).
`

// fixtureDocument wraps the body in the document that a sandboxed frame shows.
func fixtureDocument(body string) string {
	return "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><style>" + paperCSS + "</style></head><body>" + body + "</body></html>"
}

func previewSessions() []previewSession {
	return []previewSession{
		fixtureLive(), fixtureLiveSlides(), fixtureUnspoken(), fixtureEmpty(), fixtureImported(), fixtureImporting(),
		fixtureEnded(), fixtureStopped(),
	}
}

// fixtureLive is a busy session: many tabs with long names, long participant names, raised hands,
// working and waiting agents, and a long transcript.
func fixtureLive() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	l.join("grace", longName, false, true)
	l.join("linus", "Linus", false, true)
	l.pause("linus")
	l.join("margaret", "Margaret Hamilton", false, false)
	l.join("alan", "Alan Turing", false, true)
	l.leave("alan")
	l.join("minutes-keeper", "Minutes keeper", true, false)
	l.join("slides-agent", "Slides agent", true, false)
	l.join("researcher", "Researcher with a very long agent name that must not break the header", true, false)
	l.join("kai", "Kai", false, true)
	l.join("noor", "Noor Al-Sayed", false, true)
	tabs := []string{longPath, "notes/000001.md", "notes/000002.md", "decisions/000001.yaml", "decisions/000002.yaml",
		"documents/roadmap.md", "documents/glossary.md"}
	for i := 1; i <= 8; i++ {
		tabs = append(tabs, fmt.Sprintf("slides/%d", i))
	}
	for _, p := range tabs {
		l.created("minutes-keeper", p)
	}
	l.minutes("minutes-keeper", fixtureMinutes)
	for i := 0; i < 36; i++ {
		who := []string{"ada", "grace", "kai", "noor", "margaret"}[i%5]
		text := fmt.Sprintf("Point %d: we agree on the next step.", i+1)
		if i%7 == 3 {
			text = strings.Repeat("A long sentence that goes on and on to check how the transcript wraps. ", 4)
		}
		l.say(who, text)
	}
	l.hand("grace", "The migration plan needs a second reviewer, and I would like to say why before we decide anything today")
	l.hand("margaret", "")
	l.focus("slides-agent", longPath)
	l.v.setActivities([]*sessionactivityv1.Activity{working("minutes-keeper", "write_minutes"), waiting("slides-agent"), working("researcher", "")})
	l.v.artifact = artifactView{ID: longPath, Kind: "document", MediaType: "text/html", Content: fixtureDocument(
		"<h1>Migration plan</h1><p>The reporting database moves in two steps.</p><h2>Step one</h2><ul><li>Copy the data.</li><li>Switch the readers.</li></ul><pre><code>office hub add</code></pre>")}
	return previewSession{name: "stage-live.html", title: "Live session: many tabs, long names, raised hands, working agents",
		session: "weekly-planning", portal: "/portal/t/acme/s/weekly-planning", view: l.live()}
}

// fixtureLiveSlides shows the minutes as the shared focus and a note for an artifact the stage cannot show.
func fixtureLiveSlides() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	l.join("slides-agent", "Slides agent", true, false)
	l.created("slides-agent", "slides/1")
	l.created("slides-agent", "data/report.csv")
	l.focus("slides-agent", "data/report.csv")
	l.say("ada", "Let us look at the report.")
	l.v.artifact = artifactView{ID: "data/report.csv", Note: "The stage cannot show the artifact data/report.csv."}
	return previewSession{name: "stage-live-note.html", title: "Live session: the shared artifact cannot be shown",
		session: "report-review", view: l.live()}
}

// fixtureUnspoken has an agent that answered only in its own chat (the state unspoken).
func fixtureUnspoken() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	l.join("helper", "Helper agent", true, false)
	l.join("scribe", "Scribe agent", true, false)
	l.say("ada", "Helper, can you check the release notes?")
	l.minutes("scribe", "# Release notes\n\nAda asked the helper to check them.")
	l.v.setActivities([]*sessionactivityv1.Activity{unspoken("helper"), working("scribe", "write_minutes")})
	return previewSession{name: "stage-agent-unspoken.html", title: "Agent that answered only in its own chat (unspoken)",
		session: "release-check", view: l.live()}
}

// fixtureEmpty is a session that waits for the minutes: nobody wrote them, nobody spoke.
func fixtureEmpty() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	return previewSession{name: "stage-empty.html", title: "Empty session: waiting for the minutes",
		session: "just-started", view: l.live()}
}

// fixtureImported is a conversation that was imported: the origin region says so.
func fixtureImported() previewSession {
	l := newFixtureLog()
	l.imported("ada", "teams", "Customer interview, 22 November")
	l.joinImported("customer", "Customer contact")
	l.joinImported("interviewer", "Interviewer")
	at := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	l.sayImported("interviewer", "Thank you for your time. How do you use the portal today?", at)
	l.sayImported("customer", "Mostly to check the minutes after a meeting.", at.Add(time.Minute))
	l.importDone("ada")
	l.minutes("ada", "# Customer interview\n\nThe customer reads the minutes after the meeting.")
	return previewSession{name: "stage-imported.html", title: "Imported session",
		session: "customer-interview", portal: "/portal/t/acme/s/customer-interview", view: l.live()}
}

// fixtureImporting is an import that has not finished.
func fixtureImporting() previewSession {
	l := newFixtureLog()
	l.imported("ada", "zoom", "")
	l.joinImported("customer", "Customer contact")
	l.sayImported("customer", "Hello?", time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC))
	return previewSession{name: "stage-importing.html", title: "Import that is not complete yet",
		session: "import-running", view: l.live()}
}

// fixtureEnded is a session that has ended.
func fixtureEnded() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	l.join("minutes-keeper", "Minutes keeper", true, false)
	l.created("minutes-keeper", "notes/000001.md")
	l.created("minutes-keeper", "decisions/000001.yaml")
	l.say("ada", "That is all for today.")
	l.minutes("minutes-keeper", fixtureMinutes)
	l.hand("ada", "One more thing")
	l.end("ada")
	return previewSession{name: "stage-ended.html", title: "Ended session",
		session: "weekly-planning-ended", portal: "/portal/t/acme/s/weekly-planning-ended", view: l.live()}
}

// fixtureStopped is a view whose stream stopped because the link expired.
func fixtureStopped() previewSession {
	l := newFixtureLog()
	l.join("ada", "Ada Lovelace", false, true)
	l.say("ada", "The link expires now.")
	v := l.live()
	v.stopped = "The stage link has expired. Ask a participant for a new link."
	return previewSession{name: "stage-stopped.html", title: "Stream stopped: the link expired", session: "link-expired", view: v}
}
