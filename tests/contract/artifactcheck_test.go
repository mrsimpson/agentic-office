package contract

// Contract tests of the checks of the artifacts (dec-artifact-sync) through their four interfaces:
// if-records-check, if-tasks-check, if-slides-check and if-artifact-check. The checks are
// in-process Go calls, so the tests call them directly and use nothing but their signatures.
//
// The same check serves the own interface of a part. The tests show this with the real office-hub:
// what a part writes passes its check, and a hub that finds in its archive a file that the check
// rejects does not start and gives the reasons of the check.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	artifactcheck "github.com/mrsimpson/agentic-office/hub/artifacts/check"
	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	slidescheck "github.com/mrsimpson/agentic-office/hub/slides/check"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	taskscheck "github.com/mrsimpson/agentic-office/hub/tasks/check"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

const (
	decisionFile = `schema: office.decision.v1
id: 1
text: Use Go for every component.
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
session_id: s1
time: "2026-11-16T10:00:00Z"
command_id: c-1
`
	minutesFile = `schema: office.minutes.v1
id: 1
session_id: s1
text: We met.
author_participant_id: ada
author_kind: AUTHOR_KIND_AGENT
time: "2026-11-16T10:00:00Z"
command_id: c-1
`
	noteFile = `schema: office.note.v1
id: 3
text: Call the bank.
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
time: "2026-11-16T10:00:00Z"
command_id: c-3
`
	taskFile = `schema: office.task.v1
ref: "local:2"
source: TASK_SOURCE_LOCAL
title: Buy milk
status: TASK_STATUS_OPEN
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
create_time: "2026-11-16T10:00:00Z"
updated_by_participant_id: ada
update_time: "2026-11-16T10:00:00Z"
revision: 1
command_id: c-1
command_ids:
  - c-1
`
	deckFile = `schema: office.slides.v1
id: slides/4
title: Budget
slides:
  - title: Plan
    bullets:
      - Hire two people
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
create_time: "2026-11-16T10:00:00Z"
command_id: c-1
`
)

// checkCase is one file and what a check must answer.
type checkCase struct {
	name    string
	schema  string
	path    string
	content string
	reason  string // empty: the file is valid; else a text that one reason contains
}

// replace returns s with the first old replaced by new. It fails the test if s has no old.
func replace(t *testing.T, s, old, new string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("the sample has no %q", old)
	}
	return strings.Replace(s, old, new, 1)
}

// runChecks runs the cases against validate. Every invalid file needs reasons in plain words.
func runChecks(t *testing.T, validate func(schema, path string, content []byte) []string, cases []checkCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validate(c.schema, c.path, []byte(c.content))
			if c.reason == "" {
				if len(got) != 0 {
					t.Fatalf("a valid file is invalid: %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("an invalid file is valid, want a reason with %q", c.reason)
			}
			if !slices.ContainsFunc(got, func(r string) bool { return strings.Contains(r, c.reason) }) {
				t.Fatalf("reasons %q, want one with %q", got, c.reason)
			}
		})
	}
}

