package stage

import (
	"bytes"
	"context"
	"html/template"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
)

// Regions of the page. Each region is an element with this ID. The event stream sends a region
// again only when its HTML changed.
var regions = []string{"status", "participants", "hands", "artifact", "minutes", "utterances"}

// recentUtterances is how many utterances the stage shows.
const recentUtterances = 8

// view is what the stage knows about one session. It is built from the session stream only.
type view struct {
	participants []*participantView // in the order of their first join
	byID         map[string]*participantView
	hands        []handView // in the order they were raised
	artifact     artifactView
	minutes      string
	utterances   []*utteranceView // in log order
	byEvent      map[uint32]*utteranceView
	ended        bool
	live         bool   // the stream delivered its first events or is caught up
	stopped      string // why the live view stopped, if it did
}

type participantView struct {
	ID, Name string
	Agent    bool
	captured bool
	paused   bool
	left     bool
}

// Status is one of "captured", "paused", "not-captured" and "left".
func (p *participantView) Status() string {
	switch {
	case p.left:
		return "left"
	case p.paused:
		return "paused"
	case p.captured:
		return "captured"
	}
	return "not-captured"
}

// StatusText is the status as the audience reads it.
func (p *participantView) StatusText() string { return strings.ReplaceAll(p.Status(), "-", " ") }

type handView struct {
	Participant *participantView
	Topic       string
}

type artifactView struct {
	ID        string
	MediaType string
	Content   string // the HTML document, embedded in a sandboxed iframe
	Note      string // why the artifact cannot be shown
}

type utteranceView struct {
	EventID   uint32
	Speaker   *participantView
	Text      string
	Corrected bool
	retracted bool
}

func newView() *view {
	return &view{byID: map[string]*participantView{}, byEvent: map[uint32]*utteranceView{}}
}

// participant returns the participant with this ID. It adds a placeholder for an ID that has not
// joined (the session only accepts events of joined participants, so this is defensive).
func (v *view) participant(id string) *participantView {
	if p := v.byID[id]; p != nil {
		return p
	}
	p := &participantView{ID: id, Name: id}
	v.byID[id] = p
	v.participants = append(v.participants, p)
	return p
}

func (v *view) lowerHand(id string) {
	v.hands = slices.DeleteFunc(v.hands, func(h handView) bool { return h.Participant.ID == id })
}

// apply updates the view with one event. load fetches a shown artifact.
func (v *view) apply(ctx context.Context, ev *officev1.Event, load func(context.Context, string) (*officev1.Artifact, error)) {
	switch p := ev.GetPayload().(type) {
	case *officev1.Event_ParticipantJoined:
		j := p.ParticipantJoined
		pt := v.participant(j.GetParticipantId())
		if j.GetDisplayName() != "" {
			pt.Name = j.GetDisplayName()
		}
		pt.Agent = j.GetKind() == officev1.ParticipantKind_PARTICIPANT_KIND_AGENT
		pt.captured, pt.paused, pt.left = j.GetCaptured(), false, false
	case *officev1.Event_ParticipantLeft:
		v.participant(p.ParticipantLeft.GetParticipantId()).left = true
		v.lowerHand(p.ParticipantLeft.GetParticipantId())
	case *officev1.Event_ParticipantPaused:
		v.participant(p.ParticipantPaused.GetParticipantId()).paused = true
	case *officev1.Event_ParticipantResumed:
		v.participant(p.ParticipantResumed.GetParticipantId()).paused = false
	case *officev1.Event_UtteranceFinal:
		u := &utteranceView{EventID: ev.GetId(), Speaker: v.participant(p.UtteranceFinal.GetParticipantId()),
			Text: p.UtteranceFinal.GetText()}
		v.utterances = append(v.utterances, u)
		v.byEvent[ev.GetId()] = u
		// A raised hand is lowered when its participant speaks: they had the floor.
		v.lowerHand(u.Speaker.ID)
	case *officev1.Event_UtteranceRetracted:
		if u := v.byEvent[p.UtteranceRetracted.GetUtteranceEventId()]; u != nil {
			u.retracted = true
		}
	case *officev1.Event_UtteranceCorrected:
		if u := v.byEvent[p.UtteranceCorrected.GetUtteranceEventId()]; u != nil {
			u.Text, u.Corrected = p.UtteranceCorrected.GetText(), true
		}
	case *officev1.Event_HandRaised:
		pt := v.participant(p.HandRaised.GetParticipantId())
		v.lowerHand(pt.ID)
		v.hands = append(v.hands, handView{Participant: pt, Topic: p.HandRaised.GetTopic()})
	case *officev1.Event_MinutesUpdated:
		v.minutes = p.MinutesUpdated.GetText()
	case *officev1.Event_ArtifactShown:
		v.artifact = loadArtifact(ctx, p.ArtifactShown.GetArtifactId(), load)
	case *officev1.Event_SessionEnded:
		v.ended = true
		v.hands = nil
	}
}

