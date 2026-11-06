package contract

// Contract tests of the journal (bb-journal) through if-journal-api, and of the minutes that the
// meeting session stores in the journal when a session ends (rs-session-ends). They start the
// real office-server and use only the Connect clients.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/contracttest"
	"github.com/mrsimpson/agentic-office/core/ids"
)

const (
	decision = officev1.EntryKind_ENTRY_KIND_DECISION
	task     = officev1.EntryKind_ENTRY_KIND_TASK
	minutes  = officev1.EntryKind_ENTRY_KIND_MINUTES
	note     = officev1.EntryKind_ENTRY_KIND_NOTE
)

// appendEntry appends an entry as a and fails the test on an error.
func appendEntry(t *testing.T, a *contracttest.Actor, kind officev1.EntryKind, text, sessionID string) *officev1.JournalEntry {
	t.Helper()
	res, err := a.Journal.AppendEntry(ctx10(t), &officev1.AppendEntryRequest{CommandId: ids.NewCommandID(),
		Kind: kind, Text: text, AuthorParticipantId: a.ParticipantID, SessionId: sessionID})
	if err != nil {
		t.Fatalf("append %q as %s: %v", text, a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("append %q: duplicate on the first call", text)
	}
	return res.GetEntry()
}

// list lists entries as a and fails the test on an error.
func list(t *testing.T, a *contracttest.Actor, req *officev1.ListEntriesRequest) []*officev1.JournalEntry {
	t.Helper()
	res, err := a.Journal.ListEntries(ctx10(t), req)
	if err != nil {
		t.Fatalf("list %v: %v", req, err)
	}
	return res.GetEntries()
}

// entryIDs returns the IDs of entries.
func entryIDs(es []*officev1.JournalEntry) []uint32 {
	out := make([]uint32, len(es))
	for i, e := range es {
		out[i] = e.GetId()
	}
	return out
}

func wantIDs(t *testing.T, what string, es []*officev1.JournalEntry, want ...uint32) {
	t.Helper()
	if got := entryIDs(es); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%s: entry IDs %v, want %v", what, got, want)
	}
}

// TestJournalAppendAndList: entries keep what the author wrote, record the author and its kind
// from the token, get growing IDs and a time, and come back oldest first.
func TestJournalAppendAndList(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	before := time.Now().Add(-time.Second)
	e1 := appendEntry(t, ada, decision, "We ship on Friday.", "s1")
	e2 := appendEntry(t, bot, task, "Bot writes the release notes.", "")
	e3 := appendEntry(t, bot, note, "Line one\nline two", "s1")
	e4 := appendEntry(t, ada, minutes, "Minutes written by hand.", "s2")

	want := []struct {
		e      *officev1.JournalEntry
		id     uint32
		kind   officev1.EntryKind
		author string
		akind  officev1.ParticipantKind
		sid    string
	}{
		{e1, 1, decision, "ada", human, "s1"},
		{e2, 2, task, "bot", agent, ""},
		{e3, 3, note, "bot", agent, "s1"},
		{e4, 4, minutes, "ada", human, "s2"},
	}
	for _, w := range want {
		e := w.e
		if e.GetId() != w.id || e.GetKind() != w.kind || e.GetAuthorParticipantId() != w.author ||
			e.GetAuthorKind() != w.akind || e.GetSessionId() != w.sid || e.GetCommandId() == "" {
			t.Fatalf("entry %d: %v", w.id, e)
		}
		if tm := e.GetTime().AsTime(); tm.Before(before) || tm.After(time.Now().Add(time.Second)) {
			t.Fatalf("entry %d: time %v", w.id, tm)
		}
	}
	if e3.GetText() != "Line one\nline two" {
		t.Fatalf("text: %q", e3.GetText())
	}

	// Every participant reads every entry, oldest first, equal to what append returned.
	for _, a := range []*contracttest.Actor{ada, bot} {
		got := list(t, a, &officev1.ListEntriesRequest{})
		wantIDs(t, "list as "+a.ParticipantID, got, 1, 2, 3, 4)
		for i, e := range []*officev1.JournalEntry{e1, e2, e3, e4} {
			if got[i].String() != e.String() {
				t.Fatalf("listed entry %d: %v, want %v", i+1, got[i], e)
			}
		}
	}
}

