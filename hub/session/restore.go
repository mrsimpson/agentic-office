package session

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
	"github.com/mrsimpson/agentic-office/hub/session/api/logfile"
	"github.com/mrsimpson/agentic-office/hub/session/api/merkle"
)

// ArchiveStateFile is the path of the state of the session logs in the archive (YAML). complete
// is true when the hub stopped after a last write of every session, so the archive holds every
// event and no peer needs to fill in. The hub sets it to false when it starts. No session name
// starts with "+" (see ArchiveName), so the file never collides with a session.
const ArchiveStateFile = "sessions/+state.yaml"

type archiveState struct {
	Complete bool `yaml:"complete"`
}

func marshalArchiveState(complete bool) []byte {
	b, err := yaml.Marshal(archiveState{Complete: complete})
	if err != nil {
		panic("session: marshal the archive state: " + err.Error())
	}
	return append([]byte("# Written by the meeting session of the office hub. complete is true when the hub stopped\n"+
		"# after a last write of every session.\n"), b...)
}

// pushLeaf adds the leaf hash of the next event to the tree.
func (st *state) pushLeaf(h []byte) {
	// A new leaf merges with every perfect subtree of the same size: one per trailing 1 bit of
	// the old size.
	r := h
	for i := len(st.leaves); i&1 == 1; i >>= 1 {
		r = merkle.NodeHash(st.frontier[len(st.frontier)-1], r)
		st.frontier = st.frontier[:len(st.frontier)-1]
	}
	st.frontier = append(st.frontier, r)
	st.leaves = append(st.leaves, h)
	st.head = nil
}

// root returns the root hash of the current log (RFC 9162): the perfect subtrees folded from
// the right.
func (st *state) root() []byte {
	if len(st.frontier) == 0 {
		return merkle.RootHash(nil)
	}
	r := st.frontier[len(st.frontier)-1]
	for i := len(st.frontier) - 2; i >= 0; i-- {
		r = merkle.NodeHash(st.frontier[i], r)
	}
	return r
}

// signedHead returns the signed tree head of the current log of a session. The caller holds
// st.mu. The head is computed once per size.
func (s *Service) signedHead(sid string, st *state) *sessionv1.SignedTreeHead {
	if st.head == nil {
		n, root := uint64(len(st.leaves)), st.root()
		st.head = &sessionv1.SignedTreeHead{SessionId: sid, TreeSize: uint32(n), RootHash: root,
			Signature: ed25519.Sign(s.key, merkle.TreeHeadMessage(sid, n, root)),
			PublicKey: s.key.Public().(ed25519.PublicKey)}
	}
	return st.head
}

// currentHead returns the signed head of the log of a session if the log still has n events,
// else nil.
func (s *Service) currentHead(sid string, n int) *sessionv1.SignedTreeHead {
	st, err := s.lock(sid)
	if err != nil {
		return nil
	}
	defer st.mu.Unlock()
	if len(st.leaves) != n {
		return nil
	}
	return s.signedHead(sid, st)
}

// checkRestored fails while the hub waits for peers to fill in a restored session. The caller
// holds st.mu.
func (st *state) checkRestored() error {
	if !st.restoring {
		return nil
	}
	if st.broken != "" {
		return connect.NewError(connect.CodeDataLoss, "the stored log of the session does not verify ("+st.broken+
			"); a peer must offer its copy")
	}
	if !time.Now().Before(st.restoreUntil) {
		st.restoring = false
		return nil
	}
	return connect.NewError(connect.CodeUnavailable,
		"the hub restores the session after a restart and waits for a peer to fill in its copy; try again")
}

// sessionIDOf returns the session ID of a name in the paths of the archive (see ArchiveName).
func sessionIDOf(name string) string {
	if rest, ok := strings.CutPrefix(name, "="); ok {
		if b, err := base64.RawURLEncoding.DecodeString(rest); err == nil {
			return string(b)
		}
	}
	return name
}