// TestRecordsCheck: the check of the records part names its schemas, accepts the files of a
// decision, minutes and a note, and rejects every other file with reasons.
func TestRecordsCheck(t *testing.T) {
	want := []string{"office.decision.v1", "office.minutes.v1", "office.note.v1"}
	got := recordscheck.Schemas()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("schemas %v, want %v", got, want)
	}
	const d, m, n = "office.decision.v1", "office.minutes.v1", "office.note.v1"
	runChecks(t, recordscheck.Validate, []checkCase{
		{"decision", d, "decisions/000001.yaml", decisionFile, ""},
		{"minutes", m, "minutes/000001.yaml", minutesFile, ""},
		{"note", n, "notes/000003.yaml", noteFile, ""},
		{"unknown fields are ignored", d, "decisions/000001.yaml", decisionFile + "later: yes\n", ""},
		{"a schema of another part", "office.task.v1", "tasks/000001.yaml", taskFile, "not a schema of the records"},
		{"a schema that nobody owns", "office.other.v1", "decisions/000001.yaml", decisionFile, "not a schema of the records"},
		{"the file names another schema", d, "decisions/000001.yaml", replace(t, decisionFile, "office.decision.v1", "office.note.v1"), "schema must be"},
		{"no schema in the file", d, "decisions/000001.yaml", replace(t, decisionFile, "schema: office.decision.v1\n", ""), "schema must be"},
		{"no text", d, "decisions/000001.yaml", replace(t, decisionFile, "text: Use Go for every component.\n", ""), "text is missing"},
		{"a blank text", n, "notes/000003.yaml", replace(t, noteFile, "Call the bank.", "'  '"), "text is missing"},
		{"no id", n, "notes/000003.yaml", replace(t, noteFile, "id: 3\n", ""), "id is missing"},
		{"no command id", n, "notes/000003.yaml", replace(t, noteFile, "command_id: c-3\n", ""), "command_id is missing"},
		{"no author", n, "notes/000003.yaml", replace(t, noteFile, "author_participant_id: ada\n", ""), "author_participant_id is missing"},
		{"minutes without a session", m, "minutes/000001.yaml", replace(t, minutesFile, "session_id: s1\n", ""), "session_id is missing"},
		{"a wrong type of a field", n, "notes/000003.yaml", replace(t, noteFile, "id: 3", "id: three"), "do not fit the schema"},
		{"the path does not match the id", n, "notes/000004.yaml", noteFile, "belongs in notes/000003.yaml"},
		{"the directory is of another type", n, "decisions/000003.yaml", noteFile, "belongs in notes/000003.yaml"},
		{"another ending", n, "notes/000003.yml", noteFile, "ending .yaml"},
		{"not YAML", n, "notes/000003.yaml", "text: [unclosed\n", "not valid YAML"},
		{"empty", n, "notes/000003.yaml", "", "empty"},
	})
}

// TestTasksCheck: the check of the tasks part names its schema, accepts the file of a task and
// rejects every other file with reasons.
func TestTasksCheck(t *testing.T) {
	if got := taskscheck.Schemas(); !slices.Equal(got, []string{"office.task.v1"}) {
		t.Fatalf("schemas %v", got)
	}
	const s = "office.task.v1"
	runChecks(t, taskscheck.Validate, []checkCase{
		{"task", s, "tasks/000002.yaml", taskFile, ""},
		{"task with several commands", s, "tasks/000002.yaml", taskFile + "  - c-2\n", ""},
		{"a schema of another part", "office.slides.v1", "slides/000004.yaml", deckFile, "not a schema of the tasks"},
		{"a schema that nobody owns", "office.other.v1", "tasks/000002.yaml", taskFile, "not a schema of the tasks"},
		{"the file names another schema", s, "tasks/000002.yaml", replace(t, taskFile, "office.task.v1", "office.note.v1"), "schema must be"},
		{"no title", s, "tasks/000002.yaml", replace(t, taskFile, "title: Buy milk\n", ""), "title is missing"},
		{"no author", s, "tasks/000002.yaml", replace(t, taskFile, "author_participant_id: ada\n", ""), "author_participant_id is missing"},
		{"a ref that is not local", s, "tasks/000002.yaml", replace(t, taskFile, `"local:2"`, `"jira:ABC-2"`), "not a local task"},
		{"a number with a leading zero", s, "tasks/000002.yaml", replace(t, taskFile, `"local:2"`, `"local:02"`), "not a local task"},
		{"the path does not match the ref", s, "tasks/000003.yaml", taskFile, "belongs in tasks/000002.yaml"},
		{"no command id", s, "tasks/000002.yaml", replace(t, taskFile, "command_id: c-1\n", ""), "command_ids must start with the command_id"},
		{"command ids that start with another command", s, "tasks/000002.yaml", replace(t, taskFile, "  - c-1\n", "  - c-9\n"), "command_ids must start with the command_id"},
		{"no command ids", s, "tasks/000002.yaml", replace(t, taskFile, "command_ids:\n  - c-1\n", ""), "command_ids must start with the command_id"},
		{"command ids that are no list", s, "tasks/000002.yaml", replace(t, taskFile, "command_ids:\n  - c-1\n", "command_ids: c-1\n"), "list of command IDs"},
		{"another ending", s, "tasks/000002.json", taskFile, "ending .yaml"},
		{"not YAML", s, "tasks/000002.yaml", "title: [unclosed\n", "not valid YAML"},
	})
}

