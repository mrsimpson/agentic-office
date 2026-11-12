package contract

// Contract tests of the Merkle tree over the session log (dec-merkle-log) through
// if-session-commands. They start the real office-hub and check its tree heads and proofs with the
// verification code of the interface (hub/session/streamapi/merkle), and parts of it by hand.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func (m *meeting) events() []*sessionstreamv1.Event {
	m.t.Helper()
	res, err := m.reader.Stream.GetEvents(m.ctx(), &sessionstreamv1.GetEventsRequest{SessionId: m.sid, Limit: 1000})
	if err != nil {
		m.t.Fatal(err)
	}
	return res.GetEvents()
}

func (m *meeting) head(size uint32) *sessionstreamv1.SignedTreeHead {
	m.t.Helper()
	res, err := m.reader.Commands.GetTreeHead(m.ctx(), &sessioncommandsv1.GetTreeHeadRequest{SessionId: m.sid, TreeSize: size})
	if err != nil {
		m.t.Fatalf("tree head of size %d: %v", size, err)
	}
	return res.GetTreeHead()
}

func hubKey(t *testing.T, s interface {
	GetHubKey(context.Context, *sessioncommandsv1.GetHubKeyRequest) (*sessioncommandsv1.GetHubKeyResponse, error)
}) ed25519.PublicKey {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.GetHubKey(ctx, &sessioncommandsv1.GetHubKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetPublicKey()) != ed25519.PublicKeySize {
		t.Fatalf("hub key has %d bytes", len(res.GetPublicKey()))
	}
	return res.GetPublicKey()
}

func wantVerifyError(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, merkle.ErrVerify) {
		t.Errorf("%s: want a verification error, got %v", what, err)
	}
}

