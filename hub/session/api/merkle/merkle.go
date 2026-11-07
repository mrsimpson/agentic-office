// Package merkle is the Merkle tree over a session log (dec-merkle-log). It belongs to the session
// interface (if-session-commands, if-session-stream), which owns the event types, so the hub and
// every client use the same code to build and to check a tree.
//
// The tree follows RFC 9162 (Certificate Transparency 2.0), section 2.1:
//
//   - The leaf hash is SHA-256(0x00 || leaf). A node hash is SHA-256(0x01 || left || right). The
//     root of an empty tree is SHA-256 of nothing.
//   - The leaves are the events of a session in the order of their IDs: event n is leaf n-1.
//   - The leaf of an event is its deterministic binary Protobuf encoding (proto.MarshalOptions
//     with Deterministic set) with leaf_hash empty. The hub hashes the event as it is in the log,
//     so the leaf of an utterance covers its original text.
//   - A placeholder (an utterance.final with off_the_record set) carries the leaf hash of the
//     original in leaf_hash. EventLeafHash uses it instead of hashing the placeholder, so a copy
//     with placeholders still verifies (dec-off-the-record). A placeholder cannot change what the
//     tree proves: it can only hide the text.
//   - The hub signs a tree head with Ed25519 over TreeHeadMessage: a fixed prefix, the schema of
//     the session, the session ID, the tree size and the root hash.
//   - A session belongs to one schema (dec-session-schema, sessionv1.Schema). The leaves depend on
//     the encoding of the events, so this code checks only logs of the schema it knows: every
//     check of a tree head fails with ErrSchema for a head of another schema.
//
// The client side is VerifyTreeHead, VerifyEvents, VerifyInclusion, VerifyEventInclusion and
// VerifyConsistency. The hub side is RootHash, InclusionProof, ConsistencyProof, SignTreeHead and
// VerifyTreeHeadSignature.
package merkle

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"

	"google.golang.org/protobuf/proto"

	sessionv1 "github.com/mrsimpson/agentic-office/hub/session/api"
)

// HashSize is the size of every hash in the tree.
const HashSize = sha256.Size

// TreeHeadPrefix starts the message that the hub signs for a tree head. The prefix keeps the
// signature apart from every other use of the key.
const TreeHeadPrefix = "agentic-office/session/v1/tree-head\x00"

// ErrVerify is wrapped by every error of a check that fails.
var ErrVerify = errors.New("merkle: verification failed")

// ErrSchema is wrapped, together with ErrVerify, by the error of a check of a tree head with
// another schema than sessionv1.Schema.
var ErrSchema = errors.New("the log has another schema")

func fail(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrVerify, fmt.Sprintf(format, a...))
}

// LeafHash returns SHA-256(0x00 || leaf).
func LeafHash(leaf []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0})
	h.Write(leaf)
	return h.Sum(nil)
}

