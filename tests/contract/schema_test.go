package contract

// Contract tests of the schema of a session (dec-session-schema) through if-session-commands and
// if-session-stream. A session belongs to the schema it was started with. Its signed tree heads
// carry the schema, and the signature covers it. A hub does not continue a session of another
// schema, and the verification code of the interface checks only logs of its own schema.
//
// A session of another schema is prepared in the archive of a stopped hub: the test signs the
// stored tree head again with the signing key file of the hub, but with another schema. The
// archive layout and the tree head file are part of the contract (README, Archive).

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/logfile"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/merkle"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// otherSchema is a schema that no code of this repository has.
const otherSchema = "office.sessionstream.v1/0"

// resign returns a copy of a tree head with another schema, signed by key.
func resign(h *sessionstreamv1.SignedTreeHead, schema string, key ed25519.PrivateKey) *sessionstreamv1.SignedTreeHead {
	c := proto.Clone(h).(*sessionstreamv1.SignedTreeHead)
	c.Schema = schema
	c.Signature = ed25519.Sign(key, merkle.TreeHeadMessage(schema, c.GetSessionId(), uint64(c.GetTreeSize()), c.GetRootHash()))
	return c
}

func signingKey(t *testing.T, srv *contracttest.Hub) ed25519.PrivateKey {
	t.Helper()
	key, err := auth.ReadKey(srv.SigningKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func wantSchemaError(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, merkle.ErrSchema) || !errors.Is(err, merkle.ErrVerify) {
		t.Errorf("%s: want a schema error, got %v", what, err)
	}
}

// wantSchemaRefused checks that a call failed with failed_precondition and names both schemas.
func wantSchemaRefused(t *testing.T, what string, err error, schema string) {
	t.Helper()
	wantCode(t, what, err, connect.CodeFailedPrecondition)
	if err != nil && (!strings.Contains(err.Error(), schema) || !strings.Contains(err.Error(), sessionstreamv1.Schema)) {
		t.Errorf("%s: error does not name the schemas %q and %q: %v", what, schema, sessionstreamv1.Schema, err)
	}
}

// A new session records the schema of the hub: every tree head carries it (GetTreeHead, the
// stream, session.ended), and the signature covers it, so it cannot be swapped.
func TestSchemaRecordedAndSigned(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "First.")
	key := hubKey(t, m.reader.Commands)
	streamed := follow(t, m.reader, m.sid, 2).head
	ended := m.end(ada).GetSessionEnded().GetTreeHead()
	evs := m.events()
	for name, h := range map[string]*sessionstreamv1.SignedTreeHead{
		"GetTreeHead": m.head(0), "GetTreeHead of size 1": m.head(1), "stream": streamed, "session.ended": ended,
	} {
		if h.GetSchema() != sessionstreamv1.Schema {
			t.Fatalf("%s: schema %q, want %q", name, h.GetSchema(), sessionstreamv1.Schema)
		}
		// By hand: the signed message starts with the prefix and the schema.
		msg := merkle.TreeHeadMessage(sessionstreamv1.Schema, m.sid, uint64(h.GetTreeSize()), h.GetRootHash())
		if !bytes.HasPrefix(msg, append([]byte(merkle.TreeHeadPrefix), 0, 0, 0, byte(len(sessionstreamv1.Schema)))) ||
			!ed25519.Verify(key, msg, h.GetSignature()) {
			t.Fatalf("%s: the signature does not cover the schema", name)
		}
		if err := merkle.VerifyEvents(evs[:h.GetTreeSize()], h, key); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		swapped := proto.Clone(h).(*sessionstreamv1.SignedTreeHead)
		swapped.Schema = otherSchema
		wantVerifyError(t, name+" with a swapped schema", merkle.VerifyTreeHeadSignature(swapped, key))
		removed := proto.Clone(h).(*sessionstreamv1.SignedTreeHead)
		removed.Schema = ""
		wantVerifyError(t, name+" without a schema", merkle.VerifyTreeHead(removed, key))
	}
}

