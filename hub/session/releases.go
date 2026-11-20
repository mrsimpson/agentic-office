package session

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/release"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
)

// This file is the release of sessions (concept-session-release, dec-staging-repository): the
// staging area of each session, the release status, the commands release and object, the timers
// of the deadlines and the search for quotes of sentences off the record.

// DefaultGracePeriod is the time between the end of a session with humans and its release by the
// hub, unless the tenant sets another length.
const DefaultGracePeriod = 24 * time.Hour

// HubReleaseCommandID is the command ID of session.released when the hub releases a session: at
// the end of its grace period, or at the end of a session without humans. A session has one log,
// so the ID is unique in it.
const HubReleaseCommandID = "office/release"

// HubHoldCommandID is the command ID of release.objected when the hub holds a session: its grace
// period ended while a staged file quoted a sentence off the record word for word.
const HubHoldCommandID = "office/hold"

// releaseRetry is the time after which the worker tries a release again that failed.
const releaseRetry = 5 * time.Second

// ReleaseOptions configure the release of sessions.
type ReleaseOptions struct {
	// Staging is the staging repository of the tenant (if-archive-storage). Required.
	Staging storage.Staging
	// GracePeriod is the time between the end of a session with humans and its release by the hub.
	// Zero means DefaultGracePeriod.
	GracePeriod time.Duration
	// Now is the clock of the deadlines. Nil means time.Now.
	Now func() time.Time
	// Logf logs releases and failures. Nil means the Logf of the archive options.
	Logf func(format string, args ...any)
}

// releaser is the state of the release worker of the tenant: the deadlines it keeps in memory.
type releaser struct {
	opts ReleaseOptions
	mu   sync.Mutex
	due  map[string]time.Time // session ID -> when the worker releases it, or tries again
	wake chan struct{}
}

func (r *releaser) now() time.Time { return r.opts.Now() }

