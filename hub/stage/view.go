package stage

import (
	"bytes"
	"context"
	"html/template"
	"path"
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
	artifactLog  []logEntry // the events about artifacts, oldest first
	artifact     artifactView
	focus        string           // path or ID of the artifact in focus
	entries      []*artifactEntry // the artifacts of the session in the order they first appeared
	loaded       string           // focus and version of the artifact that v.artifact holds
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
	ID, Name  string
	Agent     bool
	imported  bool
	hand      int // the place in the queue of raised hands, 0 if the hand is down
	handTopic string
	captured  bool
	paused    bool
	left      bool
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

// logEntry is one line of the artifact log.
type logEntry struct {
	Time  time.Time
	Actor *participantView
	Verb  string // "created", "changed" or "focused"
	Path  string
}

// logArtifact adds a line to the artifact log.
func (v *view) logArtifact(ev *sessionstreamv1.Event, verb, path string) {
	v.artifactLog = append(v.artifactLog, logEntry{Time: ev.GetTime().AsTime().UTC(), Actor: v.participant(ev.GetActorParticipantId()), Verb: verb, Path: path})
}

// recentLog is how many lines the artifact log shows.
const recentLog = 6

// ArtifactLog returns the latest lines of the artifact log, newest first.
func (v *view) ArtifactLog() []logEntry {
	var out []logEntry
	for i := len(v.artifactLog) - 1; i >= 0 && len(out) < recentLog; i-- {
		out = append(out, v.artifactLog[i])
	}
	return out
}

// HandCount is the number of raised hands.
func (v *view) HandCount() int { return len(v.hands) }

// ShowsMinutes reports whether the minutes take the place of an artifact: nothing is in focus.
func (v *view) ShowsMinutes() bool { return v.focus == "" }

// MinutesHTML renders the minutes as Markdown. Raw HTML in the text is left out.
func (v *view) MinutesHTML() template.HTML {
	var b bytes.Buffer
	if err := markdown.Convert([]byte(v.minutes), &b); err != nil {
		return template.HTML(template.HTMLEscapeString(v.minutes))
	}
	return template.HTML(b.String()) // goldmark leaves out raw HTML and unsafe links
}

// HandNumber is the place of the raised hand in the queue, or 0 if the hand is down.
func (p *participantView) HandNumber() int { return p.hand }

// HandTopic is what the participant wants to say with the raised hand.
func (p *participantView) HandTopic() string { return p.handTopic }

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
	Kind      string // "slides", "document", "data" or "file"
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
func (v *view) apply(ev *sessionstreamv1.Event) {
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
	case *sessionstreamv1.Event_ArtifactFocused:
		id := p.ArtifactFocused.GetArtifactId()
		v.focus = id
		if id == MinutesID {
			v.focus = ""
		} else {
			v.entry(id)
		}
		v.logArtifact(ev, "focused", id)
	case *sessionstreamv1.Event_ArtifactCreated:
		v.entry(p.ArtifactCreated.GetPath()).version = p.ArtifactCreated.GetVersion()
		v.logArtifact(ev, "created", p.ArtifactCreated.GetPath())
	case *sessionstreamv1.Event_ArtifactChanged:
		v.entry(p.ArtifactChanged.GetPath()).version = p.ArtifactChanged.GetVersion()
		v.logArtifact(ev, "changed", p.ArtifactChanged.GetPath())
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

// artifactEntry is an artifact that the log of the session names.
type artifactEntry struct {
	Path    string
	version string // of the last created or changed event; empty if there is none
	v       *view
}

// MinutesID is the artifact ID of the running minutes of the session. The minutes are the first
// artifact of every session; focusing it (or no artifact at all) shows them in the middle.
const MinutesID = "minutes"

// Focused reports whether the artifact is the one in focus.
func (e *artifactEntry) Focused() bool {
	if e.Path == MinutesID {
		return e.v.ShowsMinutes()
	}
	return e.Path == e.v.focus
}

// Label is the name of the tab: "Minutes", or the last part of the path.
func (e *artifactEntry) Label() string {
	if e.Path == MinutesID {
		return "Minutes"
	}
	return path.Base(e.Path)
}

// entry returns the entry of a path and adds it when the log names the path for the first time.
func (v *view) entry(p string) *artifactEntry {
	for _, e := range v.entries {
		if e.Path == p {
			return e
		}
	}
	e := &artifactEntry{Path: p, v: v}
	v.entries = append(v.entries, e)
	return e
}

// Artifacts returns the artifacts of the session: the minutes first, then the others in the order
// the log named them.
func (v *view) Artifacts() []*artifactEntry {
	return append([]*artifactEntry{{Path: MinutesID, v: v}}, v.entries...)
}

// refresh loads the artifact in focus when it is another one or has changed since it was loaded.
// Loading happens here, once per batch of events, and not for every event: a reconnect replays
// the whole log.
func (v *view) refresh(ctx context.Context, decks Slides, files Artifacts) {
	if v.focus == "" {
		return
	}
	key := v.focus + "@" + v.entry(v.focus).version
	if key == v.loaded {
		return
	}
	v.loaded = key
	v.artifact = loadArtifact(ctx, v.focus, decks, files)
}

// slidesPrefix starts the artifact ID of a deck, for example "slides/42".
const slidesPrefix = "slides/"

// loadArtifact loads a shown artifact. The part of its ID before the first slash names its type;
// the stage shows decks of the slides interface and gives a note for every other ID.
func loadArtifact(ctx context.Context, id string, decks Slides, files Artifacts) artifactView {
	av := artifactView{ID: id, Kind: "slides"}
	if !strings.HasPrefix(id, slidesPrefix) {
		if p := path.Ext(id); p == ".md" || p == ".yaml" || p == ".yml" {
			return loadFile(ctx, id, files)
		}
		av.Kind = ""
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

func (v *view) Imported() *importView { return v.imported }
func (v *view) Participants() []*participantView {
	for _, p := range v.participants {
		p.hand, p.handTopic = 0, ""
	}
	for i, h := range v.hands {
		h.Participant.hand, h.Participant.handTopic = i+1, h.Topic
	}
	return v.participants
}
func (v *view) Hands() []handView      { return v.hands }
func (v *view) Artifact() artifactView { return v.artifact }
func (v *view) Minutes() string        { return v.minutes }

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