// TestJournalFilterAndCursor: filters by kind and session combine with the cursor and the limit.
func TestJournalFilterAndCursor(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	kinds := []officev1.EntryKind{decision, task, minutes, note}
	for i := range 12 {
		sid := []string{"s1", "s2", ""}[i%3]
		appendEntry(t, ada, kinds[i%4], fmt.Sprintf("entry %d", i+1), sid)
	}
	// Entry n has kind kinds[(n-1)%4] and session [s1 s2 ""][(n-1)%3].
	cases := []struct {
		name string
		req  *officev1.ListEntriesRequest
		want []uint32
	}{
		{"all", &officev1.ListEntriesRequest{}, []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}},
		{"decisions", &officev1.ListEntriesRequest{Kind: decision}, []uint32{1, 5, 9}},
		{"tasks", &officev1.ListEntriesRequest{Kind: task}, []uint32{2, 6, 10}},
		{"minutes", &officev1.ListEntriesRequest{Kind: minutes}, []uint32{3, 7, 11}},
		{"notes", &officev1.ListEntriesRequest{Kind: note}, []uint32{4, 8, 12}},
		{"session s1", &officev1.ListEntriesRequest{SessionId: "s1"}, []uint32{1, 4, 7, 10}},
		{"session s2 tasks", &officev1.ListEntriesRequest{SessionId: "s2", Kind: task}, []uint32{2}},
		{"unknown session", &officev1.ListEntriesRequest{SessionId: "nope"}, nil},
		{"after 5", &officev1.ListEntriesRequest{AfterEntryId: 5}, []uint32{6, 7, 8, 9, 10, 11, 12}},
		{"after 5, limit 2", &officev1.ListEntriesRequest{AfterEntryId: 5, Limit: 2}, []uint32{6, 7}},
		{"after 1, decisions, limit 1", &officev1.ListEntriesRequest{AfterEntryId: 1, Kind: decision, Limit: 1}, []uint32{5}},
		{"after the last", &officev1.ListEntriesRequest{AfterEntryId: 12}, nil},
		{"far after the last", &officev1.ListEntriesRequest{AfterEntryId: 1000}, nil},
	}
	for _, c := range cases {
		wantIDs(t, c.name, list(t, ada, c.req), c.want...)
	}

	// Paging with the last ID as the cursor reads every matching entry exactly once.
	var got []*officev1.JournalEntry
	var cursor uint32
	for pages := 0; ; pages++ {
		page := list(t, ada, &officev1.ListEntriesRequest{AfterEntryId: cursor, SessionId: "s1", Limit: 3})
		if len(page) == 0 {
			if pages != 2 {
				t.Fatalf("%d pages, want 2", pages)
			}
			break
		}
		got = append(got, page...)
		cursor = page[len(page)-1].GetId()
	}
	wantIDs(t, "paged session s1", got, 1, 4, 7, 10)

	_, err := ada.Journal.ListEntries(ctx10(t), &officev1.ListEntriesRequest{Kind: officev1.EntryKind(99)})
	wantCode(t, "unknown kind", err, connect.CodeInvalidArgument)
}

// TestJournalDefaultLimit: without a limit the server returns at most 100 entries, and never
// more than 1000.
func TestJournalDefaultLimit(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	bot := srv.Participant(t, "bot", "Bot", agent)
	for i := range 105 {
		appendEntry(t, bot, note, fmt.Sprintf("note %d", i+1), "")
	}
	if n := len(list(t, bot, &officev1.ListEntriesRequest{})); n != 100 {
		t.Fatalf("default limit: %d entries, want 100", n)
	}
	if n := len(list(t, bot, &officev1.ListEntriesRequest{Limit: 5000})); n != 105 {
		t.Fatalf("limit 5000: %d entries, want 105", n)
	}
}

