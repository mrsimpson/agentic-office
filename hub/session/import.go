package session

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	sessionimportv1 "github.com/mrsimpson/agentic-office/hub/session/importapi"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
)

// This file is the session import (if-session-import, dec-session-import). The Service realizes
// it, so every session log keeps one writer. See import.proto for the contract.

var _ sessionimportv1connect.SessionImportServiceHandler = (*Service)(nil)

// ImportedPrefix starts the participant ID of every imported participant. No registered
// participant ID contains ':', so an imported participant never matches a registered one.
const ImportedPrefix = "imported:"

// MaxImportPage is the largest number of participants, and of utterances, in one page.
const MaxImportPage = 1000

var importKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// importTypes are the event types that a page can create.
var importTypes = []string{events.SessionImported, events.ParticipantJoined, events.UtteranceFinal, events.ImportCompleted}

// ImportConversation imports one page of a conversation (see import.proto).
//
// Order of checks: required fields and the page itself, caller, repeated command_id, schema,
// restore, then the state of the import. A page is checked completely before its first event is
// written, so a page is stored as a whole or not at all.
func (s *Service) ImportConversation(ctx context.Context, req *sessionimportv1.ImportConversationRequest) (*sessionimportv1.ImportConversationResponse, error) {
	sid := req.GetSessionId()
	switch {
	case sid == "":
		return nil, invalid("session_id is required")
	case req.GetCommandId() == "":
		return nil, invalid("command_id is required")
	case req.GetParticipantId() == "":
		return nil, invalid("participant_id is required")
	case len(req.GetParticipants()) > MaxImportPage:
		return nil, invalid("a page has at most 1000 participants")
	case len(req.GetUtterances()) > MaxImportPage:
		return nil, invalid("a page has at most 1000 utterances")
	}
	if c := req.GetConversation(); c != nil && strings.TrimSpace(c.GetSource()) == "" {
		return nil, invalid("conversation.source is required")
	}
	page := map[string]bool{} // keys of this page
	for i, p := range req.GetParticipants() {
		switch {
		case !importKeyPattern.MatchString(p.GetKey()):
			return nil, invalid(field("participants", i, "key") + " must be 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
		case page[p.GetKey()]:
			return nil, invalid(field("participants", i, "key") + " is repeated in the page")
		case strings.TrimSpace(p.GetDisplayName()) == "":
			return nil, invalid(field("participants", i, "display_name") + " is required")
		case p.GetKind() != sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN && p.GetKind() != sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT:
			return nil, invalid(field("participants", i, "kind") + " must be human or agent")
		}
		page[p.GetKey()] = true
	}
	for i, u := range req.GetUtterances() {
		switch {
		case u.GetSpeaker() == "":
			return nil, invalid(field("utterances", i, "speaker") + " is required")
		case strings.TrimSpace(u.GetText()) == "":
			return nil, invalid(field("utterances", i, "text") + " is required")
		case u.GetModality() == sessionstreamv1.Modality_MODALITY_UNSPECIFIED:
			return nil, invalid(field("utterances", i, "modality") + " is required")
		}
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.ParticipantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot import")
	}
	if req.GetParticipantId() != caller.ParticipantID {
		return nil, connect.NewError(connect.CodePermissionDenied, "participant_id must be the participant of the access token")
	}
	st, err := s.lock(sid)
	if err != nil {
		return nil, err
	}
	defer st.mu.Unlock()

	prev, err := s.store.ByCommand(sid, req.GetCommandId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
	}
	if prev != nil {
		if !slices.Contains(importTypes, prev.GetType()) || prev.GetActorParticipantId() != caller.ParticipantID {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another command")
		}
		return s.importResult(sid, st, req.GetCommandId(), prev.GetId(), true)
	}
	if err := st.checkSchema(); err != nil {
		return nil, err
	}
	if err := st.checkRestored(); err != nil {
		return nil, err
	}
	first := st.lastID == 0
	switch {
	case first && req.GetConversation() == nil:
		return nil, invalid("conversation is required on the first page")
	case !first && req.GetConversation() != nil:
		// A page with a conversation starts an import.
		return nil, precondition("the session already has events; an import needs a new session")
	case !first && st.importer == "":
		return nil, precondition("the session is not an imported session")
	case !first && !st.importing:
		return nil, precondition("the import of the session is complete")
	case !first && st.importer != caller.ParticipantID:
		return nil, connect.NewError(connect.CodePermissionDenied, "only the participant who started the import can add pages")
	case !first && len(req.GetParticipants()) == 0 && len(req.GetUtterances()) == 0 && !req.GetComplete():
		return nil, invalid("a page needs participants, utterances or complete")
	}
	for i, p := range req.GetParticipants() {
		if st.participants[ImportedPrefix+p.GetKey()] != nil {
			return nil, invalid(field("participants", i, "key") + " is already a participant of the import")
		}
	}
	for i, u := range req.GetUtterances() {
		if !page[u.GetSpeaker()] && st.participants[ImportedPrefix+u.GetSpeaker()] == nil {
			return nil, invalid(field("utterances", i, "speaker") + " is not a participant of the import")
		}
	}

	var payloads []*sessionstreamv1.Event
	if first {
		payloads = append(payloads, &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionImported{SessionImported: &sessionstreamv1.SessionImported{
			Source: req.GetConversation().GetSource(), Title: req.GetConversation().GetTitle()}}})
	}
	for _, p := range req.GetParticipants() {
		payloads = append(payloads, &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ParticipantJoined{ParticipantJoined: &sessionstreamv1.ParticipantJoined{
			ParticipantId: ImportedPrefix + p.GetKey(), DisplayName: p.GetDisplayName(), Kind: p.GetKind(), Imported: true}}})
	}
	for _, u := range req.GetUtterances() {
		payloads = append(payloads, &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_UtteranceFinal{UtteranceFinal: &sessionstreamv1.UtteranceFinal{
			ParticipantId: ImportedPrefix + u.GetSpeaker(), Text: u.GetText(), Modality: u.GetModality(), OriginalTime: u.GetTime()}}})
	}
	if req.GetComplete() {
		n := st.imported + uint32(len(req.GetUtterances()))
		payloads = append(payloads, &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ImportCompleted{ImportCompleted: &sessionstreamv1.ImportCompleted{Utterances: n}}})
	}
	firstID := st.lastID + 1
	for _, ev := range payloads {
		ev.SessionId, ev.CommandId, ev.ActorParticipantId = sid, req.GetCommandId(), caller.ParticipantID
		ev.Type = events.TypeOf(ev)
		ev.Time = timestamppb.Now()
		if ev, err = s.store.Append(sid, ev); err != nil {
			return nil, connect.NewError(connect.CodeInternal, "append to session log: "+err.Error())
		}
		if err := st.apply(ev); err != nil {
			return nil, err
		}
		s.markDirty(sid, ev.GetType())
	}
	return &sessionimportv1.ImportConversationResponse{FirstEventId: firstID, LastEventId: st.lastID,
		Complete: !st.importing, TreeHead: s.signedHead(sid, st)}, nil
}

// importResult returns the stored result of a page whose last event is last. The caller holds
// st.mu.
func (s *Service) importResult(sid string, st *state, cmdID string, last uint32, dup bool) (*sessionimportv1.ImportConversationResponse, error) {
	evs, _, err := s.store.Read(sid, 0)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
	}
	first := last
	for first > 1 && evs[first-2].GetCommandId() == cmdID {
		first--
	}
	return &sessionimportv1.ImportConversationResponse{FirstEventId: first, LastEventId: last, Duplicate: dup,
		Complete: st.importer != "" && !st.importing, TreeHead: s.signedHeadAt(sid, st, int(last))}, nil
}

// signedHeadAt returns the signed tree head over the first n events. The caller holds st.mu.
func (s *Service) signedHeadAt(sid string, st *state, n int) *sessionstreamv1.SignedTreeHead {
	if n == len(st.leaves) {
		return s.signedHead(sid, st)
	}
	return merkle.SignTreeHead(s.key, st.schema, sid, st.leaves[:n])
}

func field(list string, i int, name string) string {
	return list + "[" + strconv.Itoa(i) + "]." + name
}
