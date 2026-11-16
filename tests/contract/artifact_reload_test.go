package contract

// Contract tests of the reload of the parts after a push (if-archive-watch, dec-artifact-sync,
// risk-stale-memory): a decision, a minutes file, a note, a task or a deck that someone pushes with
// real Git is seen through the interface of its part without a restart, and a part never
// overwrites a file that a push put there first. They start the real office-hub, push with the
// real git tool and use only the Connect clients. They wait for conditions with a generous
// deadline and never check how long something takes.
//
// A pushed file that is not valid cannot get through the hook of the hub. The defence of the
// parts, which ignore such a file, is reached by a change that bypasses Git access: a commit
// straight into the repository of the tenant (TestPushedInvalidFileIsIgnored). The in-process
// tests of the watch interface itself (a watcher that is too slow) lie in hub/archive.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func decisionYAML(id int, text string) string {
	return fmt.Sprintf(`schema: office.decision.v1
id: %d
text: %s
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
session_id: s1
time: "2026-11-16T10:00:00Z"
command_id: pushed-decision-%d
`, id, text, id)
}

func minutesYAML(id int, text string) string {
	return fmt.Sprintf(`schema: office.minutes.v1
id: %d
session_id: s%d
text: %s
author_participant_id: ada
author_kind: AUTHOR_KIND_AGENT
time: "2026-11-16T10:00:00Z"
command_id: pushed-minutes-%d
`, id, id, text, id)
}

func noteYAML(id int, text string) string {
	return fmt.Sprintf(`schema: office.note.v1
id: %d
text: %s
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
time: "2026-11-16T10:00:00Z"
command_id: pushed-note-%d
`, id, text, id)
}

func taskYAML(n int, title string) string {
	return fmt.Sprintf(`schema: office.task.v1
ref: "local:%d"
source: TASK_SOURCE_LOCAL
title: %s
status: TASK_STATUS_OPEN
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
create_time: "2026-11-16T10:00:00Z"
updated_by_participant_id: ada
update_time: "2026-11-16T10:00:00Z"
revision: 1
command_id: pushed-task-%d
command_ids:
  - pushed-task-%d
`, n, title, n, n)
}

func deckYAML(n int, title string) string {
	return fmt.Sprintf(`schema: office.slides.v1
id: slides/%d
title: %s
slides:
  - title: Plan
    bullets:
      - Hire two people
author_participant_id: ada
author_kind: AUTHOR_KIND_HUMAN
create_time: "2026-11-16T10:00:00Z"
command_id: pushed-deck-%d
`, n, title, n)
}

func noteTexts(t *testing.T, a *contracttest.Actor) map[uint32]string {
	t.Helper()
	out := map[uint32]string{}
	for _, n := range listNotes(t, a, &notev1.ListNotesRequest{}) {
		out[n.GetId()] = n.GetText()
	}
	return out
}