// schedule makes the worker release a session at a time.
func (r *releaser) schedule(sid string, at time.Time) {
	r.mu.Lock()
	r.due[sid] = at
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// cancel takes a session out of the timers.
func (r *releaser) cancel(sid string) {
	r.mu.Lock()
	delete(r.due, sid)
	r.mu.Unlock()
}

func (r *releaser) deadline(sid string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.due[sid]
}

// UseRelease makes the service own the release of its sessions (concept-session-release): it
// opens the staging area of every session that starts, keeps the deadlines and releases. It
// rebuilds the deadlines from the staging repository: a release that a restart cut off and a
// deadline that passed while the hub was down are due at once. Call it after UseArchive, before
// the service takes commands, then run RunRelease.
func (s *Service) UseRelease(ctx context.Context, opts ReleaseOptions) error {
	if opts.Staging == nil {
		return errors.New("session: no staging repository")
	}
	if opts.GracePeriod < 0 {
		return errors.New("session: the grace period must not be negative")
	}
	if opts.GracePeriod == 0 {
		opts.GracePeriod = DefaultGracePeriod
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = s.logf
	}
	r := &releaser{opts: opts, due: map[string]time.Time{}, wake: make(chan struct{}, 1)}
	areas, err := opts.Staging.Areas(ctx)
	if err != nil {
		return fmt.Errorf("session: read the staging areas: %w", err)
	}
	now := r.now()
	waiting, held := 0, 0
	for _, a := range areas {
		if a.Release != nil {
			if !a.Release.Done {
				r.due[a.Session] = now // a release that a restart cut off
			}
			continue // a done release keeps only the files whose path is taken
		}
		st, err := s.lock(a.Session)
		if err != nil {
			opts.Logf("release: session %q: %v", a.Session, err)
			continue
		}
		switch {
		case st.released:
			r.due[a.Session] = now // released, but the area did not move yet
		case st.held:
			held++
		case st.ended:
			at := a.Deadline
			if at.IsZero() {
				at = now
			}
			r.due[a.Session] = at
			waiting++
		}
		st.mu.Unlock()
	}
	if len(areas) > 0 {
		opts.Logf("release: %d staging areas, %d sessions wait for their release, %d are held", len(areas), waiting, held)
	}
	s.rel = r
	return nil
}

// RunRelease is the release worker of the tenant: it releases every session whose deadline has
// come, until ctx is done. Call it once, in its own goroutine. A release that fails is tried again
// after a few seconds.
func (s *Service) RunRelease(ctx context.Context) {
	r := s.rel
	if r == nil {
		return
	}
	for {
		r.mu.Lock()
		var next time.Time
		for _, at := range r.due {
			if next.IsZero() || at.Before(next) {
				next = at
			}
		}
		r.mu.Unlock()
		var timer *time.Timer
		var fire <-chan time.Time
		if !next.IsZero() {
			timer = time.NewTimer(max(next.Sub(r.now()), 0))
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-fire:
		case <-r.wake:
		}
		if timer != nil {
			timer.Stop()
		}
		now := r.now()
		var due []string
		r.mu.Lock()
		for sid, at := range r.due {
			if !at.After(now) {
				due = append(due, sid)
				delete(r.due, sid)
			}
		}
		r.mu.Unlock()
		slices.Sort(due)
		for _, sid := range due {
			if err := s.releaseDue(context.WithoutCancel(ctx), sid); err != nil {
				r.opts.Logf("release: session %q: %v; trying again", sid, err)
				r.schedule(sid, r.now().Add(releaseRetry))
			}
		}
	}
}

// releaseDue releases a session whose deadline has come, with the hub as the author. A session
// that a human released already only completes the move of its area. A session that is held, or
// that has not ended in its log, is not released.
func (s *Service) releaseDue(ctx context.Context, sid string) error {
	st, err := s.lock(sid)
	if err != nil {
		return err
	}
	switch {
	case st.lastID == 0 || !st.ended:
		st.mu.Unlock()
		s.rel.opts.Logf("release: session %q has a staging area but has not ended; it is not released", sid)
		return nil
	case !st.released && st.held:
		st.mu.Unlock()
		return nil
	case !st.released:
		if err := st.checkSchema(); err != nil {
			st.mu.Unlock()
			s.rel.opts.Logf("release: session %q: %v", sid, err)
			return nil
		}
		// A session with humans is held while a staged file quotes a sentence off the record:
		// only a human releases it then, with the quote token. Nobody could confirm the quotes of
		// a session without humans.
		if st.hasHumans() {
			quotes, _, err := s.findQuotes(ctx, sid, st)
			if err != nil {
				st.mu.Unlock()
				return err
			}
			if len(quotes) > 0 {
				ev := &sessionstreamv1.Event{SessionId: sid, CommandId: HubHoldCommandID,
					Payload: &sessionstreamv1.Event_ReleaseObjected{ReleaseObjected: &sessionstreamv1.ReleaseObjected{}}}
				if err := s.appendEvent(sid, st, ev); err != nil {
					st.mu.Unlock()
					return err
				}
				st.mu.Unlock()
				s.rel.opts.Logf("release: session %q held by the hub: %d quotes of sentences off the record", sid, len(quotes))
				if err := s.persistNow(ctx, sid); err != nil {
					return err
				}
				return s.markHeld(ctx, sid)
			}
		}
		ev := &sessionstreamv1.Event{SessionId: sid, CommandId: HubReleaseCommandID,
			Payload: &sessionstreamv1.Event_SessionReleased{SessionReleased: &sessionstreamv1.SessionReleased{}}}
		if err := s.appendEvent(sid, st, ev); err != nil {
			st.mu.Unlock()
			return err
		}
		s.rel.opts.Logf("release: session %q released by the hub", sid)
	}
	st.mu.Unlock()
	if err := s.persistNow(ctx, sid); err != nil {
		return err
	}
	return s.moveArea(ctx, sid)
}

// releaseAtEnd releases a session without humans at its end (concept-session-release). A failure
// leaves the release to the worker.
func (s *Service) releaseAtEnd(ctx context.Context, sid string) {
	if s.rel == nil {
		return
	}
	st, err := s.lock(sid)
	if err != nil {
		return
	}
	now := st.ended && !st.hasHumans()
	st.mu.Unlock()
	if !now {
		return
	}
	s.rel.cancel(sid)
	if err := s.releaseDue(context.WithoutCancel(ctx), sid); err != nil {
		s.rel.opts.Logf("release: session %q: %v; trying again", sid, err)
		s.rel.schedule(sid, s.rel.now().Add(releaseRetry))
	}
}

// moveArea moves the staging area of a released session into the artifact repository, with the
// author of session.released: the human who released, or the hub. A session without an area has
// nothing to move. The archive completes a move that a restart cut off and never makes it twice.
func (s *Service) moveArea(ctx context.Context, sid string) error {
	if s.rel == nil {
		return nil
	}
	st, err := s.lock(sid)
	if err != nil {
		return err
	}
	var author storage.Author
	if by := st.releasedBy; by != "" {
		name := by
		if pt := st.participants[by]; pt != nil && pt.joined.GetParticipantJoined().GetDisplayName() != "" {
			name = pt.joined.GetParticipantJoined().GetDisplayName()
		}
		author = storage.Author{Name: name, Email: by + "@office.invalid"}
	}
	st.mu.Unlock()
	res, err := s.rel.opts.Staging.Release(ctx, sid, author)
	if errors.Is(err, storage.ErrNoArea) {
		return nil
	}
	if err != nil {
		return err
	}
	if res.Commit != "" {
		s.rel.opts.Logf("release: session %q moved into the artifact repository in commit %s", sid, res.Commit)
	}
	for _, c := range res.Conflicts {
		s.rel.opts.Logf("release: session %q: %s is not released, the path is taken; it stays in the staging area", c.Session, c.Path)
	}
	return nil
}

// openArea opens the staging area of a session, or adds a reader to it, before the participant
// joins or imports (concept-session-release). The caller holds st.mu.
func (s *Service) openArea(ctx context.Context, sid string, st *state, reader string) error {
	if s.rel == nil || slices.Contains(st.readers, reader) {
		return nil
	}
	readers := append(slices.Clone(st.readers), reader)
	if err := s.rel.opts.Staging.OpenArea(ctx, sid, storage.AreaState{Readers: readers}); err != nil {
		return connect.NewError(connect.CodeUnavailable, "open the staging area of the session: "+err.Error())
	}
	return nil
}

// setDeadline writes the deadline of the release into the staging area when the session ends,
// and starts its timer: the end plus the grace period, or the end itself when no human took part.
// The caller holds st.mu.
func (s *Service) setDeadline(ctx context.Context, sid string, st *state) error {
	if s.rel == nil {
		return nil
	}
	at := s.rel.now()
	if !st.hasHumans() {
		// EndSession releases the session itself (releaseAtEnd).
		return s.writeDeadline(ctx, sid, st, at)
	}
	at = at.Add(s.rel.opts.GracePeriod)
	if err := s.writeDeadline(ctx, sid, st, at); err != nil {
		return err
	}
	s.rel.schedule(sid, at)
	return nil
}

func (s *Service) writeDeadline(ctx context.Context, sid string, st *state, at time.Time) error {
	if err := s.rel.opts.Staging.OpenArea(ctx, sid, storage.AreaState{Readers: st.readers, Deadline: at}); err != nil {
		return connect.NewError(connect.CodeUnavailable, "write the deadline of the release: "+err.Error())
	}
	return nil
}

// markHeld records an objection in the staging area, so the timers stay off after a restart.
func (s *Service) markHeld(ctx context.Context, sid string) error {
	if s.rel == nil {
		return nil
	}
	st, err := s.lock(sid)
	if err != nil {
		return err
	}
	readers := slices.Clone(st.readers)
	st.mu.Unlock()
	a, err := s.rel.opts.Staging.Area(ctx, sid)
	if err != nil && !errors.Is(err, storage.ErrNoArea) {
		return err
	}
	return s.rel.opts.Staging.OpenArea(ctx, sid, storage.AreaState{Readers: readers, Deadline: a.Deadline, Held: true})
}

// releaseState returns the release status of a session.
func (s *Service) releaseState(sid string) (release.State, error) {
	st, err := s.lock(sid)
	if err != nil {
		return release.State{}, err
	}
	defer st.mu.Unlock()
	switch {
	case st.lastID == 0:
		return release.State{}, release.ErrNoSession
	case st.released:
		return release.State{Status: release.Released}, nil
	case st.held:
		return release.State{Status: release.Held}, nil
	case st.ended:
		var at time.Time
		if s.rel != nil {
			at = s.rel.deadline(sid)
		}
		return release.State{Status: release.Waiting, Deadline: at}, nil
	}
	return release.State{Status: release.Open}, nil
}

// releaseStatus is releaseState in the form of the session commands.
func (s *Service) releaseStatus(sid string) (*sessioncommandsv1.ReleaseStatus, error) {
	rs, err := s.releaseState(sid)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error())
	}
	out := &sessioncommandsv1.ReleaseStatus{}
	switch rs.Status {
	case release.Open:
		out.State = sessioncommandsv1.ReleaseState_RELEASE_STATE_OPEN
	case release.Waiting:
		out.State = sessioncommandsv1.ReleaseState_RELEASE_STATE_WAITING
		if !rs.Deadline.IsZero() {
			out.Deadline = timestamppb.New(rs.Deadline)
		}
	case release.Held:
		out.State = sessioncommandsv1.ReleaseState_RELEASE_STATE_HELD
	case release.Released:
		out.State = sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED
	}
	return out, nil
}

