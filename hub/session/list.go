package session

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"

	sessionlistv1 "github.com/mrsimpson/agentic-office/hub/session/listapi"
	"github.com/mrsimpson/agentic-office/hub/session/listapi/sessionlistv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
)

var _ sessionlistv1connect.SessionListServiceHandler = (*Service)(nil)

// summary is what the list shows of one session, and who may see it.
type summary struct {
	s        *sessionlistv1.Session
	importer string // the participant who imported the session, or empty
	finished bool   // the log does not change any more: the summary can be kept
}

// summaries keeps the summaries of finished sessions that were read from the archive, so a list
// reads every stored log once (dec-session-eviction keeps them out of memory otherwise).
type summaries struct {
	mu sync.Mutex
	m  map[string]*summary
}

func (ss *summaries) get(sid string) *summary {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.m[sid]
}

func (ss *summaries) put(sid string, s *summary) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.m == nil {
		ss.m = map[string]*summary{}
	}
	ss.m[sid] = s
}

// summarize builds the summary of a session from its events. It returns nil for a log without
// events.
func summarize(sid string, evs []*sessionstreamv1.Event) *summary {
	if len(evs) == 0 {
		return nil
	}
	sum := &summary{s: &sessionlistv1.Session{SessionId: sid, StartedAt: evs[0].GetTime()}}
	seen := map[string]bool{}
	for _, ev := range evs {
		switch {
		case ev.GetSessionImported() != nil:
			sum.s.Imported, sum.importer = true, ev.GetActorParticipantId()
		case ev.GetParticipantJoined() != nil:
			j := ev.GetParticipantJoined()
			if seen[j.GetParticipantId()] {
				continue
			}
			seen[j.GetParticipantId()] = true
			name := j.GetDisplayName()
			if name == "" {
				name = j.GetParticipantId()
			}
			sum.s.Participants = append(sum.s.Participants, &sessionlistv1.Participant{ParticipantId: j.GetParticipantId(),
				DisplayName: name, Kind: j.GetKind(), Imported: j.GetImported()})
		case ev.GetSessionEnded() != nil:
			sum.s.EndedAt, sum.finished = ev.GetTime(), true
		}
	}
	return sum
}

// visible reports whether the caller may see the session: a participant who joined it, the
// participant who imported it, or a tenant admin.
func (sum *summary) visible(c Caller) bool {
	if c.TenantAdmin || (sum.importer != "" && sum.importer == c.ParticipantID) {
		return true
	}
	for _, p := range sum.s.Participants {
		if p.GetParticipantId() == c.ParticipantID {
			return true
		}
	}
	return false
}

// ListSessions implements the session list (if-session-list).
func (s *Service) ListSessions(ctx context.Context, _ *sessionlistv1.ListSessionsRequest) (*sessionlistv1.ListSessionsResponse, error) {
	c, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if c.ParticipantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot list sessions")
	}
	// The sessions in memory, and the ended sessions that only the archive holds.
	s.mu.Lock()
	ids := make([]string, 0, len(s.sessions))
	inMemory := map[string]bool{}
	for sid, st := range s.sessions {
		if !st.evicted {
			ids = append(ids, sid)
			inMemory[sid] = true
		}
	}
	s.mu.Unlock()
	var stored []string
	if s.arch != nil {
		paths, err := s.arch.opts.Archive.List(ctx, "sessions")
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "list the sessions in the archive: "+err.Error())
		}
		seen := map[string]bool{}
		for _, p := range paths {
			dir := path.Dir(p)
			if path.Dir(dir) != "sessions" || seen[dir] {
				continue
			}
			seen[dir] = true
			if sid := sessionIDOf(path.Base(dir)); !inMemory[sid] {
				stored = append(stored, path.Base(dir))
			}
		}
	}
	res := &sessionlistv1.ListSessionsResponse{}
	add := func(sum *summary) {
		if sum != nil && sum.visible(c) {
			res.Sessions = append(res.Sessions, sum.s)
		}
	}
	for _, sid := range ids {
		evs, _, err := s.store.Read(sid, 0)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "read session log: "+err.Error())
		}
		if len(evs) == 0 && s.arch != nil {
			// The session left memory after the look at s.sessions (dec-session-eviction). It
			// leaves only after its last write, so the archive holds it.
			sum, err := s.storedSummary(ctx, ArchiveName(sid))
			if err != nil {
				s.logf("list: session %q: %v; it is left out", sid, err)
				continue
			}
			add(sum)
			continue
		}
		add(summarize(sid, evs))
	}
	for _, name := range stored {
		sum, err := s.storedSummary(ctx, name)
		if err != nil {
			s.logf("list: session %q: %v; it is left out", sessionIDOf(name), err)
			continue
		}
		add(sum)
	}
	slices.SortFunc(res.Sessions, func(a, b *sessionlistv1.Session) int {
		if c := b.GetStartedAt().AsTime().Compare(a.GetStartedAt().AsTime()); c != 0 {
			return c
		}
		return strings.Compare(a.GetSessionId(), b.GetSessionId())
	})
	return res, nil
}

// storedSummary reads the summary of a session that only the archive holds. The log must verify
// against its signed tree head, as for every use of a stored log; a finished summary is kept.
func (s *Service) storedSummary(ctx context.Context, name string) (*summary, error) {
	sid := sessionIDOf(name)
	if sum := s.summaries.get(sid); sum != nil {
		return sum, nil
	}
	files, err := s.arch.opts.Archive.Read(ctx, "sessions/"+name)
	if err != nil {
		return nil, err
	}
	var logData, headData []byte
	for _, f := range files {
		switch path.Base(f.Path) {
		case path.Base(ArchiveLogFile):
			logData = f.Data
		case path.Base(ArchiveTreeHeadFile):
			headData = f.Data
		}
	}
	if logData == nil && headData == nil {
		return nil, nil // a session that was never written, for example one without events
	}
	head, err := logfile.UnmarshalTreeHead(headData)
	if err != nil {
		return nil, err
	}
	if head.GetSessionId() != "" {
		sid = head.GetSessionId()
	}
	evs, err := logfile.UnmarshalLines(logData)
	if err != nil {
		return nil, err
	}
	if err := s.verifyStored(evs, head); err != nil {
		return nil, err
	}
	sum := summarize(sid, evs)
	if sum != nil && sum.finished {
		s.summaries.put(sid, sum)
	}
	return sum, nil
}

// mayRead checks who reads a session (if-session-stream): everyone who ever joined it, the
// participant who imported it, a tenant admin of the tenant, and a stage link for this session.
// Everybody else gets the answer for a session that does not exist. The caller holds st.mu; a
// stage link for another session was refused before.
func mayRead(c Caller, st *state) error {
	if c.StageSessionID != "" || c.TenantAdmin {
		return nil
	}
	if st.participants[c.ParticipantID] != nil || (st.importer != "" && st.importer == c.ParticipantID) {
		return nil
	}
	return notFound("no such session for you: only its participants, the participant who imported it and the tenant admins read it")
}