// A pushed decision, minutes file, note, task and deck is visible through the interface of its
// part without a restart. A changed file shows its new content, and a removed file is gone.
func TestPushedArtifactsAreSeenWithoutRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	p.write("decisions/000007.yaml", decisionYAML(7, "Pushed decision."))
	p.write("minutes/000002.yaml", minutesYAML(2, "Pushed minutes."))
	p.write("notes/000003.yaml", noteYAML(3, "Pushed note."))
	p.write("tasks/000005.yaml", taskYAML(5, "Pushed task"))
	p.write("slides/000004.yaml", deckYAML(4, "Pushed deck"))
	p.commit("add the artifacts of five parts")
	p.mustPush()

	waitFor(t, "the pushed decision", func() bool {
		ds, err := ada.Decision.ListDecisions(ctx10(t), &decisionv1.ListDecisionsRequest{})
		return err == nil && len(ds.GetDecisions()) == 1 && ds.GetDecisions()[0].GetId() == 7 && ds.GetDecisions()[0].GetText() == "Pushed decision."
	})
	waitFor(t, "the pushed minutes", func() bool {
		return len(listMinutes(t, ada, &minutesv1.ListMinutesRequest{})) == 1 && listMinutes(t, ada, &minutesv1.ListMinutesRequest{})[0].GetText() == "Pushed minutes."
	})
	waitFor(t, "the pushed note", func() bool { return noteTexts(t, ada)[3] == "Pushed note." })
	waitFor(t, "the pushed task", func() bool {
		res, err := ada.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{Ref: "local:5"})
		return err == nil && res.GetTask().GetTitle() == "Pushed task"
	})
	waitFor(t, "the pushed deck", func() bool {
		res, err := ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/4"})
		return err == nil && strings.Contains(string(res.GetDeck().GetContent()), "Pushed deck")
	})

	// A changed file shows its new content.
	p.commitFile("notes/000003.yaml", noteYAML(3, "Changed note."))
	p.commitFile("tasks/000005.yaml", taskYAML(5, "Changed task"))
	p.commitFile("slides/000004.yaml", deckYAML(4, "Changed deck"))
	p.mustPush()
	waitFor(t, "the changed note", func() bool { return noteTexts(t, ada)[3] == "Changed note." })
	waitFor(t, "the changed task", func() bool { return getTaskOrNil(t, ada, "local:5").GetTitle() == "Changed task" })
	waitFor(t, "the changed deck", func() bool {
		res, err := ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/4"})
		return err == nil && strings.Contains(string(res.GetDeck().GetContent()), "Changed deck")
	})

	// The next number of each part is above the pushed ones.
	n := addNote(t, ada, "Hub note", "")
	if n.GetId() != 4 {
		t.Errorf("the note after a pushed note 3 has ID %d, want 4", n.GetId())
	}
	task := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Hub task"})
	if task.GetRef() != "local:6" {
		t.Errorf("the task after a pushed task 5 has ref %s, want local:6", task.GetRef())
	}
	if d := render(t, ada, budgetDeck()); d.GetId() != "slides/5" {
		t.Errorf("the deck after a pushed deck 4 has ID %s, want slides/5", d.GetId())
	}

	// A removed file is gone.
	p.mustGit("pull", "-q", "--no-rebase", "--no-edit")
	for _, f := range []string{"notes/000003.yaml", "tasks/000005.yaml", "slides/000004.yaml", "decisions/000007.yaml", "minutes/000002.yaml"} {
		p.mustGit("rm", "-q", f)
	}
	p.mustGit("commit", "-q", "-m", "remove the pushed files")
	p.mustPush()
	waitFor(t, "the removed note", func() bool { _, ok := noteTexts(t, ada)[3]; return !ok })
	waitFor(t, "the removed task", func() bool { return getTaskOrNil(t, ada, "local:5") == nil })
	waitFor(t, "the removed deck", func() bool {
		_, err := ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/4"})
		return err != nil
	})
	waitFor(t, "the removed decision", func() bool { return len(listDecisions(t, ada, &decisionv1.ListDecisionsRequest{})) == 0 })
	waitFor(t, "the removed minutes", func() bool { return len(listMinutes(t, ada, &minutesv1.ListMinutesRequest{})) == 0 })
	// The hub's own records stay.
	if got := noteTexts(t, ada); got[4] != "Hub note" {
		t.Errorf("notes after the removal: %v", got)
	}
	// A removed number is not taken again.
	if n := addNote(t, ada, "After removal", ""); n.GetId() != 5 {
		t.Errorf("a note after a removed pushed note has ID %d, want 5", n.GetId())
	}
}

// A push of a record with the next number while the hub creates a record gives two records with
// different IDs and loses none. The foreign commit is made with real Git at the exact step where
// the hub moves main, so no timing is involved.
func TestHubCreateTakesNextIDWhenAPushCameFirst(t *testing.T) {
	race := contracttest.NewGitRace(t, contracttest.DefaultTenant)
	srv := contracttest.StartHub(t, race.Env)
	ada := srv.Participant(t, "ada", "Ada", human)

	race.Arm(t, "notes/000001.yaml", noteYAML(1, "Foreign note."))
	n := addNote(t, ada, "Hub note", "")
	if !race.Fired() {
		t.Fatal("the foreign change was not made")
	}
	if n.GetId() != 2 {
		t.Errorf("the hub note has ID %d, want 2", n.GetId())
	}
	waitFor(t, "both notes", func() bool {
		got := noteTexts(t, ada)
		return len(got) == 2 && got[1] == "Foreign note." && got[2] == "Hub note"
	})
	files := strings.Join(contracttest.Files(t, srv.ArchiveDir()), ",")
	if !strings.Contains(files, "notes/000001.yaml") || !strings.Contains(files, "notes/000002.yaml") {
		t.Errorf("files in the repository: %s", files)
	}
	if got := contracttest.Git(t, srv.ArchiveDir(), "show", "main:notes/000001.yaml"); !strings.Contains(got, "Foreign note.") {
		t.Errorf("the pushed file was overwritten:\n%s", got)
	}
}