// TestSlidesCheck: the check of the slides part names its schema, accepts the file of a deck and
// rejects every other file with reasons.
func TestSlidesCheck(t *testing.T) {
	if got := slidescheck.Schemas(); !slices.Equal(got, []string{"office.slides.v1"}) {
		t.Fatalf("schemas %v", got)
	}
	const s = "office.slides.v1"
	runChecks(t, slidescheck.Validate, []checkCase{
		{"deck", s, "slides/000004.yaml", deckFile, ""},
		{"a deck without a title", s, "slides/000004.yaml", replace(t, deckFile, "title: Budget\n", ""), ""},
		{"a schema of another part", "office.task.v1", "tasks/000002.yaml", taskFile, "not a schema of the slides"},
		{"a schema that nobody owns", "office.other.v1", "slides/000004.yaml", deckFile, "not a schema of the slides"},
		{"the file names another schema", s, "slides/000004.yaml", replace(t, deckFile, "office.slides.v1", "office.task.v1"), "schema must be"},
		{"an id that is no deck", s, "slides/000004.yaml", replace(t, deckFile, "slides/4", "deck-4"), "not the ID of a deck"},
		{"the path does not match the id", s, "slides/000005.yaml", deckFile, "belongs in slides/000004.yaml"},
		{"no slides", s, "slides/000004.yaml", replace(t, deckFile, "slides:\n  - title: Plan\n    bullets:\n      - Hire two people\n", ""), "no slides"},
		{"a slide without a title", s, "slides/000004.yaml", replace(t, deckFile, "  - title: Plan\n", "  - notes: Plan\n"), "slide 1 has no title"},
		{"no command id", s, "slides/000004.yaml", replace(t, deckFile, "command_id: c-1\n", ""), "command_id is missing"},
		{"no author", s, "slides/000004.yaml", replace(t, deckFile, "author_participant_id: ada\n", ""), "author_participant_id is missing"},
		{"another ending", s, "slides/000004.html", deckFile, "ending .yaml"},
		{"not YAML", s, "slides/000004.yaml", "slides: [unclosed\n", "not valid YAML"},
	})
}

// artifactCase is one file and what the artifact check must answer.
type artifactCase struct {
	name    string
	path    string
	content string
	reason  string // empty: the file is valid
}