// restore reads every session log and its signed tree head back from the archive
// (dec-restore-from-peers). Every log must verify against its head with the key of the hub
// before it is used. A log that verifies is the start of the session: its events keep their
// IDs, and the next write appends to the stored lines. A session that has not ended may have
// lost the events after the last write: unless the archive is complete (the hub stopped after a
// last write), commands wait up to RestoreWait for a peer to offer its copy (OfferLog). A log
// that does not verify is not used; its session takes no commands until a peer offers a copy.
func (s *Service) restore(ctx context.Context) error {
	a := s.arch
	files, err := a.opts.Archive.Read(ctx, "sessions")
	if err != nil {
		return fmt.Errorf("session: read the sessions from the archive: %w", err)
	}
	complete := false
	type stored struct{ log, head []byte }
	byName := map[string]*stored{}
	var names []string
	for _, f := range files {
		if f.Path == ArchiveStateFile {
			var as archiveState
			if err := yaml.Unmarshal(f.Data, &as); err != nil {
				s.logf("restore: %s: %v; peers may fill in every session", ArchiveStateFile, err)
			}
			complete = as.Complete
			continue
		}
		dir, file := path.Split(f.Path)
		name := path.Base(dir)
		if path.Dir(strings.TrimSuffix(dir, "/")) != "sessions" {
			continue
		}
		st := byName[name]
		if st == nil {
			st = &stored{}
			byName[name] = st
			names = append(names, name)
		}
		switch file {
		case path.Base(ArchiveLogFile):
			st.log = f.Data
		case path.Base(ArchiveTreeHeadFile):
			st.head = f.Data
		}
	}
	until := time.Now().Add(a.opts.RestoreWait)
	var restored, waiting, broken int
	for _, name := range names {
		f := byName[name]
		sid, st, err := s.restoreOne(name, f.log, f.head)
		if err != nil {
			s.logf("restore: session %q: %v; it takes no commands until a peer offers its copy", sid, err)
			s.sessions[sid] = &state{participants: map[string]*participant{}, utterances: map[uint32]*utterance{},
				loaded: true, restoring: true, broken: err.Error()}
			broken++
			continue
		}
		if !st.ended && !complete && a.opts.RestoreWait > 0 {
			st.restoring, st.restoreUntil = true, until
			waiting++
		}
		s.sessions[sid] = st
		a.lines[sid] = &archivedLog{data: f.log, count: len(st.leaves)}
		restored++
	}
	s.logf("restore: %d sessions from the archive, %d wait up to %s for peers, %d do not verify",
		restored, waiting, a.opts.RestoreWait, broken)
	if complete {
		// From now on the archive may miss events again until the next stop.
		if _, err := a.opts.Archive.Commit(ctx, storage.Change{Message: "Sessions: the hub started",
			Files: []storage.File{{Path: ArchiveStateFile, Data: marshalArchiveState(false)}}}); err != nil {
			return fmt.Errorf("session: write %s: %w", ArchiveStateFile, err)
		}
	}
	return nil
}

// restoreOne verifies one stored session and loads it into the store and a new state.
func (s *Service) restoreOne(name string, logData, headData []byte) (string, *state, error) {
	sid := sessionIDOf(name)
	if headData == nil || logData == nil {
		return sid, nil, fmt.Errorf("the log or its tree head is missing")
	}
	head, err := logfile.UnmarshalTreeHead(headData)
	if err != nil {
		return sid, nil, fmt.Errorf("read the tree head: %w", err)
	}
	if head.GetSessionId() != "" {
		sid = head.GetSessionId()
	}
	if ArchiveName(sid) != name {
		return sid, nil, fmt.Errorf("the tree head is for session %q, not for %s", sid, name)
	}
	evs, err := logfile.UnmarshalLines(logData)
	if err != nil {
		return sid, nil, fmt.Errorf("read the log: %w", err)
	}
	if err := merkle.VerifyEvents(evs, head, s.key.Public().(ed25519.PublicKey)); err != nil {
		return sid, nil, err
	}
	st := &state{participants: map[string]*participant{}, utterances: map[uint32]*utterance{}, loaded: true}
	for _, ev := range evs {
		stored, err := s.store.Append(sid, ev)
		if err != nil {
			return sid, nil, err
		}
		if err := st.apply(stored); err != nil {
			return sid, nil, err
		}
	}
	return sid, st, nil
}