// TestJournalIdempotent: a repeated command_id returns the stored entry; reusing it for another
// entry fails.
func TestJournalIdempotent(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)
	req := &officev1.AppendEntryRequest{CommandId: ids.NewCommandID(), Kind: decision, Text: "We ship.",
		AuthorParticipantId: "ada", SessionId: "s1"}
	first, err := ada.Journal.AppendEntry(ctx10(t), req)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := ada.Journal.AppendEntry(ctx10(t), req)
		if err != nil {
			t.Fatal(err)
		}
		if !again.GetDuplicate() || again.GetEntry().String() != first.GetEntry().String() {
			t.Fatalf("repeat: %v, want the first entry with duplicate", again)
		}
	}
	other := func(change func(r *officev1.AppendEntryRequest)) *officev1.AppendEntryRequest {
		r := &officev1.AppendEntryRequest{CommandId: req.GetCommandId(), Kind: req.GetKind(), Text: req.GetText(),
			AuthorParticipantId: req.GetAuthorParticipantId(), SessionId: req.GetSessionId()}
		change(r)
		return r
	}
	for name, r := range map[string]*officev1.AppendEntryRequest{
		"other text":    other(func(r *officev1.AppendEntryRequest) { r.Text = "We wait." }),
		"other kind":    other(func(r *officev1.AppendEntryRequest) { r.Kind = note }),
		"other session": other(func(r *officev1.AppendEntryRequest) { r.SessionId = "s2" }),
	} {
		_, err := ada.Journal.AppendEntry(ctx10(t), r)
		wantCode(t, name, err, connect.CodeAlreadyExists)
	}
	_, err = bot.Journal.AppendEntry(ctx10(t), other(func(r *officev1.AppendEntryRequest) { r.AuthorParticipantId = "bot" }))
	wantCode(t, "other author", err, connect.CodeAlreadyExists)

	wantIDs(t, "entries", list(t, ada, &officev1.ListEntriesRequest{}), 1)
}