// humanOnly refuses an agent: agents never release or object (concept-session-release).
func humanOnly(c Caller) error {
	if c.Kind != sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN {
		return connect.NewError(connect.CodePermissionDenied, "only a human who took part in the session may release it or object; an agent never does")
	}
	return nil
}

// ReleaseSession releases a session for a human who took part (see commands.proto). While a
// staged file quotes a sentence off the record, it refuses with failed_precondition and the
// places and a quote token as the error detail, unless the request accepts exactly that token.
// A refusal appends no event, so the same command with the token releases.
func (s *Service) ReleaseSession(ctx context.Context, req *sessioncommandsv1.ReleaseSessionRequest) (*sessioncommandsv1.ReleaseSessionResponse, error) {
	var quotes []*sessioncommandsv1.Quote
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.SessionReleased}, tookPart: true, check: humanOnly,
		afterEnd: func(st *state) error {
			if st.released {
				return precondition("the session is released already")
			}
			return nil
		},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if !st.ended {
				return nil, precondition("the session has not ended; end it first")
			}
			q, token, err := s.findQuotes(ctx, req.GetSessionId(), st)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnavailable, "search the staged files for quotes: "+err.Error())
			}
			if len(q) > 0 && !hmac.Equal([]byte(req.GetAcceptQuotes()), []byte(token)) {
				return nil, sessioncommandsv1.RefusalError(&sessioncommandsv1.QuoteRefusal{Quotes: q, QuoteToken: token})
			}
			quotes = q
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_SessionReleased{SessionReleased: &sessionstreamv1.SessionReleased{}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	// The release is stored; move the area now. If that fails, the worker tries again, also
	// after a restart.
	if s.rel != nil {
		s.rel.cancel(req.GetSessionId())
		if err := s.moveArea(context.WithoutCancel(ctx), req.GetSessionId()); err != nil {
			s.rel.opts.Logf("release: session %q: %v; trying again", req.GetSessionId(), err)
			s.rel.schedule(req.GetSessionId(), s.rel.now().Add(releaseRetry))
		}
	}
	return &sessioncommandsv1.ReleaseSessionResponse{Result: res, Quotes: quotes}, nil
}

