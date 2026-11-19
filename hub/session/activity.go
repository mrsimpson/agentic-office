package session

import (
	"context"
	"regexp"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	sessionactivityv1 "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	"github.com/mrsimpson/agentic-office/hub/session/activityapi/sessionactivityv1connect"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
)

// ActivityTTL is how long an activity lives without a new report (if-session-activity).
const ActivityTTL = 60 * time.Second

var _ sessionactivityv1connect.SessionActivityServiceHandler = (*Service)(nil)

// toolName is the form of a tool name in an activity: a short word, never free text.
var toolName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// activities keeps the short-lived activities of the participants (dec-agent-activity). They are
// not events: they live in memory only, never reach the log, the archive or a peer, and end
// when they expire, when the participant leaves or when the session ends.
type activities struct {
	mu sync.Mutex
	m  map[string]*sessionActivities // by session ID
}

type sessionActivities struct {
	byParticipant map[string]activityEntry
	changed       chan struct{} // closed and replaced at every change
}

type activityEntry struct {
	a       *sessionactivityv1.Activity
	expires time.Time
}

func (as *activities) session(sid string) *sessionActivities {
	if as.m == nil {
		as.m = map[string]*sessionActivities{}
	}
	sa := as.m[sid]
	if sa == nil {
		sa = &sessionActivities{byParticipant: map[string]activityEntry{}, changed: make(chan struct{})}
		as.m[sid] = sa
	}
	return sa
}

func (sa *sessionActivities) notify() {
	close(sa.changed)
	sa.changed = make(chan struct{})
}

// set keeps the activity of a participant.
func (as *activities) set(sid string, a *sessionactivityv1.Activity, now time.Time) {
	as.mu.Lock()
	defer as.mu.Unlock()
	sa := as.session(sid)
	sa.byParticipant[a.GetParticipantId()] = activityEntry{a: a, expires: now.Add(ActivityTTL)}
	sa.notify()
}

// drop ends the activity of a participant, or of all participants when pid is empty.
func (as *activities) drop(sid, pid string) {
	as.mu.Lock()
	defer as.mu.Unlock()
	sa := as.m[sid]
	if sa == nil {
		return
	}
	if pid == "" {
		clear(sa.byParticipant)
	} else if _, ok := sa.byParticipant[pid]; !ok {
		return
	} else {
		delete(sa.byParticipant, pid)
	}
	sa.notify()
}

// snapshot returns the current activities of a session ordered by participant, the channel that
// is closed at the next change, and the time of the next expiry (zero: none). It drops the
// activities that have expired.
func (as *activities) snapshot(sid string, now time.Time) ([]*sessionactivityv1.Activity, <-chan struct{}, time.Time) {
	as.mu.Lock()
	defer as.mu.Unlock()
	sa := as.session(sid)
	var out []*sessionactivityv1.Activity
	var next time.Time
	for pid, e := range sa.byParticipant {
		if !now.Before(e.expires) {
			delete(sa.byParticipant, pid)
			continue
		}
		out = append(out, e.a)
		if next.IsZero() || e.expires.Before(next) {
			next = e.expires
		}
	}
	slices.SortFunc(out, func(a, b *sessionactivityv1.Activity) int {
		switch {
		case a.GetParticipantId() < b.GetParticipantId():
			return -1
		case a.GetParticipantId() > b.GetParticipantId():
			return 1
		}
		return 0
	})
	return out, sa.changed, next
}

// endActivities ends activities after an event of the log: the activity of a participant who
// left, and all activities when the session ended. The caller holds the lock of the session.
func (s *Service) endActivities(sid string, ev *sessionstreamv1.Event) {
	switch ev.GetType() {
	case events.ParticipantLeft:
		s.activity.drop(sid, ev.GetParticipantLeft().GetParticipantId())
	case events.SessionEnded:
		s.activity.drop(sid, "")
	}
}

// ReportActivity sets the activity of the caller (if-session-activity).
func (s *Service) ReportActivity(ctx context.Context, req *sessionactivityv1.ReportActivityRequest) (*sessionactivityv1.ReportActivityResponse, error) {
	sid, pid := req.GetSessionId(), req.GetParticipantId()
	switch {
	case sid == "":
		return nil, invalid("session_id is required")
	case pid == "":
		return nil, invalid("participant_id is required")
	}
	switch req.GetState() {
	case sessionactivityv1.ActivityState_ACTIVITY_STATE_IDLE:
		if req.GetTool() != "" {
			return nil, invalid("tool is only allowed with ACTIVITY_STATE_WORKING")
		}
	case sessionactivityv1.ActivityState_ACTIVITY_STATE_WORKING:
		if req.GetUnspokenReply() {
			return nil, invalid("unspoken_reply is only allowed with ACTIVITY_STATE_IDLE")
		}
		if t := req.GetTool(); t != "" && !toolName.MatchString(t) {
			return nil, invalid("tool must be a short name: a lowercase letter, then lowercase letters, digits and underscores, at most 64 characters")
		}
	default:
		return nil, invalid("state is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.ParticipantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot report an activity")
	}
	if pid != caller.ParticipantID {
		return nil, connect.NewError(connect.CodePermissionDenied, "participant_id must be the participant of the access token")
	}
	st, err := s.lock(sid)
	if err != nil {
		return nil, err
	}
	defer st.mu.Unlock()
	if st.ended {
		return nil, precondition("the session has ended")
	}
	switch pt := st.participants[pid]; {
	case pt == nil:
		return nil, precondition("the participant has not joined this session")
	case pt.left:
		return nil, precondition("the participant has left the session")
	}
	now := time.Now()
	a := &sessionactivityv1.Activity{ParticipantId: pid, State: req.GetState(), Tool: req.GetTool(),
		UnspokenReply: req.GetUnspokenReply(), Since: timestamppb.New(now)}
	// Under the lock of the session, so a leave or the end cannot come between the check and the
	// report.
	s.activity.set(sid, a, now)
	return &sessionactivityv1.ReportActivityResponse{Activity: a, Ttl: durationpb.New(ActivityTTL)}, nil
}

// WatchActivity sends the activities of a session, then every change (if-session-activity).
func (s *Service) WatchActivity(ctx context.Context, req *sessionactivityv1.WatchActivityRequest, stream sessionactivityv1connect.SessionActivityServiceWatchActivityServerStream) error {
	sid := req.GetSessionId()
	if sid == "" {
		return invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return err
	}
	if caller.StageSessionID != "" && caller.StageSessionID != sid {
		return connect.NewError(connect.CodePermissionDenied, "the stage link is for another session")
	}
	st, err := s.lock(sid)
	if err != nil {
		return err
	}
	ended := st.ended
	st.mu.Unlock()
	if err := stream.SendHeaders(); err != nil {
		return err
	}
	for {
		as, changed, next := s.activity.snapshot(sid, time.Now())
		if err := stream.Send(&sessionactivityv1.WatchActivityResponse{Activities: as}); err != nil {
			return err
		}
		if ended {
			return nil
		}
		var expiry <-chan time.Time
		var timer *time.Timer
		if !next.IsZero() {
			timer = time.NewTimer(time.Until(next))
			expiry = timer.C
		}
		select {
		case <-ctx.Done():
		case <-caller.Done:
		case <-changed:
		case <-expiry:
		}
		if timer != nil {
			timer.Stop()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-caller.Done:
			return connect.NewError(connect.CodeUnauthenticated, "access token expired or revoked")
		default:
		}
		st, err := s.lock(sid)
		if err != nil {
			return err
		}
		ended = st.ended
		st.mu.Unlock()
	}
}