// The verification code of the interface checks a log only with its own schema: a head of
// another schema fails with ErrSchema, also with a valid signature of the hub.
func TestSchemaVerificationFailsForAnotherSchema(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "First.")
	m.say(ada, "Second.")
	key := hubKey(t, m.reader.Commands)
	evs := m.events()
	signer := signingKey(t, m.srv)
	head, older := m.head(0), m.head(2)
	other, otherOlder := resign(head, otherSchema, signer), resign(older, otherSchema, signer)
	if err := merkle.VerifyTreeHeadSignature(other, key); err != nil {
		t.Fatalf("the signature of the head with another schema: %v", err)
	}
	wantSchemaError(t, "VerifyTreeHead", merkle.VerifyTreeHead(other, key))
	wantSchemaError(t, "VerifyEvents", merkle.VerifyEvents(evs, other, key))
	if err := merkle.VerifyEvents(evs, other, key); err == nil || !strings.Contains(err.Error(), otherSchema) {
		t.Errorf("the error does not name the schema of the log: %v", err)
	}
	proof, err := m.reader.Commands.GetInclusionProof(m.ctx(), &sessioncommandsv1.GetInclusionProofRequest{SessionId: m.sid, EventId: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyEventInclusion(evs[1], proof.GetHashes(), head, key); err != nil {
		t.Fatal(err)
	}
	wantSchemaError(t, "VerifyEventInclusion", merkle.VerifyEventInclusion(evs[1], proof.GetHashes(), other, key))
	cons, err := m.reader.Commands.GetConsistencyProof(m.ctx(), &sessioncommandsv1.GetConsistencyProofRequest{SessionId: m.sid, FirstSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := merkle.VerifyTreeHeadConsistency(older, head, cons.GetHashes(), key); err != nil {
		t.Fatal(err)
	}
	wantSchemaError(t, "VerifyTreeHeadConsistency", merkle.VerifyTreeHeadConsistency(otherOlder, other, cons.GetHashes(), key))
	wantSchemaError(t, "VerifyTreeHeadConsistency, older of another schema",
		merkle.VerifyTreeHeadConsistency(otherOlder, head, cons.GetHashes(), key))
}

// The hub rejects an offered copy whose tree head has another schema, even with a valid
// signature of the hub, and its log stays as it is.
func TestSchemaOfferOfAnotherSchemaIsRejected(t *testing.T) {
	m := newMeeting(t)
	ada := m.join("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN)
	m.say(ada, "First.")
	evs := m.events()
	other := resign(m.head(0), otherSchema, signingKey(t, m.srv))
	_, err := m.s(ada).OfferLog(m.ctx(), &sessioncommandsv1.OfferLogRequest{SessionId: m.sid, Events: evs, TreeHead: other})
	wantSchemaRefused(t, "offer of another schema", err, otherSchema)
	// A schema changed without a new signature does not verify.
	swapped := proto.Clone(m.head(0)).(*sessionstreamv1.SignedTreeHead)
	swapped.Schema = otherSchema
	_, err = m.s(ada).OfferLog(m.ctx(), &sessioncommandsv1.OfferLogRequest{SessionId: m.sid, Events: evs, TreeHead: swapped})
	wantCode(t, "offer with a swapped schema", err, connect.CodeInvalidArgument)
	if got := m.events(); len(got) != len(evs) {
		t.Fatalf("a rejected offer changed the log: %d events, want %d", len(got), len(evs))
	}
}

// A stored session of another schema keeps its schema after a restart. It is readable (events,
// stream, tree heads with its schema, proofs), but the hub does not continue it: new commands and
// offers fail with failed_precondition. A session of the schema of the hub goes on as before.
func TestSchemaStoredSessionOfAnotherSchema(t *testing.T) {
	srv := contracttest.StartHub(t, nil, "-archive-interval", "1h")
	ada := srv.Participant(t, "ada", "Ada", human)
	oldSID, newSID := "s-"+coreapi.NewCommandID(), "s-"+coreapi.NewCommandID()
	repeat := &sessioncommandsv1.SubmitUtteranceRequest{SessionId: oldSID, CommandId: coreapi.NewCommandID(), ParticipantId: "ada",
		Text: "Before the restart.", Modality: sessionstreamv1.Modality_MODALITY_TEXT}
	var first *sessioncommandsv1.SubmitUtteranceResponse
	for _, sid := range []string{oldSID, newSID} {
		if _, err := ada.Commands.Join(ctx10(t), &sessioncommandsv1.JoinRequest{SessionId: sid, CommandId: coreapi.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
		req := proto.Clone(repeat).(*sessioncommandsv1.SubmitUtteranceRequest)
		req.SessionId = sid
		if sid != oldSID {
			req.CommandId = coreapi.NewCommandID()
		}
		res, err := ada.Commands.SubmitUtterance(ctx10(t), req)
		if err != nil {
			t.Fatal(err)
		}
		if sid == oldSID {
			first = res
		}
	}
	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v", code, err)
	}

	// The stored session keeps its schema in the tree head file.
	repo := srv.ArchiveDir()
	for _, sid := range []string{oldSID, newSID} {
		c := contracttest.SessionCommits(t, repo, sid)
		if len(c) == 0 {
			t.Fatalf("session %s is not in the archive", sid)
		}
		if a := contracttest.ReadArchivedSession(t, repo, sid, c[len(c)-1]); a.Head.GetSchema() != sessionstreamv1.Schema {
			t.Fatalf("stored tree head of %s has schema %q, want %q", sid, a.Head.GetSchema(), sessionstreamv1.Schema)
		}
	}
	// Make oldSID a session of another schema: the same log, its head signed with another schema.
	c := contracttest.SessionCommits(t, repo, oldSID)
	stored := contracttest.ReadArchivedSession(t, repo, oldSID, c[len(c)-1])
	oldHead := resign(stored.Head, otherSchema, signingKey(t, srv))
	data, err := logfile.MarshalTreeHead(oldHead)
	if err != nil {
		t.Fatal(err)
	}
	headFile := contracttest.ArchivedSessionDir(oldSID) + "/tree-head.yaml"
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(headFile)), data, 0o600); err != nil {
		t.Fatal(err)
	}
	contracttest.Git(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid",
		"commit", "-q", "--no-verify", "-m", "Test: a session of another schema", "--", headFile)

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil, "-archive-interval", "1h", "-restore-wait", "1h")
	ada = srv.WithKey(t, ada.Key)
	key := hubKey(t, ada.Commands)
	srv.WaitStderr(t, regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("session %q: the session has schema %q", oldSID, otherSchema))), 10*time.Second)

	// Readable: the events, the head with its own schema, the stream and proofs.
	got, err := ada.Stream.GetEvents(ctx10(t), &sessionstreamv1.GetEventsRequest{SessionId: oldSID})
	if err != nil {
		t.Fatalf("read a session of another schema: %v", err)
	}
	if len(got.GetEvents()) != len(stored.Events) {
		t.Fatalf("session of another schema: %d events, want %d", len(got.GetEvents()), len(stored.Events))
	}
	head, err := ada.Commands.GetTreeHead(ctx10(t), &sessioncommandsv1.GetTreeHeadRequest{SessionId: oldSID})
	if err != nil {
		t.Fatal(err)
	}
	if h := head.GetTreeHead(); h.GetSchema() != otherSchema || !bytes.Equal(h.GetRootHash(), oldHead.GetRootHash()) {
		t.Fatalf("tree head of a session of another schema: %v", h)
	}
	if err := merkle.VerifyTreeHeadSignature(head.GetTreeHead(), key); err != nil {
		t.Fatal(err)
	}
	wantSchemaError(t, "verify a session of another schema", merkle.VerifyEvents(got.GetEvents(), head.GetTreeHead(), key))
	smaller, err := ada.Commands.GetTreeHead(ctx10(t), &sessioncommandsv1.GetTreeHeadRequest{SessionId: oldSID, TreeSize: 1})
	if err != nil || smaller.GetTreeHead().GetSchema() != otherSchema {
		t.Fatalf("smaller tree head of a session of another schema: %v, %v", smaller, err)
	}
	if streamed := follow(t, ada, oldSID, len(stored.Events)).head; streamed.GetSchema() != otherSchema {
		t.Fatalf("stream head of a session of another schema has schema %q", streamed.GetSchema())
	}

	// Not continued: a new command fails, a repeated command returns its stored event.
	_, err = ada.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: oldSID,
		CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: "After the restart.", Modality: sessionstreamv1.Modality_MODALITY_TEXT})
	wantSchemaRefused(t, "command in a session of another schema", err, otherSchema)
	_, err = ada.Commands.EndSession(ctx10(t), &sessioncommandsv1.EndSessionRequest{SessionId: oldSID,
		CommandId: coreapi.NewCommandID(), ParticipantId: "ada"})
	wantSchemaRefused(t, "end a session of another schema", err, otherSchema)
	again, err := ada.Commands.SubmitUtterance(ctx10(t), repeat)
	if err != nil || !again.GetResult().GetDuplicate() || !proto.Equal(again.GetResult().GetEvent(), first.GetResult().GetEvent()) {
		t.Fatalf("repeated command in a session of another schema: %v, %v", again, err)
	}
	// An offer of a longer copy with the old schema is rejected too.
	longer := append(slices.Clone(stored.Events), &sessionstreamv1.Event{Id: uint32(len(stored.Events) + 1), SessionId: oldSID,
		Type: "hand.raised", ActorParticipantId: "ada",
		Payload: &sessionstreamv1.Event_HandRaised{HandRaised: &sessionstreamv1.HandRaised{ParticipantId: "ada"}}})
	var leaves [][]byte
	for _, ev := range longer {
		h, err := merkle.EventLeafHash(ev)
		if err != nil {
			t.Fatal(err)
		}
		leaves = append(leaves, h)
	}
	longerHead := merkle.SignTreeHead(signingKey(t, srv), otherSchema, oldSID, leaves)
	_, err = ada.Commands.OfferLog(ctx10(t), &sessioncommandsv1.OfferLogRequest{SessionId: oldSID, Events: longer, TreeHead: longerHead})
	wantSchemaRefused(t, "offer to a session of another schema", err, otherSchema)
	if got, err := ada.Stream.GetEvents(ctx10(t), &sessionstreamv1.GetEventsRequest{SessionId: oldSID}); err != nil || len(got.GetEvents()) != len(stored.Events) {
		t.Fatalf("the session of another schema changed: %v, %v", got, err)
	}

	// The session of the schema of the hub goes on.
	res, err := ada.Commands.SubmitUtterance(ctx10(t), &sessioncommandsv1.SubmitUtteranceRequest{SessionId: newSID,
		CommandId: coreapi.NewCommandID(), ParticipantId: "ada", Text: "After the restart.", Modality: sessionstreamv1.Modality_MODALITY_TEXT})
	if err != nil {
		t.Fatalf("command in a session of the schema of the hub: %v", err)
	}
	if res.GetResult().GetEvent().GetId() != 3 {
		t.Fatalf("event ID %d, want 3", res.GetResult().GetEvent().GetId())
	}
}