// ObjectToRelease holds the release of a session for a human who took part (see commands.proto).
func (s *Service) ObjectToRelease(ctx context.Context, req *sessioncommandsv1.ObjectToReleaseRequest) (*sessioncommandsv1.ObjectToReleaseResponse, error) {
	res, err := s.run(ctx, command{
		sessionID: req.GetSessionId(), commandID: req.GetCommandId(), participantID: req.GetParticipantId(),
		types: []string{events.ReleaseObjected}, tookPart: true, check: humanOnly,
		afterEnd: func(st *state) error {
			switch {
			case st.released:
				return precondition("the session is released already")
			case st.held:
				return precondition("the session is held already")
			}
			return nil
		},
		build: func(st *state, _ Caller) (*sessionstreamv1.Event, error) {
			if !st.ended {
				return nil, precondition("the session has not ended; an objection holds an ended session")
			}
			return &sessionstreamv1.Event{Payload: &sessionstreamv1.Event_ReleaseObjected{ReleaseObjected: &sessionstreamv1.ReleaseObjected{}}}, nil
		},
	}, false)
	if err != nil {
		return nil, err
	}
	if s.rel != nil {
		s.rel.cancel(req.GetSessionId())
		if err := s.markHeld(context.WithoutCancel(ctx), req.GetSessionId()); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, "record the objection in the staging area: "+err.Error()+"; repeat the command")
		}
	}
	return &sessioncommandsv1.ObjectToReleaseResponse{Result: res}, nil
}