func loadArtifact(ctx context.Context, id string, load func(context.Context, string) (*officev1.Artifact, error)) artifactView {
	av := artifactView{ID: id}
	if load == nil {
		av.Note = "The stage cannot load artifacts."
		return av
	}
	a, err := load(ctx, id)
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound:
		av.Note = "There is no artifact " + id + "."
	case err != nil:
		av.Note = "The artifact " + id + " could not be loaded."
	case !strings.HasPrefix(a.GetMediaType(), "text/html"):
		av.MediaType = a.GetMediaType()
		av.Note = "The stage cannot show artifacts of type " + a.GetMediaType() + "."
	default:
		av.MediaType = a.GetMediaType()
		av.Content = string(a.GetContent())
	}
	return av
}

// Status is the state of the session: "connecting", "live", "ended" or "stopped".
func (v *view) Status() string {
	switch {
	case v.ended:
		return "ended"
	case v.stopped != "":
		return "stopped"
	case v.live:
		return "live"
	}
	return "connecting"
}

func (v *view) StatusText() string {
	switch v.Status() {
	case "ended":
		return "The session has ended."
	case "stopped":
		return v.stopped
	case "live":
		return "Live"
	}
	return "Connecting …"
}

func (v *view) Participants() []*participantView { return v.participants }
func (v *view) Hands() []handView                { return v.hands }
func (v *view) Artifact() artifactView           { return v.artifact }
func (v *view) Minutes() string                  { return v.minutes }

// Utterances returns the most recent utterances that are not retracted, oldest first.
func (v *view) Utterances() []*utteranceView {
	var out []*utteranceView
	for i := len(v.utterances) - 1; i >= 0 && len(out) < recentUtterances; i-- {
		if !v.utterances[i].retracted {
			out = append(out, v.utterances[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// render returns the inner HTML of a region.
func (v *view) render(region string) string {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, region, v); err != nil {
		return template.HTMLEscapeString("render " + region + ": " + err.Error())
	}
	return strings.TrimSpace(b.String())
}

// renderAll returns the inner HTML of every region.
func (v *view) renderAll() map[string]template.HTML {
	out := map[string]template.HTML{}
	for _, r := range regions {
		out[r] = template.HTML(v.render(r)) // rendered by html/template above
	}
	return out
}

var templates = template.Must(template.New("stage").Parse(`
{{define "status"}}<span data-status="{{.Status}}">{{.StatusText}}</span>{{end}}

{{define "name"}}<span class="name">{{.Name}}</span>{{if .Agent}} <span class="badge agent" data-kind="agent">agent</span>{{end}}{{end}}

{{define "participants"}}<h2>Participants</h2>
{{if .Participants}}<ul class="participants">
{{range .Participants}}<li data-participant="{{.ID}}" data-kind="{{if .Agent}}agent{{else}}human{{end}}" data-capture="{{.Status}}">{{template "name" .}} <span class="capture {{.Status}}">{{.StatusText}}</span></li>
{{end}}</ul>{{else}}<p class="empty">Nobody has joined yet.</p>{{end}}{{end}}

{{define "hands"}}<h2>Raised hands</h2>
{{if .Hands}}<ol class="hands">
{{range .Hands}}<li data-participant="{{.Participant.ID}}">✋ {{template "name" .Participant}}{{if .Topic}}: <span class="topic">{{.Topic}}</span>{{end}}</li>
{{end}}</ol>{{else}}<p class="empty">No raised hands.</p>{{end}}{{end}}

{{define "artifact"}}{{with .Artifact}}{{if .Content}}<iframe title="{{.ID}}" data-artifact="{{.ID}}" sandbox="" referrerpolicy="no-referrer" srcdoc="{{.Content}}"></iframe>
{{else if .ID}}<p class="note" data-artifact="{{.ID}}">{{.Note}}</p>
{{else}}<p class="empty">Nothing is shown.</p>{{end}}{{end}}{{end}}

{{define "minutes"}}<h2>Minutes</h2>
{{if .Minutes}}<div class="minutes-text">{{.Minutes}}</div>{{else}}<p class="empty">No minutes yet.</p>{{end}}{{end}}

{{define "utterances"}}<h2>Recent contributions</h2>
{{if .Utterances}}<ol class="utterances">
{{range .Utterances}}<li data-utterance="{{.EventID}}" data-participant="{{.Speaker.ID}}">{{template "name" .Speaker}}: <span class="text">{{.Text}}</span>{{if .Corrected}} <span class="corrected">(corrected)</span>{{end}}</li>
{{end}}</ol>{{else}}<p class="empty">Nothing said yet.</p>{{end}}{{end}}
`))