// OfferLog takes the missing events of a session from the copy of a peer
// (dec-restore-from-peers). See the contract in session.proto.
//
// The copy must prove itself with a tree head signed by the hub key: the hub signs a head with
// every batch of the session stream, so a peer holds a head over its whole copy, also over the
// events the hub lost because it never wrote them to the archive. The hub checks the signature,
// that its own events up to after_event_id and the offered events give the root hash of that
// head, and that its own log and the copy are consistent (a consistency proof between its
// current head and the head of the copy). Only then it appends the events it misses, with their
// IDs. A copy that is not longer is a no-op.
func (s *Service) OfferLog(ctx context.Context, req *sessionv1.OfferLogRequest) (*sessionv1.OfferLogResponse, error) {
	sid := req.GetSessionId()
	if sid == "" {
		return nil, invalid("session_id is required")
	}
	caller, err := s.auth.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.ParticipantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, "a stage link cannot offer a copy")
	}
	head := req.GetTreeHead()
	if head == nil {
		return nil, invalid("tree_head is required")
	}
	if head.GetSessionId() != sid {
		return nil, invalid("tree_head is for another session")
	}
	if err := merkle.VerifyTreeHead(head, s.key.Public().(ed25519.PublicKey)); err != nil {
		return nil, invalid("tree_head: " + err.Error())
	}
	m, after := int(head.GetTreeSize()), int(req.GetAfterEventId())
	if after > m {
		return nil, invalid("after_event_id is beyond tree_head.tree_size")
	}
	if len(req.GetEvents()) != m-after {
		return nil, invalid(fmt.Sprintf("want the events %d to %d, got %d events", after+1, m, len(req.GetEvents())))
	}

	st, err := s.lock(sid)
	if err != nil {
		return nil, err
	}
	defer st.mu.Unlock()
	n := len(st.leaves)
	if after > n {
		return nil, connect.NewError(connect.CodeOutOfRange,
			fmt.Sprintf("after_event_id is beyond the last event of the hub (%d)", n))
	}
	leaves := make([][]byte, 0, m)
	leaves = append(leaves, st.leaves[:after]...)
	for i, ev := range req.GetEvents() {
		if ev.GetId() != uint32(after+i+1) {
			return nil, invalid(fmt.Sprintf("event at position %d has ID %d, not %d", i+1, ev.GetId(), after+i+1))
		}
		if ev.GetSessionId() != sid {
			return nil, invalid(fmt.Sprintf("event %d belongs to another session", ev.GetId()))
		}
		h, err := merkle.EventLeafHash(ev)
		if err != nil {
			return nil, invalid(err.Error())
		}
		leaves = append(leaves, h)
	}
	if !bytes.Equal(merkle.RootHash(leaves), head.GetRootHash()) {
		return nil, invalid("the events do not give the root hash of tree_head")
	}
	// The log of the hub and the copy must be consistent: the shorter one is a prefix of the
	// longer one. The proof is between the current head of the hub and the head of the copy.
	if n > 0 && m > 0 {
		own := s.signedHead(sid, st).GetRootHash()
		var err error
		if m >= n {
			err = merkle.VerifyConsistency(uint64(n), uint64(m), own, head.GetRootHash(), merkle.ConsistencyProof(leaves, n))
		} else {
			err = merkle.VerifyConsistency(uint64(m), uint64(n), head.GetRootHash(), own, merkle.ConsistencyProof(st.leaves, m))
		}
		if err != nil {
			return nil, precondition("the copy does not extend the log of the hub: " + err.Error())
		}
	}
	if m <= n {
		return &sessionv1.OfferLogResponse{TreeHead: s.signedHead(sid, st)}, nil
	}
	for _, ev := range req.GetEvents()[n-after:] {
		stored, err := s.store.Append(sid, ev)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "append to session log: "+err.Error())
		}
		if err := st.apply(stored); err != nil {
			return nil, err
		}
		s.markDirty(sid, stored.GetType())
	}
	st.restoring, st.broken = false, ""
	s.logf("restore: session %q: %s filled in the events %d to %d", sid, caller.ParticipantID, n+1, m)
	return &sessionv1.OfferLogResponse{Accepted: true, TreeHead: s.signedHead(sid, st)}, nil
}