func sha(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// The tree head of a known log has its size and the RFC 9162 root hash of its events, and its
// signature verifies with the key of the hub.
func TestMerkleTreeHeadOfKnownLog(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "First.")
	m.say(ada, "Second.")
	evs := m.events()
	if len(evs) != 3 {
		t.Fatalf("got %d events", len(evs))
	}
	key := hubKey(t, m.reader.Commands)

	head := m.head(0)
	if head.GetSessionId() != m.sid || head.GetTreeSize() != 3 || !bytes.Equal(head.GetPublicKey(), key) {
		t.Fatalf("tree head: %v", head)
	}
	// By hand (RFC 9162): leaf = SHA-256(0x00 || deterministic encoding of the event),
	// root of three leaves = node(node(l1, l2), l3), node = SHA-256(0x01 || left || right).
	var l [][]byte
	for _, ev := range evs {
		b, err := proto.MarshalOptions{Deterministic: true}.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		l = append(l, sha([]byte{0}, b))
	}
	root := sha([]byte{1}, sha([]byte{1}, l[0], l[1]), l[2])
	if !bytes.Equal(head.GetRootHash(), root) {
		t.Fatalf("root hash %x, want %x", head.GetRootHash(), root)
	}
	if err := merkle.VerifyEvents(evs, head, key); err != nil {
		t.Fatal(err)
	}

	// A head for a smaller size covers the first events only.
	two := m.head(2)
	if two.GetTreeSize() != 2 || !bytes.Equal(two.GetRootHash(), sha([]byte{1}, l[0], l[1])) {
		t.Fatalf("tree head of size 2: %v", two)
	}
	if err := merkle.VerifyEvents(evs[:2], two, key); err != nil {
		t.Fatal(err)
	}

	// Forged heads fail.
	forged := proto.Clone(head).(*sessionstreamv1.SignedTreeHead)
	forged.TreeSize = 2
	wantVerifyError(t, "head with another size", merkle.VerifyTreeHead(forged, key))
	forged = proto.Clone(head).(*sessionstreamv1.SignedTreeHead)
	forged.SessionId = "other"
	wantVerifyError(t, "head with another session", merkle.VerifyTreeHead(forged, key))
	other, _, _ := ed25519.GenerateKey(nil)
	wantVerifyError(t, "head checked with another key", merkle.VerifyTreeHead(head, other))
	wantVerifyError(t, "copy with a missing event", merkle.VerifyEvents(evs[:2], head, key))

	// A changed event fails.
	changed := proto.Clone(evs[1]).(*sessionstreamv1.Event)
	changed.GetUtteranceFinal().Text = "Frist."
	wantVerifyError(t, "copy with a changed event",
		merkle.VerifyEvents([]*sessionstreamv1.Event{evs[0], changed, evs[2]}, head, key))

	// An empty session has a head of size 0.
	empty, err := m.reader.Commands.GetTreeHead(m.ctx(), &sessioncommandsv1.GetTreeHeadRequest{SessionId: "s-" + coreapi.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyEvents(nil, empty.GetTreeHead(), key); err != nil || empty.GetTreeHead().GetTreeSize() != 0 {
		t.Fatalf("empty session: %v %v", empty.GetTreeHead(), err)
	}
}

// A consistency proof shows that a longer log extends a shorter one; a forged proof or a forged
// head fails.
func TestMerkleConsistencyProof(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	for i := 0; i < 6; i++ {
		m.say(ada, "Sentence.")
	}
	key := hubKey(t, m.reader.Commands)
	last := m.head(0)
	if last.GetTreeSize() != 7 {
		t.Fatalf("tree size %d", last.GetTreeSize())
	}
	for first := uint32(1); first <= 7; first++ {
		older := m.head(first)
		res, err := m.reader.Commands.GetConsistencyProof(m.ctx(), &sessioncommandsv1.GetConsistencyProofRequest{
			SessionId: m.sid, FirstSize: first})
		if err != nil {
			t.Fatal(err)
		}
		if res.GetFirstSize() != first || res.GetSecondSize() != 7 {
			t.Fatalf("sizes %d %d", res.GetFirstSize(), res.GetSecondSize())
		}
		if err := merkle.VerifyTreeHeadConsistency(older, last, res.GetHashes(), key); err != nil {
			t.Fatalf("consistency %d -> 7: %v", first, err)
		}
		if first == 7 {
			continue
		}
		for i := range res.GetHashes() {
			forged := proto.Clone(res).(*sessioncommandsv1.GetConsistencyProofResponse)
			forged.Hashes[i][0] ^= 1
			wantVerifyError(t, "forged consistency proof", merkle.VerifyTreeHeadConsistency(older, last, forged.GetHashes(), key))
		}
		wantVerifyError(t, "proof without its last hash",
			merkle.VerifyTreeHeadConsistency(older, last, res.GetHashes()[:len(res.GetHashes())-1], key))
		// A log that does not extend the older one: the root of the older head is changed.
		wantVerifyError(t, "forged older root", merkle.VerifyConsistency(uint64(first), 7,
			sha([]byte("forged")), last.GetRootHash(), res.GetHashes()))
	}

	// Between two older sizes.
	res, err := m.reader.Commands.GetConsistencyProof(m.ctx(), &sessioncommandsv1.GetConsistencyProofRequest{
		SessionId: m.sid, FirstSize: 3, SecondSize: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyTreeHeadConsistency(m.head(3), m.head(5), res.GetHashes(), key); err != nil {
		t.Fatal(err)
	}
	wantVerifyError(t, "proof for other sizes", merkle.VerifyTreeHeadConsistency(m.head(2), m.head(5), res.GetHashes(), key))

	ctx := m.ctx()
	_, err = m.reader.Commands.GetConsistencyProof(ctx, &sessioncommandsv1.GetConsistencyProofRequest{SessionId: m.sid, SecondSize: 5})
	wantCode(t, "first_size 0", err, connect.CodeInvalidArgument)
	_, err = m.reader.Commands.GetConsistencyProof(ctx, &sessioncommandsv1.GetConsistencyProofRequest{SessionId: m.sid, FirstSize: 6, SecondSize: 5})
	wantCode(t, "first_size greater than second_size", err, connect.CodeInvalidArgument)
	_, err = m.reader.Commands.GetConsistencyProof(ctx, &sessioncommandsv1.GetConsistencyProofRequest{SessionId: m.sid, FirstSize: 1, SecondSize: 8})
	wantCode(t, "second_size beyond the log", err, connect.CodeOutOfRange)
	_, err = m.reader.Commands.GetTreeHead(ctx, &sessioncommandsv1.GetTreeHeadRequest{SessionId: m.sid, TreeSize: 8})
	wantCode(t, "tree_size beyond the log", err, connect.CodeOutOfRange)
	_, err = m.srv.Anonymous(t).Commands.GetTreeHead(ctx, &sessioncommandsv1.GetTreeHeadRequest{SessionId: m.sid})
	wantCode(t, "tree head without a token", err, connect.CodeUnauthenticated)
}

// An inclusion proof shows that one event is in the tree; a changed event or a wrong ID fails.
func TestMerkleInclusionProof(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	for i := 0; i < 4; i++ {
		m.say(ada, "Sentence.")
	}
	key := hubKey(t, m.reader.Commands)
	evs := m.events()
	for _, size := range []uint32{0, 3} {
		head := m.head(size)
		for _, ev := range evs[:head.GetTreeSize()] {
			res, err := m.reader.Commands.GetInclusionProof(m.ctx(), &sessioncommandsv1.GetInclusionProofRequest{
				SessionId: m.sid, EventId: ev.GetId(), TreeSize: size})
			if err != nil {
				t.Fatal(err)
			}
			if res.GetEventId() != ev.GetId() || res.GetTreeSize() != head.GetTreeSize() {
				t.Fatalf("inclusion proof: %v", res)
			}
			if err := merkle.VerifyEventInclusion(ev, res.GetHashes(), head, key); err != nil {
				t.Fatalf("event %d in tree of %d: %v", ev.GetId(), head.GetTreeSize(), err)
			}
			changed := proto.Clone(ev).(*sessionstreamv1.Event)
			changed.ActorParticipantId = "mallory"
			wantVerifyError(t, "changed event", merkle.VerifyEventInclusion(changed, res.GetHashes(), head, key))
			moved := proto.Clone(ev).(*sessionstreamv1.Event)
			moved.Id = ev.GetId()%head.GetTreeSize() + 1
			if moved.Id != ev.GetId() {
				wantVerifyError(t, "event with another ID", merkle.VerifyEventInclusion(moved, res.GetHashes(), head, key))
			}
		}
	}
	_, err := m.reader.Commands.GetInclusionProof(m.ctx(), &sessioncommandsv1.GetInclusionProofRequest{SessionId: m.sid, EventId: 4, TreeSize: 3})
	wantCode(t, "event beyond the tree size", err, connect.CodeOutOfRange)
	_, err = m.reader.Commands.GetInclusionProof(m.ctx(), &sessioncommandsv1.GetInclusionProofRequest{SessionId: m.sid})
	wantCode(t, "event_id 0", err, connect.CodeInvalidArgument)
}

// A copy with a placeholder for an utterance off the record still verifies, and so does the
// original copy. A placeholder cannot carry another leaf hash, and no other event can claim one.
func TestMerklePlaceholderStillVerifies(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Public sentence.")
	secret := m.say(ada, "The budget is ten million.")
	original := m.read(0, 3)
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: ada, UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	key := hubKey(t, m.reader.Commands)
	head := m.head(0)
	copyWith := m.events()
	ph := copyWith[secret.GetId()-1]
	if !ph.GetUtteranceFinal().GetOffTheRecord() || ph.GetUtteranceFinal().GetText() != "" || len(ph.GetLeafHash()) != merkle.HashSize {
		t.Fatalf("placeholder: %v", ph)
	}
	if err := merkle.VerifyEvents(copyWith, head, key); err != nil {
		t.Fatalf("copy with a placeholder: %v", err)
	}
	full := append(append([]*sessionstreamv1.Event{}, original...), copyWith[3])
	if err := merkle.VerifyEvents(full, head, key); err != nil {
		t.Fatalf("copy with the original: %v", err)
	}
	res, err := m.reader.Commands.GetInclusionProof(m.ctx(), &sessioncommandsv1.GetInclusionProofRequest{SessionId: m.sid, EventId: secret.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyEventInclusion(ph, res.GetHashes(), head, key); err != nil {
		t.Fatalf("inclusion of the placeholder: %v", err)
	}

	forged := proto.Clone(ph).(*sessionstreamv1.Event)
	forged.LeafHash[0] ^= 1
	wantVerifyError(t, "placeholder with another leaf hash",
		merkle.VerifyEvents([]*sessionstreamv1.Event{copyWith[0], copyWith[1], forged, copyWith[3]}, head, key))
	claim := proto.Clone(copyWith[1]).(*sessionstreamv1.Event)
	claim.LeafHash = ph.GetLeafHash()
	wantVerifyError(t, "event that is not a placeholder with a leaf hash",
		merkle.VerifyEvents([]*sessionstreamv1.Event{copyWith[0], claim, ph, copyWith[3]}, head, key))
	// Hiding a sentence that is not off the record: the placeholder needs the original leaf hash,
	// so a placeholder with the hash of another event fails.
	hidden := proto.Clone(copyWith[1]).(*sessionstreamv1.Event)
	f := hidden.GetUtteranceFinal()
	f.Text, f.OffTheRecord = "", true
	hidden.LeafHash = ph.GetLeafHash()
	wantVerifyError(t, "placeholder with the leaf hash of another event",
		merkle.VerifyEvents([]*sessionstreamv1.Event{copyWith[0], hidden, ph, copyWith[3]}, head, key))
}

// session.ended carries the signed tree head of the log before it. The signature verifies with
// the key of the hub, also after a restart of the hub: the key stays the same.
func TestMerkleSessionEndedAndRestart(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "Done.")
	ended := m.end(ada)
	log := m.readAll(0)
	key := hubKey(t, m.reader.Commands)
	head := ended.GetSessionEnded().GetTreeHead()
	if head.GetTreeSize() != ended.GetId()-1 {
		t.Fatalf("tree head in session.ended: %v", head)
	}
	if err := merkle.VerifyEvents(log[:len(log)-1], head, key); err != nil {
		t.Fatalf("head in session.ended: %v", err)
	}
	final := m.head(0)
	if err := merkle.VerifyEvents(log, final, key); err != nil {
		t.Fatalf("head with session.ended: %v", err)
	}

	m.srv.Kill()
	srv := contracttest.StartHubIn(t, m.srv.DataDir, m.srv.AdminKey, nil)
	reader := srv.WithKey(t, m.reader.Key)
	after := hubKey(t, reader.Commands)
	if !bytes.Equal(after, key) {
		t.Fatalf("hub key changed after a restart: %x, was %x", after, key)
	}
	for _, h := range []*sessionstreamv1.SignedTreeHead{head, final} {
		if err := merkle.VerifyEvents(log[:h.GetTreeSize()], h, after); err != nil {
			t.Fatalf("head of size %d after a restart: %v", h.GetTreeSize(), err)
		}
	}
}