// The same with a push that the hub has not seen yet: a participant pushes the next note while the hub
// adds notes. Every number is used once, and nothing is lost. The test checks the final state.
func TestPushedAndCreatedNotesKeepBoth(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	p := newPusher(t, srv, ada, "Ada", contracttest.DefaultTenant)

	const rounds = 8
	created := make(chan uint32, rounds)
	pushed := map[int]string{}
	for i := 0; i < rounds; i++ {
		done := make(chan struct{})
		go func(i int) {
			defer close(done)
			res, err := ada.Note.AddNote(ctx10(t), &notev1.AddNoteRequest{CommandId: coreapi.NewCommandID(), Text: fmt.Sprintf("hub %d", i), AuthorParticipantId: ada.ParticipantID})
			if err != nil {
				t.Errorf("add note %d: %v", i, err)
				return
			}
			created <- res.GetNote().GetId()
		}(i)
		// Push the file with the next number of the clone, until the push is accepted.
		for attempt := 0; ; attempt++ {
			if attempt == 50 {
				t.Fatal("the push was refused 50 times")
			}
			p.mustGit("fetch", "-q", "origin")
			p.mustGit("reset", "-q", "--hard", "origin/main")
			id := nextNoteNumber(t, p.dir)
			text := fmt.Sprintf("pushed %d", i)
			p.write(fmt.Sprintf("notes/%06d.yaml", id), noteYAML(id, text))
			p.commit("note " + text)
			if _, code := p.push(); code == 0 {
				pushed[id] = text
				break
			}
		}
		<-done
	}
	close(created)

	seen := map[uint32]bool{}
	for id := range created {
		if seen[id] {
			t.Errorf("the hub gave ID %d twice", id)
		}
		seen[id] = true
	}
	for id := range pushed {
		if seen[uint32(id)] {
			t.Errorf("ID %d is the ID of a pushed note and of a hub note", id)
		}
	}
	waitFor(t, "all notes", func() bool { return len(noteTexts(t, ada)) == 2*rounds })
	got := noteTexts(t, ada)
	for id, text := range pushed {
		if got[uint32(id)] != text {
			t.Errorf("pushed note %d: %q, want %q", id, got[uint32(id)], text)
		}
	}
	var hubTexts []string
	for id := range seen {
		hubTexts = append(hubTexts, got[id])
	}
	sort.Strings(hubTexts)
	for i, text := range hubTexts {
		if text != fmt.Sprintf("hub %d", i) {
			t.Errorf("hub notes: %v", hubTexts)
			break
		}
	}
	// The files in the repository are the same set.
	if n := len(strings.Fields(contracttest.Git(t, srv.ArchiveDir(), "ls-tree", "-r", "--name-only", "main", "notes"))); n != 2*rounds {
		t.Errorf("%d note files in main, want %d", n, 2*rounds)
	}
}

// A file that is not valid and bypasses the hook of Git access (a commit straight into the
// repository) does not stop the hub: the parts log it once and ignore it, they never take its
// number, and everything else works. The hook refuses such a file in a push (see gitpush_test.go),
// so this is the only way to reach the defence from outside.
func TestPushedInvalidFileIsIgnored(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	addNote(t, ada, "First", "")

	broken := strings.Replace(noteYAML(9, "Broken."), "author_participant_id: ada\n", "", 1)
	contracttest.Edit(t, srv.ArchiveDir(), "a broken note", func(work string) {
		contracttest.WriteFile(t, work, "notes/000009.yaml", broken)
		contracttest.WriteFile(t, work, "tasks/000009.yaml", "schema: office.task.v1\nref: nonsense\n")
		contracttest.WriteFile(t, work, "slides/000009.yaml", "not: a deck\n")
	})
	// The next commit of the hub reports the foreign change to the parts.
	second := addNote(t, ada, "Second", "")
	waitFor(t, "the log of the ignored note", func() bool {
		return strings.Contains(srv.Stderr(), "ignore the pushed file notes/000009.yaml")
	})
	if got := noteTexts(t, ada); len(got) != 2 {
		t.Errorf("notes: %v", got)
	}
	// The number of the broken file is taken: the next notes come after it.
	if second.GetId() != 2 {
		t.Errorf("second note has ID %d, want 2", second.GetId())
	}
	if later := addNote(t, ada, "Later", ""); later.GetId() != 10 {
		t.Errorf("a note after the broken file 9 has ID %d, want 10", later.GetId())
	}
	if res := listTasks(t, ada, &taskv1.ListTasksRequest{}); len(res.GetTasks()) != 0 {
		t.Errorf("tasks: %v", res.GetTasks())
	}
	if task := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Works"}); task.GetRef() == "" {
		t.Error("no task ref")
	}
	if d := render(t, ada, budgetDeck()); d.GetId() == "" {
		t.Error("no deck ID")
	}
}

// nextNoteNumber returns the number after the highest note file in a clone.
func nextNoteNumber(t *testing.T, clone string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(clone, "notes"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	highest := 0
	for _, e := range entries {
		var n int
		if _, err := fmt.Sscanf(e.Name(), "%d.yaml", &n); err == nil && n > highest {
			highest = n
		}
	}
	return highest + 1
}

func getTaskOrNil(t *testing.T, a *contracttest.Actor, ref string) *taskv1.Task {
	t.Helper()
	res, err := a.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{Ref: ref})
	if err != nil {
		return nil
	}
	return res.GetTask()
}