// GetRelease returns the release of a session (see commands.proto): everything to who may read
// the session, and the release commit of a released session to every other member of the tenant.
func (s *Service) GetRelease(ctx context.Context, req *sessioncommandsv1.GetReleaseRequest) (*sessioncommandsv1.GetReleaseResponse, error) {
	sid := req.GetSessionId()
	if sid == "" {
		return nil, invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.StageSessionID != "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot read the release of a session")
	}
	st, err := s.lock(sid)
	if err != nil {
		return nil, err
	}
	exists := st.lastID != 0
	reader := mayRead(caller, st) == nil
	released := st.released
	may := caller.Kind == sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN && st.tookPart(caller.ParticipantID) &&
		st.ended && !st.released
	st.mu.Unlock()
	if !exists || (!reader && !released) {
		return nil, notFound("no such session for you: only who may read a session sees its release before it is released")
	}
	status, err := s.releaseStatus(sid)
	if err != nil {
		return nil, err
	}
	out := &sessioncommandsv1.GetReleaseResponse{Status: status, MayRelease: may}
	if s.rel == nil {
		return out, nil
	}
	if !released {
		paths, err := s.rel.opts.Staging.List(ctx, sid)
		if err != nil && !errors.Is(err, storage.ErrNoArea) {
			return nil, connect.NewError(connect.CodeUnavailable, "list the staged files: "+err.Error())
		}
		out.Staged = paths
		return out, nil
	}
	if reader {
		// The files that a done release kept back because their path is taken. While the area
		// has not moved yet, nothing is kept back.
		a, err := s.rel.opts.Staging.Area(ctx, sid)
		if err != nil && !errors.Is(err, storage.ErrNoArea) {
			return nil, connect.NewError(connect.CodeUnavailable, "read the files that were not released: "+err.Error())
		}
		if a.Release != nil && a.Release.Done {
			out.Unreleased = a.Names
		}
	}
	rc, err := s.rel.opts.Staging.Released(ctx, sid)
	if errors.Is(err, storage.ErrNotReleased) {
		return out, nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, "read the release commit: "+err.Error())
	}
	commit := &sessioncommandsv1.ReleaseCommit{CommitId: rc.ID, Time: timestamppb.New(rc.Time), Paths: rc.Paths}
	if rc.Author != (storage.Author{}) {
		commit.ReleasedBy = &sessioncommandsv1.CommitAuthor{Name: rc.Author.Name, Email: rc.Author.Email}
	}
	for _, a := range rc.CoAuthors {
		commit.Authors = append(commit.Authors, &sessioncommandsv1.CommitAuthor{Name: a.Name, Email: a.Email})
	}
	out.Commit = commit
	return out, nil
}

// persistNow writes a session to the archive and returns when the commit is made. The release
// commands and the events after the end of a session use it: no peer holds such an event.
func (s *Service) persistNow(ctx context.Context, sid string) error {
	if s.arch == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	files, n, err := s.archiveFiles(sid)
	if err != nil {
		return err
	}
	s.archMu.Lock()
	done := s.arch.committed[sid] >= n
	s.archMu.Unlock()
	if done {
		return nil
	}
	if _, err := s.arch.opts.Archive.Commit(ctx, storage.Change{
		Message: fmt.Sprintf("Sessions: %s (%d events)", sid, n), Files: files}); err != nil {
		return err
	}
	s.archMu.Lock()
	if s.arch.committed[sid] < n {
		s.arch.committed[sid] = n
	}
	s.archMu.Unlock()
	return nil
}

// Releases returns the release status of the sessions for the parts of the hub
// (if-session-release).
func (s *Service) Releases() release.Checker { return checker{s} }

type checker struct{ s *Service }

func (c checker) State(_ context.Context, sid string) (release.State, error) {
	if sid == "" {
		return release.State{}, release.ErrNoSession
	}
	return c.s.releaseState(sid)
}

func (c checker) TookPart(_ context.Context, sid, pid string) (bool, error) {
	if sid == "" || pid == "" {
		return false, nil
	}
	st, err := c.s.lock(sid)
	if err != nil {
		return false, err
	}
	defer st.mu.Unlock()
	return st.tookPart(pid), nil
}

// minQuoteWords is the least number of words of a sentence that the search for quotes looks for.
// Shorter sentences, such as "Yes.", would be found everywhere.
const minQuoteWords = 3

var sentenceEnd = regexp.MustCompile(`[.!?]+(\s+|$)`)

