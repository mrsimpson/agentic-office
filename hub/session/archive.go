package session

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/session/api/events"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
)

// Layout of a session in the archive (dec-yaml-git-storage). <name> is ArchiveName(session ID).
const (
	// ArchiveLogFile is the path of the session log: one line per event (package logfile), the
	// raw log with the original text of utterances that are off the record and their marks.
	ArchiveLogFile = "sessions/%s/log.jsonl"
	// ArchiveTreeHeadFile is the path of the signed tree head of the stored log (YAML, package
	// logfile). Its tree size is the number of lines of the log in the same commit.
	ArchiveTreeHeadFile = "sessions/%s/tree-head.yaml"
)

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// ArchiveName returns the name of a session in the paths of the archive. A session ID that is
// safe as a file name is used as it is; any other ID is "=" plus its base64url form without
// padding. This is the same rule as for the local log of a participant.
func ArchiveName(sessionID string) string {
	if safeName.MatchString(sessionID) {
		return sessionID
	}
	return "=" + base64.RawURLEncoding.EncodeToString([]byte(sessionID))
}

// ArchiveOptions configure how the service writes sessions to the archive
// (dec-session-persistence).
type ArchiveOptions struct {
	// Archive stores the sessions (if-archive-storage).
	Archive storage.Archive
	// Interval is the time between two writes while sessions change. It must be positive.
	Interval time.Duration
	// RestoreWait is how long a restored session that has not ended waits for a peer to fill in
	// the events after the last write (dec-restore-from-peers). Commands of the session fail with
	// Unavailable until a peer's copy is accepted or the time is over. 0 means no wait. There is
	// no wait if the hub stopped after a last write of every session.
	RestoreWait time.Duration
	// Logf logs failed writes and the restore. Nil means no logging.
	Logf func(format string, args ...any)
}

// archiver is the state of the background writer. Only the writer goroutine uses it, except
// dirty and wake (under Service.archMu, and a channel).
type archiver struct {
	opts ArchiveOptions
	// lines is the stored form of every session written so far, and how many events it holds.
	lines map[string]*archivedLog
}

type archivedLog struct {
	data  []byte
	count int
}

// markDirty records that a session has new events. On session.ended it wakes the writer, so the
// ended session is written without waiting for the interval. It never blocks.
func (s *Service) markDirty(sessionID, eventType string) {
	if s.wake == nil {
		return
	}
	s.archMu.Lock()
	s.dirty[sessionID] = true
	s.archMu.Unlock()
	if eventType == events.SessionEnded {
		select {
		case s.wake <- struct{}{}:
		default: // a wake is pending; the next write takes this session too
		}
	}
}

// RunArchive writes the changed sessions to the archive until ctx is done
// (dec-session-persistence). Call it once, in its own goroutine, before the service takes
// commands. It writes at every interval and when a session ends, in the background: commands and
// streams never wait for the archive. At most one write is in progress. A write takes all
// sessions that changed since the last write; a write that takes longer than the interval makes
// the next write take everything that came meanwhile, so writes never queue up.
//
// Every write is one commit with, for every changed session, its log (ArchiveLogFile) and the
// tree head signed by the hub over exactly that log (ArchiveTreeHeadFile). A failed write is
// logged and repeated with the next write. When ctx is done, RunArchive lets a running write
// end, makes a last write with a timeout of 30 s and returns.
func (s *Service) RunArchive(ctx context.Context) {
	a := s.arch
	if a == nil {
		return
	}
	t := time.NewTicker(a.opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			last, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			s.writeArchive(last, true)
			cancel()
			return
		case <-t.C:
		case <-s.wake:
		}
		// A write is not cancelled when ctx is done: a killed commit could leave the repository
		// locked. The last write follows it.
		s.writeArchive(context.WithoutCancel(ctx), false)
	}
}

func (s *Service) logf(format string, args ...any) {
	if s.arch != nil && s.arch.opts.Logf != nil {
		s.arch.opts.Logf(format, args...)
	}
}

// writeArchive writes every session that changed since the last write as one commit. The last
// write (final) also records in ArchiveStateFile that the archive is complete, if it wrote every
// changed session.
func (s *Service) writeArchive(ctx context.Context, final bool) {
	s.archMu.Lock()
	dirty := s.dirty
	s.dirty = map[string]bool{}
	s.archMu.Unlock()
	if len(dirty) == 0 && !final {
		return
	}
	ids := make([]string, 0, len(dirty))
	for id := range dirty {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var files []storage.File
	var summary []string
	failed := false
	for _, sid := range ids {
		f, n, err := s.archiveFiles(sid)
		if err != nil {
			s.logf("archive: session %q: %v", sid, err)
			failed = true
			continue
		}
		files = append(files, f...)
		summary = append(summary, fmt.Sprintf("%s (%d events)", sid, n))
	}
	if final && !failed && !s.anyRestoring() {
		files = append(files, storage.File{Path: ArchiveStateFile, Data: marshalArchiveState(true)})
		summary = append(summary, "complete at stop")
	}
	if len(files) == 0 {
		return
	}
	msg := "Sessions: " + strings.Join(summary, ", ")
	if _, err := s.arch.opts.Archive.Commit(ctx, storage.Change{Message: msg, Files: files}); err != nil {
		s.logf("archive: write failed, the next write repeats it: %v", err)
		s.archMu.Lock()
		for _, id := range ids {
			s.dirty[id] = true
		}
		s.archMu.Unlock()
	}
}

// archiveFiles returns the files of one session: the raw log and the signed tree head over it.
// It holds the lock of the session only to read the size of the log and its leaves.
func (s *Service) archiveFiles(sid string) ([]storage.File, int, error) {
	st, err := s.lock(sid)
	if err != nil {
		return nil, 0, err
	}
	n := len(st.leaves)
	head := s.signedHead(sid, st)
	st.mu.Unlock()

	al := s.arch.lines[sid]
	if al == nil {
		al = &archivedLog{}
		s.arch.lines[sid] = al
	}
	if al.count < n {
		evs, _, err := s.store.Read(sid, uint32(al.count))
		if err != nil {
			return nil, 0, err
		}
		if len(evs) < n-al.count {
			return nil, 0, fmt.Errorf("the store has %d events, the state %d", al.count+len(evs), n)
		}
		data := al.data
		for _, ev := range evs[:n-al.count] {
			line, err := logfile.MarshalLine(ev)
			if err != nil {
				return nil, 0, err
			}
			data = append(data, line...)
		}
		al.data, al.count = data, n
	}
	headData, err := logfile.MarshalTreeHead(head)
	if err != nil {
		return nil, 0, err
	}
	name := ArchiveName(sid)
	return []storage.File{
		{Path: fmt.Sprintf(ArchiveLogFile, name), Data: al.data},
		{Path: fmt.Sprintf(ArchiveTreeHeadFile, name), Data: headData},
	}, n, nil
}

// anyRestoring reports whether a session still waits for a peer to fill it in. Then the archive
// is not complete even after a last write.
func (s *Service) anyRestoring() bool {
	s.mu.Lock()
	sts := make([]*state, 0, len(s.sessions))
	for _, st := range s.sessions {
		sts = append(sts, st)
	}
	s.mu.Unlock()
	for _, st := range sts {
		st.mu.Lock()
		r := st.restoring && (st.broken != "" || time.Now().Before(st.restoreUntil))
		st.mu.Unlock()
		if r {
			return true
		}
	}
	return false
}
