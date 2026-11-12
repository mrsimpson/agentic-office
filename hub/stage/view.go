package stage

import (
	"bytes"
	"context"
	"html/template"
	"slices"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/hub/stage/web"

	"connectrpc.com/connect/v2"

	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
)

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
	imported     *importView // set when the log starts with session.imported
	live         bool        // the stream delivered its first events or is caught up
	stopped      string      // why the live view stopped, if it did
}

// importView says where an imported session comes from (dec-session-import).
type importView struct {
	Source, Title string
	By            string // the participant who imported the conversation
	Complete      bool
}

type participantView struct {
	ID, Name string
	Agent    bool
	imported bool
	captured bool
	paused   bool
	left     bool
}

// Status is one of "imported", "captured", "paused", "not-captured" and "left".
func (p *participantView) Status() string {
	switch {
	case p.imported:
		return "imported"
	case p.left:
		return "left"
	case p.paused:
		return "paused"
	case p.captured:
		return "captured"
	}
	return "not-captured"
}

// Initials are the first letters of the first two words of the name, for the avatar.
func (p *participantView) Initials() string {
	var out []rune
	for _, w := range strings.Fields(p.Name) {
		out = append(out, []rune(strings.ToUpper(w))[0])
		if len(out) == 2 {
			break
		}
	}
	return string(out)
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
	EventID      uint32
	Speaker      *participantView
	Text         string
	OriginalTime time.Time // when an imported utterance was said; zero for live ones
	Corrected    bool
	offTheRecord bool
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

// apply updates the view with one event. decks loads a shown deck.
func (v *view) apply(ctx context.Context, ev *sessionstreamv1.Event, decks Slides) {
	switch p := ev.GetPayload().(type) {
	case *sessionstreamv1.Event_ParticipantJoined:
		j := p.ParticipantJoined
		pt := v.participant(j.GetParticipantId())
		if j.GetDisplayName() != "" {
			pt.Name = j.GetDisplayName()
		}
		pt.Agent = j.GetKind() == sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT
		pt.imported = j.GetImported()
		pt.captured, pt.paused, pt.left = j.GetCaptured(), false, false
	case *sessionstreamv1.Event_ParticipantLeft:
		v.participant(p.ParticipantLeft.GetParticipantId()).left = true
		v.lowerHand(p.ParticipantLeft.GetParticipantId())
	case *sessionstreamv1.Event_ParticipantPaused:
		v.participant(p.ParticipantPaused.GetParticipantId()).paused = true
	case *sessionstreamv1.Event_ParticipantResumed:
		v.participant(p.ParticipantResumed.GetParticipantId()).paused = false
	case *sessionstreamv1.Event_UtteranceFinal:
		u := &utteranceView{EventID: ev.GetId(), Speaker: v.participant(p.UtteranceFinal.GetParticipantId()),
			Text: p.UtteranceFinal.GetText(), offTheRecord: p.UtteranceFinal.GetOffTheRecord()}
		if t := p.UtteranceFinal.GetOriginalTime(); t != nil {
			u.OriginalTime = t.AsTime().UTC()
		}
		v.utterances = append(v.utterances, u)
		v.byEvent[ev.GetId()] = u
		// A raised hand is lowered when its participant speaks: they had the floor.
		v.lowerHand(u.Speaker.ID)
	case *sessionstreamv1.Event_UtteranceOffTheRecord:
		if u := v.byEvent[p.UtteranceOffTheRecord.GetUtteranceEventId()]; u != nil {
			u.offTheRecord = true
		}
	case *sessionstreamv1.Event_UtteranceCorrected:
		if u := v.byEvent[p.UtteranceCorrected.GetUtteranceEventId()]; u != nil {
			u.Text, u.Corrected = p.UtteranceCorrected.GetText(), true
		}
	case *sessionstreamv1.Event_HandRaised:
		pt := v.participant(p.HandRaised.GetParticipantId())
		v.lowerHand(pt.ID)
		v.hands = append(v.hands, handView{Participant: pt, Topic: p.HandRaised.GetTopic()})
	case *sessionstreamv1.Event_MinutesUpdated:
		v.minutes = p.MinutesUpdated.GetText()
	case *sessionstreamv1.Event_ArtifactShown:
		v.artifact = loadArtifact(ctx, p.ArtifactShown.GetArtifactId(), decks)
	case *sessionstreamv1.Event_SessionImported:
		v.imported = &importView{Source: p.SessionImported.GetSource(), Title: p.SessionImported.GetTitle(),
			By: ev.GetActorParticipantId()}
	case *sessionstreamv1.Event_ImportCompleted:
		if v.imported != nil {
			v.imported.Complete = true
		}
	case *sessionstreamv1.Event_SessionEnded:
		v.ended = true
		v.hands = nil
	}
}

// slidesPrefix starts the artifact ID of a deck, for example "slides/42".
const slidesPrefix = "slides/"

// loadArtifact loads a shown artifact. The part of its ID before the first slash names its type;
// the stage shows decks of the slides interface and gives a note for every other ID.
func loadArtifact(ctx context.Context, id string, decks Slides) artifactView {
	av := artifactView{ID: id}
	if !strings.HasPrefix(id, slidesPrefix) {
		av.Note = "The stage cannot show the artifact " + id + "."
		return av
	}
	if decks == nil {
		av.Note = "The stage cannot load slides."
		return av
	}
	d, err := decks.Load(ctx, id)
	switch {
	case connect.CodeOf(err) == connect.CodeNotFound:
		av.Note = "There is no artifact " + id + "."
	case err != nil:
		av.Note = "The artifact " + id + " could not be loaded."
	case !strings.HasPrefix(d.GetMediaType(), "text/html"):
		av.MediaType = d.GetMediaType()
		av.Note = "The stage cannot show artifacts of type " + d.GetMediaType() + "."
	default:
		av.MediaType = d.GetMediaType()
		av.Content = string(d.GetContent())
	}
	return av
}

// Status is the state of the session: "connecting", "live", "imported", "ended" or "stopped". An
// imported session is never shown as live: the hub did not see its conversation happen.
func (v *view) Status() string {
	switch {
	case v.ended:
		return "ended"
	case v.stopped != "":
		return "stopped"
	case v.live && v.imported != nil:
		return "imported"
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
	case "imported":
		return "Imported"
	}
	return "Connecting …"
}

func (v *view) Imported() *importView            { return v.imported }
func (v *view) Participants() []*participantView { return v.participants }
func (v *view) Hands() []handView                { return v.hands }
func (v *view) Artifact() artifactView           { return v.artifact }
func (v *view) Minutes() string                  { return v.minutes }

// Utterances returns the most recent utterances that are not off the record, oldest first.
func (v *view) Utterances() []*utteranceView {
	var out []*utteranceView
	for i := len(v.utterances) - 1; i >= 0 && len(out) < recentUtterances; i-- {
		if !v.utterances[i].offTheRecord {
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
	if err := web.RegionTemplates.ExecuteTemplate(&b, region, v); err != nil {
		return template.HTMLEscapeString("render " + region + ": " + err.Error())
	}
	return strings.TrimSpace(b.String())
}

// renderAll returns the inner HTML of every region.
func (v *view) renderAll() map[string]template.HTML {
	out := map[string]template.HTML{}
	for _, r := range web.Regions {
		out[r] = template.HTML(v.render(r)) // rendered by html/template above
	}
	return out
}