// NodeHash returns SHA-256(0x01 || left || right).
func NodeHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{1})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// EventLeaf returns the leaf of an event: its deterministic Protobuf encoding with leaf_hash
// empty. It fails for a placeholder, whose original text is gone.
func EventLeaf(ev *sessionv1.Event) ([]byte, error) {
	if ev.GetUtteranceFinal().GetOffTheRecord() {
		return nil, fail("event %d is a placeholder; its leaf is the original", ev.GetId())
	}
	if len(ev.GetLeafHash()) > 0 {
		ev = proto.Clone(ev).(*sessionv1.Event)
		ev.LeafHash = nil
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(ev)
}

// EventLeafHash returns the leaf hash of an event. For a placeholder it returns the leaf hash
// the placeholder carries. Any other event with a leaf_hash, or a placeholder without one,
// fails.
func EventLeafHash(ev *sessionv1.Event) ([]byte, error) {
	if ev.GetUtteranceFinal().GetOffTheRecord() {
		if len(ev.GetLeafHash()) != HashSize {
			return nil, fail("placeholder %d has no leaf hash of %d bytes", ev.GetId(), HashSize)
		}
		return bytes.Clone(ev.GetLeafHash()), nil
	}
	if len(ev.GetLeafHash()) > 0 {
		return nil, fail("event %d carries a leaf hash but is not a placeholder", ev.GetId())
	}
	leaf, err := EventLeaf(ev)
	if err != nil {
		return nil, err
	}
	return LeafHash(leaf), nil
}

// split returns the largest power of two smaller than n (n >= 2).
func split(n int) int { return 1 << (bits.Len(uint(n-1)) - 1) }

// RootHash returns the root hash of the tree over the leaf hashes (MTH in RFC 9162).
func RootHash(leaves [][]byte) []byte {
	switch n := len(leaves); n {
	case 0:
		h := sha256.Sum256(nil)
		return h[:]
	case 1:
		return bytes.Clone(leaves[0])
	default:
		k := split(n)
		return NodeHash(RootHash(leaves[:k]), RootHash(leaves[k:]))
	}
}

// InclusionProof returns the audit path of leaf index in the tree over leaves (PATH in
// RFC 9162). index must be smaller than len(leaves).
func InclusionProof(leaves [][]byte, index int) [][]byte {
	n := len(leaves)
	if n <= 1 {
		return nil
	}
	k := split(n)
	if index < k {
		return append(InclusionProof(leaves[:k], index), RootHash(leaves[k:]))
	}
	return append(InclusionProof(leaves[k:], index-k), RootHash(leaves[:k]))
}

// ConsistencyProof returns the proof that the tree over leaves extends the tree over its first m
// leaves (PROOF in RFC 9162). It needs 1 <= m <= len(leaves); for m == len(leaves) it is empty.
func ConsistencyProof(leaves [][]byte, m int) [][]byte {
	return subproof(m, leaves, true)
}

func subproof(m int, leaves [][]byte, complete bool) [][]byte {
	n := len(leaves)
	if m == n {
		if complete {
			return nil
		}
		return [][]byte{RootHash(leaves)}
	}
	k := split(n)
	if m <= k {
		return append(subproof(m, leaves[:k], complete), RootHash(leaves[k:]))
	}
	return append(subproof(m-k, leaves[k:], false), RootHash(leaves[:k]))
}

// VerifyInclusion checks the audit path of the leaf at index in a tree of size leaves with the
// given root hash (RFC 9162, section 2.1.3.2).
func VerifyInclusion(index, size uint64, leafHash []byte, proof [][]byte, root []byte) error {
	if index >= size {
		return fail("leaf index %d is not in a tree of size %d", index, size)
	}
	fn, sn, r := index, size-1, leafHash
	for _, p := range proof {
		if sn == 0 {
			return fail("inclusion proof is too long")
		}
		if fn&1 == 1 || fn == sn {
			r = NodeHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = NodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 {
		return fail("inclusion proof is too short")
	}
	if !bytes.Equal(r, root) {
		return fail("inclusion proof does not lead to the root hash")
	}
	return nil
}

// VerifyConsistency checks that the tree of second leaves with secondRoot extends the tree of
// first leaves with firstRoot (RFC 9162, section 2.1.4.2). It needs 1 <= first <= second.
func VerifyConsistency(first, second uint64, firstRoot, secondRoot []byte, proof [][]byte) error {
	switch {
	case first == 0 || first > second:
		return fail("sizes %d and %d cannot be checked", first, second)
	case first == second:
		if len(proof) != 0 {
			return fail("consistency proof for equal sizes must be empty")
		}
		if !bytes.Equal(firstRoot, secondRoot) {
			return fail("root hashes of equal sizes differ")
		}
		return nil
	case len(proof) == 0:
		return fail("consistency proof is empty")
	}
	if first&(first-1) == 0 {
		proof = append([][]byte{firstRoot}, proof...)
	}
	fn, sn := first-1, second-1
	for fn&1 == 1 {
		fn >>= 1
		sn >>= 1
	}
	fr, sr := proof[0], proof[0]
	for _, c := range proof[1:] {
		if sn == 0 {
			return fail("consistency proof is too long")
		}
		if fn&1 == 1 || fn == sn {
			fr = NodeHash(c, fr)
			sr = NodeHash(c, sr)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			sr = NodeHash(sr, c)
		}
		fn >>= 1
		sn >>= 1
	}
	if sn != 0 {
		return fail("consistency proof is too short")
	}
	if !bytes.Equal(fr, firstRoot) {
		return fail("consistency proof does not lead to the first root hash")
	}
	if !bytes.Equal(sr, secondRoot) {
		return fail("consistency proof does not lead to the second root hash")
	}
	return nil
}

// TreeHeadMessage returns the bytes the hub signs for a tree head: TreeHeadPrefix, the length
// of the schema (4 bytes, big endian), the schema, the length of the session ID (4 bytes, big
// endian), the session ID, the tree size (8 bytes, big endian) and the root hash.
func TreeHeadMessage(schema, sessionID string, size uint64, root []byte) []byte {
	b := make([]byte, 0, len(TreeHeadPrefix)+4+len(schema)+4+len(sessionID)+8+len(root))
	b = append(b, TreeHeadPrefix...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(schema)))
	b = append(b, schema...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(sessionID)))
	b = append(b, sessionID...)
	b = binary.BigEndian.AppendUint64(b, size)
	return append(b, root...)
}

// SignTreeHead returns the signed tree head of a session of the given schema over the leaf
// hashes.
func SignTreeHead(key ed25519.PrivateKey, schema, sessionID string, leaves [][]byte) *sessionv1.SignedTreeHead {
	root := RootHash(leaves)
	return &sessionv1.SignedTreeHead{
		SessionId: sessionID, Schema: schema, TreeSize: uint32(len(leaves)), RootHash: root,
		Signature: ed25519.Sign(key, TreeHeadMessage(schema, sessionID, uint64(len(leaves)), root)),
		PublicKey: key.Public().(ed25519.PublicKey),
	}
}

// VerifyTreeHead checks a tree head with the hub key the caller trusts: its signature, and that
// it has the schema of this code (sessionv1.Schema). It fails if the head names another key. A
// head of another schema fails with ErrSchema.
func VerifyTreeHead(sth *sessionv1.SignedTreeHead, hubKey ed25519.PublicKey) error {
	if err := VerifyTreeHeadSignature(sth, hubKey); err != nil {
		return err
	}
	if sth.GetSchema() != sessionv1.Schema {
		return fmt.Errorf("%w: %w: the tree head has schema %q, this code checks only schema %q",
			ErrVerify, ErrSchema, sth.GetSchema(), sessionv1.Schema)
	}
	return nil
}

// VerifyTreeHeadSignature checks only the signature of a tree head, whatever its schema. A hub
// uses it to serve a stored session of another schema; a verifier uses VerifyTreeHead.
func VerifyTreeHeadSignature(sth *sessionv1.SignedTreeHead, hubKey ed25519.PublicKey) error {
	if len(hubKey) != ed25519.PublicKeySize {
		return fail("hub key has %d bytes, not %d", len(hubKey), ed25519.PublicKeySize)
	}
	if len(sth.GetPublicKey()) > 0 && !bytes.Equal(sth.GetPublicKey(), hubKey) {
		return fail("tree head is signed by another key")
	}
	if len(sth.GetRootHash()) != HashSize {
		return fail("root hash has %d bytes, not %d", len(sth.GetRootHash()), HashSize)
	}
	msg := TreeHeadMessage(sth.GetSchema(), sth.GetSessionId(), uint64(sth.GetTreeSize()), sth.GetRootHash())
	if !ed25519.Verify(hubKey, msg, sth.GetSignature()) {
		return fail("signature of the tree head is not valid")
	}
	return nil
}

// LeafHashes returns the leaf hashes of a complete log: the events must have the IDs 1 to
// len(evs) in order and belong to sessionID.
func LeafHashes(sessionID string, evs []*sessionv1.Event) ([][]byte, error) {
	out := make([][]byte, 0, len(evs))
	for i, ev := range evs {
		if ev.GetId() != uint32(i+1) {
			return nil, fail("event at position %d has ID %d, not %d", i+1, ev.GetId(), i+1)
		}
		if ev.GetSessionId() != sessionID {
			return nil, fail("event %d belongs to session %q, not %q", ev.GetId(), ev.GetSessionId(), sessionID)
		}
		h, err := EventLeafHash(ev)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// VerifyEvents checks a copy of a session log against a signed tree head: the signature, and
// that the events 1 to tree_size give its root hash. evs must hold exactly the events 1 to
// tree_size, in order. Placeholders verify by the leaf hash they carry.
func VerifyEvents(evs []*sessionv1.Event, sth *sessionv1.SignedTreeHead, hubKey ed25519.PublicKey) error {
	if err := VerifyTreeHead(sth, hubKey); err != nil {
		return err
	}
	if len(evs) != int(sth.GetTreeSize()) {
		return fail("copy has %d events, the tree head %d", len(evs), sth.GetTreeSize())
	}
	leaves, err := LeafHashes(sth.GetSessionId(), evs)
	if err != nil {
		return err
	}
	if !bytes.Equal(RootHash(leaves), sth.GetRootHash()) {
		return fail("events do not give the root hash of the tree head")
	}
	return nil
}

// VerifyEventInclusion checks that one event is in the tree of a signed tree head, with the
// audit path from GetInclusionProof. It also checks the signature of the head.
func VerifyEventInclusion(ev *sessionv1.Event, proof [][]byte, sth *sessionv1.SignedTreeHead, hubKey ed25519.PublicKey) error {
	if err := VerifyTreeHead(sth, hubKey); err != nil {
		return err
	}
	if ev.GetSessionId() != sth.GetSessionId() {
		return fail("event belongs to session %q, the tree head to %q", ev.GetSessionId(), sth.GetSessionId())
	}
	if ev.GetId() == 0 {
		return fail("event has no ID")
	}
	h, err := EventLeafHash(ev)
	if err != nil {
		return err
	}
	return VerifyInclusion(uint64(ev.GetId()-1), uint64(sth.GetTreeSize()), h, proof, sth.GetRootHash())
}

// VerifyTreeHeadConsistency checks two signed tree heads of one session and the consistency
// proof between them: the newer log extends the older one.
func VerifyTreeHeadConsistency(older, newer *sessionv1.SignedTreeHead, proof [][]byte, hubKey ed25519.PublicKey) error {
	for _, sth := range []*sessionv1.SignedTreeHead{older, newer} {
		if err := VerifyTreeHead(sth, hubKey); err != nil {
			return err
		}
	}
	if older.GetSessionId() != newer.GetSessionId() {
		return fail("tree heads belong to different sessions")
	}
	return VerifyConsistency(uint64(older.GetTreeSize()), uint64(newer.GetTreeSize()),
		older.GetRootHash(), newer.GetRootHash(), proof)
}