// findQuotes returns the places where a staged file of the session quotes a sentence off the
// record word for word (concept-session-release), and the quote token over them. It is a raw text
// search: it knows no file type, ignores case and treats every run of white space as one space. It
// looks for each sentence of an utterance off the record with at least minQuoteWords words, and
// for the whole utterance. It changes nothing. Without quotes, the token is empty. The caller
// holds st.mu.
func (s *Service) findQuotes(ctx context.Context, sid string, st *state) ([]*sessioncommandsv1.Quote, string, error) {
	if s.rel == nil {
		return nil, "", nil
	}
	type needle struct {
		id   uint32
		text string
	}
	var needles []needle
	evs, _, err := s.store.Read(sid, 0)
	if err != nil {
		return nil, "", err
	}
	for _, ev := range evs {
		u := st.utterances[ev.GetId()]
		if ev.GetUtteranceFinal() == nil || u == nil || !u.offTheRecord {
			continue
		}
		text := ev.GetUtteranceFinal().GetText()
		parts := append(sentenceEnd.Split(text, -1), text)
		seen := map[string]bool{}
		for _, p := range parts {
			n, _ := normalize(strings.TrimFunc(p, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
			if len(strings.Fields(n)) < minQuoteWords || seen[n] {
				continue
			}
			seen[n] = true
			needles = append(needles, needle{ev.GetId(), n})
		}
	}
	if len(needles) == 0 {
		return nil, "", nil
	}
	paths, err := s.rel.opts.Staging.List(ctx, sid)
	if errors.Is(err, storage.ErrNoArea) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var out []*sessioncommandsv1.Quote
	quoted := map[*sessioncommandsv1.Quote]string{} // the sentence that a place quotes
	for _, p := range paths {
		files, err := s.rel.opts.Staging.Read(ctx, sid, p)
		if err != nil {
			return nil, "", err
		}
		for _, f := range files {
			if f.Path != p || !utf8.Valid(f.Data) {
				continue
			}
			hay, offsets := normalize(string(f.Data))
			type key struct {
				line uint32
				id   uint32
			}
			found := map[key]bool{}
			for _, nd := range needles {
				for from := 0; from < len(hay); {
					i := strings.Index(hay[from:], nd.text)
					if i < 0 {
						break
					}
					at := from + i
					line := uint32(strings.Count(string(f.Data[:offsets[at]]), "\n") + 1)
					if k := (key{line, nd.id}); !found[k] {
						found[k] = true
						q := &sessioncommandsv1.Quote{Path: f.Path, Line: line, UtteranceEventId: nd.id}
						out = append(out, q)
						quoted[q] = nd.text
					}
					from = at + len(nd.text)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b *sessioncommandsv1.Quote) int {
		if c := strings.Compare(a.GetPath(), b.GetPath()); c != 0 {
			return c
		}
		if a.GetLine() != b.GetLine() {
			return int(a.GetLine()) - int(b.GetLine())
		}
		return int(a.GetUtteranceEventId()) - int(b.GetUtteranceEventId())
	})
	if len(out) == 0 {
		return nil, "", nil
	}
	return out, s.quoteToken(sid, out, quoted), nil
}

// quoteToken returns the quote token of a search: a keyed hash over the session and the exact
// quotes, each with its place and the sentence it quotes. The key comes from the signing key of
// the hub, so nobody can test a guessed sentence against a token.
func (s *Service) quoteToken(sid string, quotes []*sessioncommandsv1.Quote, quoted map[*sessioncommandsv1.Quote]string) string {
	k := sha256.Sum256(append([]byte("office-quote-token\x00"), s.key.Seed()...))
	m := hmac.New(sha256.New, k[:])
	field := func(b []byte) {
		_ = binary.Write(m, binary.BigEndian, uint64(len(b)))
		m.Write(b)
	}
	field([]byte(sid))
	for _, q := range quotes {
		field([]byte(q.GetPath()))
		_ = binary.Write(m, binary.BigEndian, q.GetLine())
		_ = binary.Write(m, binary.BigEndian, q.GetUtteranceEventId())
		field([]byte(quoted[q]))
	}
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// normalize returns text in lower case with every run of white space as one space and without
// space at the start, and for each byte of the result the offset of its rune in text.
func normalize(text string) (string, []int) {
	var b strings.Builder
	var offsets []int
	space := false
	for i, r := range text {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			offsets = append(offsets, i)
			space = false
		}
		n := b.Len()
		b.WriteString(strings.ToLower(string(r)))
		for range b.Len() - n {
			offsets = append(offsets, i)
		}
	}
	return b.String(), offsets
}
