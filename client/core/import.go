package core

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// Import imports a conversation as a new session (dec-session-import). sessionID may be empty: then
// Import creates one ("import-" plus a new ID). Each page gets its own command ID; a page that is
// sent again after a dropped token is not stored twice.
//
// If sessionID names an imported session that exists already, Import extends it instead: the
// conversation must start with every utterance that the session holds, unchanged and in the same
// order. Import then sends only the participants and utterances after them, each page with the
// base that the hub checks (if-session-import). A file that adds nothing imports nothing.
func (c *hubClient) Import(ctx context.Context, sessionID string, conv *coreapi.Conversation) (*coreapi.Imported, error) {
	ps, us, err := conv.Parts()
	if err != nil {
		return nil, err
	}
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		sessionID = "import-" + coreapi.NewCommandID()
	} else {
		prev, err := c.importedSoFar(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		if prev != nil {
			return c.extendImport(ctx, sessionID, pid, ps, us, prev)
		}
	}
	pages := coreapi.ImportPages(ps, us)
	pages[0].Conversation = &sessionimportv1.Conversation{Source: conv.Source, Title: conv.Title}
	out := &coreapi.Imported{SessionID: sessionID, Participants: len(ps), Utterances: len(us)}
	return out, c.sendPages(ctx, sessionID, pid, pages, out)
}

// sendPages sends the pages of an import and fills in the event IDs and the tree head of out.
func (c *hubClient) sendPages(ctx context.Context, sessionID, pid string, pages []*sessionimportv1.ImportConversationRequest, out *coreapi.Imported) error {
	out.Pages = len(pages)
	for i, p := range pages {
		p.SessionId, p.CommandId, p.ParticipantId = sessionID, coreapi.NewCommandID(), pid
		var res *sessionimportv1.ImportConversationResponse
		err := retry(ctx, func() (err error) {
			res, err = c.sessionImport.ImportConversation(ctx, p)
			return err
		})
		if err != nil {
			return fmt.Errorf("import page %d of %d into session %s: %w", i+1, len(pages), sessionID, err)
		}
		if i == 0 {
			out.FirstEventID = res.GetFirstEventId()
		}
		out.LastEventID, out.TreeHead = res.GetLastEventId(), res.GetTreeHead()
	}
	return nil
}

// importLog is what a session holds of an import.
type importLog struct {
	imported     bool // the session starts with session.imported
	importing    bool // its import is not complete
	ended        bool
	participants map[string]*sessionstreamv1.ParticipantJoined // imported speakers by key
	utterances   []*sessionimportv1.ImportedUtterance
}

// importedSoFar reads a session and returns what it holds of an import, or nil if the session
// has no events.
func (c *hubClient) importedSoFar(ctx context.Context, sessionID string) (*importLog, error) {
	l := &importLog{participants: map[string]*sessionstreamv1.ParticipantJoined{}}
	var after uint32
	for {
		evs, more, err := c.Events(ctx, sessionID, after, 0)
		if connect.CodeOf(err) == connect.CodeNotFound && after == 0 {
			return nil, nil // a new session, or one that the caller may not read: the hub decides
		}
		if err != nil {
			return nil, fmt.Errorf("read session %s: %w", sessionID, err)
		}
		for _, ev := range evs {
			switch p := ev.GetPayload().(type) {
			case *sessionstreamv1.Event_SessionImported:
				l.imported, l.importing = true, true
			case *sessionstreamv1.Event_ImportCompleted:
				l.importing = false
			case *sessionstreamv1.Event_SessionEnded:
				l.ended = true
			case *sessionstreamv1.Event_ParticipantJoined:
				if j := p.ParticipantJoined; j.GetImported() {
					l.participants[strings.TrimPrefix(j.GetParticipantId(), sessionimportv1.ImportedPrefix)] = j
				}
			case *sessionstreamv1.Event_UtteranceFinal:
				key, ok := strings.CutPrefix(p.UtteranceFinal.GetParticipantId(), sessionimportv1.ImportedPrefix)
				if ok && l.participants[key] != nil {
					l.utterances = append(l.utterances, &sessionimportv1.ImportedUtterance{Speaker: key,
						Text: p.UtteranceFinal.GetText(), Modality: p.UtteranceFinal.GetModality(), Time: p.UtteranceFinal.GetOriginalTime()})
				}
			}
			after = ev.GetId()
		}
		if !more || len(evs) == 0 {
			break
		}
	}
	if after == 0 {
		return nil, nil
	}
	return l, nil
}

// extendImport extends the imported session sessionID with the end of a conversation that starts
// with every utterance of prev.
func (c *hubClient) extendImport(ctx context.Context, sessionID, pid string, ps []*sessionimportv1.ImportedParticipant,
	us []*sessionimportv1.ImportedUtterance, prev *importLog) (*coreapi.Imported, error) {
	switch {
	case !prev.imported:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s is not an imported session; import into a new session", sessionID))
	case prev.ended:
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("session %s has ended; an ended session takes no more utterances", sessionID))
	case len(us) < len(prev.utterances):
		return nil, fmt.Errorf("%w: the file has %d utterances, but session %s holds %d; the file must start with all of them",
			coreapi.ErrBadInput, len(us), sessionID, len(prev.utterances))
	}
	var digest []byte
	for i, held := range prev.utterances {
		if !sameUtterance(held, us[i]) {
			return nil, fmt.Errorf("%w: utterance %d differs from the one that session %s holds: the session has %s, the file has %s",
				coreapi.ErrBadInput, i, sessionID, describe(held), describe(us[i]))
		}
		digest = sessionimportv1.ImportDigest(digest, us[i])
	}
	var join []*sessionimportv1.ImportedParticipant
	for i, p := range ps {
		j := prev.participants[p.GetKey()]
		switch {
		case j == nil:
			join = append(join, p)
		case j.GetDisplayName() != p.GetDisplayName() || j.GetKind() != p.GetKind():
			return nil, fmt.Errorf("%w: participants[%d] %q differs from the participant of session %s (name %q)",
				coreapi.ErrBadInput, i, p.GetKey(), sessionID, j.GetDisplayName())
		}
	}
	n := len(prev.utterances)
	out := &coreapi.Imported{SessionID: sessionID, Participants: len(join), Utterances: len(us) - n, Extended: true, Previous: n}
	if len(join) == 0 && len(us) == n && !prev.importing {
		return out, nil // the session holds the whole conversation
	}
	pages := coreapi.ImportPages(join, us[n:])
	for _, p := range pages {
		p.Base = &sessionimportv1.ImportBase{Utterances: uint32(n), Digest: digest}
		for _, u := range p.GetUtterances() {
			digest = sessionimportv1.ImportDigest(digest, u)
			n++
		}
	}
	return out, c.sendPages(ctx, sessionID, pid, pages, out)
}

// sameUtterance reports whether two imported utterances are the same.
func sameUtterance(a, b *sessionimportv1.ImportedUtterance) bool {
	return a.GetSpeaker() == b.GetSpeaker() && a.GetText() == b.GetText() && a.GetModality() == b.GetModality() &&
		proto.Equal(a.GetTime(), b.GetTime())
}

// describe names an utterance in an error message: its speaker and the start of its text.
func describe(u *sessionimportv1.ImportedUtterance) string {
	text := u.GetText()
	if utf8.RuneCountInString(text) > 60 {
		text = string([]rune(text)[:60]) + "…"
	}
	return fmt.Sprintf("%s: %q", u.GetSpeaker(), text)
}