// TestJournalErrors: bad requests, a wrong author, no token and stage links.
func TestJournalErrors(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	ok := func() *officev1.AppendEntryRequest {
		return &officev1.AppendEntryRequest{CommandId: ids.NewCommandID(), Kind: note, Text: "x", AuthorParticipantId: "ada"}
	}
	bad := map[string]func(r *officev1.AppendEntryRequest){
		"no command_id":       func(r *officev1.AppendEntryRequest) { r.CommandId = "" },
		"reserved command_id": func(r *officev1.AppendEntryRequest) { r.CommandId = "office/session-end/x" },
		"no kind":             func(r *officev1.AppendEntryRequest) { r.Kind = officev1.EntryKind_ENTRY_KIND_UNSPECIFIED },
		"unknown kind":        func(r *officev1.AppendEntryRequest) { r.Kind = officev1.EntryKind(99) },
		"no text":             func(r *officev1.AppendEntryRequest) { r.Text = "" },
		"blank text":          func(r *officev1.AppendEntryRequest) { r.Text = " \n\t" },
		"no author":           func(r *officev1.AppendEntryRequest) { r.AuthorParticipantId = "" },
	}
	for name, change := range bad {
		r := ok()
		change(r)
		_, err := ada.Journal.AppendEntry(ctx10(t), r)
		wantCode(t, name, err, connect.CodeInvalidArgument)
	}
	r := ok()
	r.AuthorParticipantId = "bob"
	_, err := ada.Journal.AppendEntry(ctx10(t), r)
	wantCode(t, "author is not the caller", err, connect.CodePermissionDenied)

	anon := srv.Anonymous(t)
	_, err = anon.Journal.AppendEntry(ctx10(t), ok())
	wantCode(t, "append without token", err, connect.CodeUnauthenticated)
	_, err = anon.Journal.ListEntries(ctx10(t), &officev1.ListEntriesRequest{})
	wantCode(t, "list without token", err, connect.CodeUnauthenticated)
	_, err = srv.WithToken(t, "not-a-token").Journal.ListEntries(ctx10(t), &officev1.ListEntriesRequest{})
	wantCode(t, "list with a bad token", err, connect.CodeUnauthenticated)

	link, err := ada.Identity.CreateStageLink(ctx10(t), &officev1.CreateStageLinkRequest{SessionId: "s1", CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	stage := srv.WithToken(t, stageToken(t, link.GetUrl()))
	_, err = stage.Journal.AppendEntry(ctx10(t), ok())
	wantCode(t, "append with a stage link", err, connect.CodePermissionDenied)
	_, err = stage.Journal.ListEntries(ctx10(t), &officev1.ListEntriesRequest{SessionId: "s1"})
	wantCode(t, "list with a stage link", err, connect.CodePermissionDenied)

	if es := list(t, ada, &officev1.ListEntriesRequest{}); len(es) != 0 {
		t.Fatalf("failed calls stored entries: %v", es)
	}
}

// TestJournalSurvivesRestart: the office server keeps the journal in its data directory.
func TestJournalSurvivesRestart(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	e1 := appendEntry(t, ada, decision, "We ship.", "s1")
	e2 := appendEntry(t, ada, task, "Ada writes the plan.", "")
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	if fi, err := os.Stat(filepath.Join(srv.DataDir, "journal.jsonl")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("journal file: %v, %v", fi, err)
	}

	// The server stopped while it wrote a line: the incomplete line is dropped at the start.
	f, err := os.OpenFile(filepath.Join(srv.DataDir, "journal.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":3,"kind":"ENTRY_KIND_NO`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	srv = contracttest.StartServerIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	got := list(t, ada, &officev1.ListEntriesRequest{})
	if len(got) != 2 || got[0].String() != e1.String() || got[1].String() != e2.String() {
		t.Fatalf("after restart: %v, want %v and %v", got, e1, e2)
	}
	// Idempotency and IDs go on after the restart.
	res, err := ada.Journal.AppendEntry(ctx10(t), &officev1.AppendEntryRequest{CommandId: e1.GetCommandId(),
		Kind: decision, Text: "We ship.", AuthorParticipantId: "ada", SessionId: "s1"})
	if err != nil || !res.GetDuplicate() || res.GetEntry().GetId() != 1 {
		t.Fatalf("repeat after restart: %v, %v", res, err)
	}
	if e3 := appendEntry(t, ada, note, "After the restart.", ""); e3.GetId() != 3 {
		t.Fatalf("next ID after restart: %d, want 3", e3.GetId())
	}
}

// TestSessionEndStoresMinutes: when a session ends, the last minutes.updated text goes into the
// journal as one minutes entry of that session, written by the minute taker (rs-session-ends).
// A session without minutes, or with blank minutes at the end, stores nothing.
func TestSessionEndStoresMinutes(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	scribe := srv.Participant(t, "scribe", "Scribe", agent)
	join := func(a *contracttest.Actor, sid string) {
		t.Helper()
		if _, err := a.Session.Join(ctx10(t), &officev1.JoinRequest{SessionId: sid, CommandId: ids.NewCommandID()}); err != nil {
			t.Fatalf("join %s: %v", a.ParticipantID, err)
		}
	}
	update := func(a *contracttest.Actor, sid, text string) {
		t.Helper()
		if _, err := a.Session.UpdateMinutes(ctx10(t), &officev1.UpdateMinutesRequest{SessionId: sid,
			CommandId: ids.NewCommandID(), ParticipantId: a.ParticipantID, Text: text}); err != nil {
			t.Fatalf("minutes of %s: %v", a.ParticipantID, err)
		}
	}
	end := func(a *contracttest.Actor, sid, cid string) *officev1.CommandResult {
		t.Helper()
		res, err := a.Session.EndSession(ctx10(t), &officev1.EndSessionRequest{SessionId: sid, CommandId: cid,
			ParticipantId: a.ParticipantID})
		if err != nil {
			t.Fatalf("end %s: %v", sid, err)
		}
		return res.GetResult()
	}

	sid := "s-" + ids.NewCommandID()
	join(ada, sid)
	join(scribe, sid)
	update(scribe, sid, "Draft")
	update(ada, sid, "Draft, fixed by Ada")
	update(scribe, sid, "## Decisions\n- We ship on Friday.")
	appendEntry(t, ada, note, "A note in the meeting.", sid)
	if es := list(t, ada, &officev1.ListEntriesRequest{Kind: minutes}); len(es) != 0 {
		t.Fatalf("minutes before the end: %v", es)
	}

	before := time.Now().Add(-time.Second)
	endCmd := ids.NewCommandID()
	ended := end(ada, sid, endCmd)
	// The entry is there as soon as EndSession returns.
	got := list(t, scribe, &officev1.ListEntriesRequest{SessionId: sid, Kind: minutes})
	if len(got) != 1 {
		t.Fatalf("minutes entries of the session: %v, want 1", got)
	}
	e := got[0]
	if e.GetText() != "## Decisions\n- We ship on Friday." || e.GetAuthorParticipantId() != "scribe" ||
		e.GetAuthorKind() != agent || e.GetSessionId() != sid || e.GetCommandId() != "office/session-end/"+endCmd {
		t.Fatalf("minutes entry: %v", e)
	}
	if tm := e.GetTime().AsTime(); tm.Before(before) || tm.After(ended.GetEvent().GetTime().AsTime()) {
		t.Fatalf("minutes time %v is not between %v and session.ended at %v", tm, before, ended.GetEvent().GetTime().AsTime())
	}
	wantIDs(t, "all entries of the session", list(t, ada, &officev1.ListEntriesRequest{SessionId: sid}), 1, 2)

	// A repeated EndSession returns the stored event and does not store the minutes again.
	if again := end(ada, sid, endCmd); !again.GetDuplicate() || again.GetEvent().GetId() != ended.GetEvent().GetId() {
		t.Fatalf("repeated end: %v", again)
	}
	// A new end fails, and also stores nothing.
	_, err := scribe.Session.EndSession(ctx10(t), &officev1.EndSessionRequest{SessionId: sid, CommandId: ids.NewCommandID(),
		ParticipantId: "scribe"})
	wantCode(t, "end an ended session", err, connect.CodeFailedPrecondition)
	wantIDs(t, "minutes after repeats", list(t, ada, &officev1.ListEntriesRequest{Kind: minutes}), 2)

	// A session without minutes stores nothing.
	quiet := "s-" + ids.NewCommandID()
	join(ada, quiet)
	end(ada, quiet, ids.NewCommandID())
	// A session whose last minutes are blank stores nothing.
	cleared := "s-" + ids.NewCommandID()
	join(scribe, cleared)
	update(scribe, cleared, "Something")
	update(scribe, cleared, "  ")
	end(scribe, cleared, ids.NewCommandID())
	// A human minute taker is recorded as human.
	human1 := "s-" + ids.NewCommandID()
	join(ada, human1)
	update(ada, human1, "Ada's minutes")
	end(ada, human1, ids.NewCommandID())

	all := list(t, ada, &officev1.ListEntriesRequest{Kind: minutes})
	wantIDs(t, "minutes of all sessions", all, 2, 3)
	if e := all[1]; e.GetSessionId() != human1 || e.GetAuthorParticipantId() != "ada" || e.GetAuthorKind() != human ||
		e.GetText() != "Ada's minutes" {
		t.Fatalf("minutes of a human: %v", e)
	}
}

// TestSessionEndStoresMinutesAfterRestart: session logs are lost in a restart, the journal is
// not. A session with the same ID after the restart stores its minutes as a new entry.
func TestSessionEndStoresMinutesAfterRestart(t *testing.T) {
	srv := contracttest.StartServer(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	run := func(text string) {
		t.Helper()
		ctx := ctx10(t)
		if _, err := ada.Session.Join(ctx, &officev1.JoinRequest{SessionId: "standup", CommandId: ids.NewCommandID()}); err != nil {
			t.Fatal(err)
		}
		if _, err := ada.Session.UpdateMinutes(ctx, &officev1.UpdateMinutesRequest{SessionId: "standup",
			CommandId: ids.NewCommandID(), ParticipantId: "ada", Text: text}); err != nil {
			t.Fatal(err)
		}
		if _, err := ada.Session.EndSession(ctx, &officev1.EndSessionRequest{SessionId: "standup",
			CommandId: ids.NewCommandID(), ParticipantId: "ada"}); err != nil {
			t.Fatal(err)
		}
	}
	run("Monday")
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv = contracttest.StartServerIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	run("Tuesday")
	got := list(t, ada, &officev1.ListEntriesRequest{SessionId: "standup", Kind: minutes})
	if len(got) != 2 || got[0].GetText() != "Monday" || got[1].GetText() != "Tuesday" {
		t.Fatalf("minutes of standup: %v", got)
	}
}