// TestArtifactCheck: the artifact check finds the schema that a Markdown or YAML file names, passes
// the file to the part that owns the schema, and answers valid or invalid with reasons.
func TestArtifactCheck(t *testing.T) {
	front := func(schema string) string { return "---\nschema: " + schema + "\ntitle: A\n---\n# Text\n" }
	cases := []artifactCase{
		// Files that name a schema go to the owner.
		{"a decision", "decisions/000001.yaml", decisionFile, ""},
		{"minutes", "minutes/000001.yaml", minutesFile, ""},
		{"a note", "notes/000003.yaml", noteFile, ""},
		{"a task", "tasks/000002.yaml", taskFile, ""},
		{"a deck", "slides/000004.yaml", deckFile, ""},
		{"an invalid decision", "decisions/000001.yaml", replace(t, decisionFile, "text: Use Go for every component.\n", ""), "text is missing"},
		{"an invalid task", "tasks/000002.yaml", replace(t, taskFile, "title: Buy milk\n", ""), "title is missing"},
		{"an invalid deck", "slides/000004.yaml", replace(t, deckFile, "command_id: c-1\n", ""), "command_id is missing"},
		{"a decision in the wrong place", "notes/000001.yaml", decisionFile, "belongs in decisions/000001.yaml"},
		{"a decision as Markdown", "decisions/000001.md", front("office.decision.v1"), "ending .yaml"},
		// A schema that no part owns.
		{"an unknown schema in YAML", "other/a.yaml", "schema: office.other.v1\nx: 1\n", "not known"},
		{"an unknown schema in front matter", "docs/a.md", front("office.other.v1"), "not known"},
		{"a schema without a version", "other/a.yaml", "schema: office.decision\n", "not known"},
		{"a schema that is no text", "other/a.yaml", "schema: 3\n", "name of a schema"},
		{"an empty schema", "other/a.yaml", "schema: \"\"\n", "name of a schema"},
		// Files without a schema: any Markdown, any YAML.
		{"Markdown", "docs/readme.md", "# Title\n\nSome text with `code`.\n", ""},
		{"empty Markdown", "docs/empty.md", "", ""},
		{"Markdown that starts with a rule", "docs/rule.md", "---\n\nText without a closing rule\n", ""},
		{"Markdown with front matter and no schema", "docs/a.md", "---\ntitle: A\ntags: [x, y]\n---\nText\n", ""},
		{"Markdown with front matter and CRLF", "docs/a.md", "---\r\ntitle: A\r\n---\r\nText\r\n", ""},
		{"Markdown that is no UTF-8", "docs/a.md", "# Title \xff\xfe\n", "UTF-8"},
		{"Markdown with broken front matter", "docs/a.md", "---\ntitle: [unclosed\n---\nText\n", "front matter"},
		{"YAML", "data/a.yaml", "name: x\nitems:\n  - 1\n  - 2\n", ""},
		{"YAML with the ending yml", "data/a.yml", "name: x\n", ""},
		{"a YAML list", "data/a.yaml", "- a\n- b\n", ""},
		{"empty YAML", "data/a.yaml", "", ""},
		{"YAML with a comment only", "data/a.yaml", "# nothing\n", ""},
		{"YAML with two documents", "data/a.yaml", "a: 1\n---\nb: 2\n", ""},
		{"broken YAML", "data/a.yaml", "name: [unclosed\n", "not valid YAML"},
		{"broken YAML in the second document", "data/a.yaml", "a: 1\n---\nb: [unclosed\n", "not valid YAML"},
		// Files that are no artifacts.
		{"JSON", "data/a.json", "{}", "must end in"},
		{"plain text", "docs/a.txt", "text", "must end in"},
		{"HTML", "slides/000004.html", "<html></html>", "must end in"},
		{"no ending", "docs/LICENSE", "text", "must end in"},
		{"a session log", "sessions/s1/log.jsonl", "{}", "session logs"},
		{"Markdown in the session logs", "sessions/s1/notes.md", "# Notes\n", "session logs"},
		{"YAML in the session logs", "sessions/s1/tree-head.yaml", "a: 1\n", "session logs"},
		{"the session logs with a detour", "docs/../sessions/s1/notes.md", "# Notes\n", "session logs"},
		{"a directory that only starts like the session logs", "sessions-notes/a.md", "# Notes\n", ""},
		{"a path outside the repository", "../a.md", "# A\n", "not a path inside"},
		{"an absolute path", "/etc/a.md", "# A\n", "not a path inside"},
		{"no path", "", "# A\n", "not a path inside"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := artifactcheck.Validate(c.path, []byte(c.content))
			if c.reason == "" {
				if len(got) != 0 {
					t.Fatalf("a valid file is invalid: %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("an invalid file is valid, want a reason with %q", c.reason)
			}
			if !slices.ContainsFunc(got, func(r string) bool { return strings.Contains(r, c.reason) }) {
				t.Fatalf("reasons %q, want one with %q", got, c.reason)
			}
			for _, r := range got {
				if strings.TrimSpace(r) == "" {
					t.Fatalf("an empty reason in %q", got)
				}
			}
		})
	}
}

// TestArtifactCheckIsTheCheckOfTheOwner: the answer of the artifact check for a file with a schema
// is the answer of the check of the part that owns the schema, with the same reasons.
func TestArtifactCheckIsTheCheckOfTheOwner(t *testing.T) {
	bad := replace(t, noteFile, "text: Call the bank.\n", "")
	ownReasons := recordscheck.Validate("office.note.v1", "notes/000003.yaml", []byte(bad))
	if len(ownReasons) == 0 {
		t.Fatal("the records check accepts a note without text")
	}
	got := artifactcheck.Validate("notes/000003.yaml", []byte(bad))
	if !slices.Equal(got, ownReasons) {
		t.Fatalf("artifact check %q, records check %q", got, ownReasons)
	}
}

// stopAndCorrupt stops the hub, replaces the file at rel in its archive with content and commits
// it. A file that the check rejects is then in the archive.
func stopAndCorrupt(t *testing.T, srv *contracttest.Hub, rel, content string) {
	t.Helper()
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	repo := srv.ArchiveDir()
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	contracttest.Git(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-a", "-m", "break "+rel)
}

// wantNoStart starts the hub on the data directory and fails the test unless it stops with an
// error that names the file and gives every reason of the check.
func wantNoStart(t *testing.T, srv *contracttest.Hub, rel string, reasons []string) {
	t.Helper()
	if len(reasons) == 0 {
		t.Fatal("the check accepts the file that the test broke")
	}
	p := contracttest.Start(t, "office-hub", nil, "-addr", "127.0.0.1:0", "-data-dir", srv.DataDir,
		"-signing-key", srv.SigningKeyFile)
	select {
	case <-p.Done():
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatalf("the hub started with the file %s that its check rejects", rel)
	}
	out := p.Stderr()
	if !strings.Contains(out, rel) {
		t.Fatalf("the error does not name %s:\n%s", rel, out)
	}
	for _, r := range reasons {
		if !strings.Contains(out, r) {
			t.Fatalf("the error does not give the reason %q of the check:\n%s", r, out)
		}
	}
}

// show returns the stored file at rel in the archive of the hub.
func show(t *testing.T, srv *contracttest.Hub, rel string) string {
	t.Helper()
	return contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:"+rel)
}

// TestRecordsInterfaceUsesTheCheck: every record that the records interfaces write passes the
// check of the part, and a hub rejects at its start what the check rejects.
func TestRecordsInterfaceUsesTheCheck(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	recordDecision(t, ada, "Use Go.", "s1")
	addNote(t, ada, "Call the bank.", "")
	for _, c := range []struct{ schema, path string }{
		{"office.decision.v1", "decisions/000001.yaml"},
		{"office.note.v1", "notes/000001.yaml"},
	} {
		raw := show(t, srv, c.path)
		if !strings.HasPrefix(raw, "schema: "+c.schema+"\n") {
			t.Fatalf("%s does not start with its schema:\n%s", c.path, raw)
		}
		if reasons := recordscheck.Validate(c.schema, c.path, []byte(raw)); len(reasons) != 0 {
			t.Fatalf("the records check rejects %s that the hub wrote: %v\n%s", c.path, reasons, raw)
		}
		if reasons := artifactcheck.Validate(c.path, []byte(raw)); len(reasons) != 0 {
			t.Fatalf("the artifact check rejects %s that the hub wrote: %v", c.path, reasons)
		}
	}
	bad := replace(t, show(t, srv, "decisions/000001.yaml"), "schema: office.decision.v1", "schema: office.note.v1")
	stopAndCorrupt(t, srv, "decisions/000001.yaml", bad)
	wantNoStart(t, srv, "decisions/000001.yaml", recordscheck.Validate("office.decision.v1", "decisions/000001.yaml", []byte(bad)))
}

// TestTasksInterfaceUsesTheCheck: every task that the task interface writes passes the check of
// the part, also after an update, and a hub rejects at its start what the check rejects.
func TestTasksInterfaceUsesTheCheck(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	t1 := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Buy milk", Description: "Line one\nline two"})
	updateTask(t, ada, &taskv1.UpdateTaskRequest{CommandId: coreapi.NewCommandID(), Ref: t1.GetRef(), Status: ptr(inProgress)})
	const rel = "tasks/000001.yaml"
	raw := show(t, srv, rel)
	if !strings.HasPrefix(raw, "schema: office.task.v1\n") {
		t.Fatalf("%s does not start with its schema:\n%s", rel, raw)
	}
	if reasons := taskscheck.Validate("office.task.v1", rel, []byte(raw)); len(reasons) != 0 {
		t.Fatalf("the tasks check rejects the task that the hub wrote: %v\n%s", reasons, raw)
	}
	if reasons := artifactcheck.Validate(rel, []byte(raw)); len(reasons) != 0 {
		t.Fatalf("the artifact check rejects the task that the hub wrote: %v", reasons)
	}
	bad := replace(t, raw, "schema: office.task.v1", "schema: office.note.v1")
	stopAndCorrupt(t, srv, rel, bad)
	wantNoStart(t, srv, rel, taskscheck.Validate("office.task.v1", rel, []byte(bad)))
}

// TestSlidesInterfaceUsesTheCheck: every deck that the slides interface writes passes the check
// of the part, and a hub rejects at its start what the check rejects.
func TestSlidesInterfaceUsesTheCheck(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	render(t, ada, budgetDeck())
	const rel = "slides/000001.yaml"
	raw := show(t, srv, rel)
	if !strings.HasPrefix(raw, "schema: office.slides.v1\n") {
		t.Fatalf("%s does not start with its schema:\n%s", rel, raw)
	}
	if reasons := slidescheck.Validate("office.slides.v1", rel, []byte(raw)); len(reasons) != 0 {
		t.Fatalf("the slides check rejects the deck that the hub wrote: %v\n%s", reasons, raw)
	}
	if reasons := artifactcheck.Validate(rel, []byte(raw)); len(reasons) != 0 {
		t.Fatalf("the artifact check rejects the deck that the hub wrote: %v", reasons)
	}
	bad := replace(t, raw, "command_id:", "command_ident:")
	stopAndCorrupt(t, srv, rel, bad)
	wantNoStart(t, srv, rel, slidescheck.Validate("office.slides.v1", rel, []byte(bad)))
}
