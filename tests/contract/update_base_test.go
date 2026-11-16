package contract

// Contract tests of two gaps after a push (issue #169), with a real hub and real git:
//
//   - an update of a task never replaces a field that a push changed meanwhile, and
//   - a number whose file a push removed is not given again after the hub restarts
//     (the next number is above every name that ever lay in the directory).
//
// The tests check the final state only and never how long something takes. The case that every
// try of an update fails (pushes again and again) cannot be reached from outside: a foreign
// commit is made once for each arming, and the number of tries is far higher than that. It is
// therefore not tested here.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// A push changes the description of a task at the exact step where the hub moves main for an
// update of the status. Both changes stay: the part reads the pushed file again and applies the
// update to it. The foreign commit is made with real Git at that step, so no timing is involved.
func TestUpdateKeepsAPushedChangeOfAnotherField(t *testing.T) {
	race := contracttest.NewGitRace(t, contracttest.DefaultTenant)
	srv := contracttest.StartHub(t, race.Env)
	ada := srv.Participant(t, "ada", "Ada", human)
	task := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Plan", Description: "Original description."})
	if task.GetRef() != "local:1" {
		t.Fatalf("ref %s, want local:1", task.GetRef())
	}

	stored := contracttest.Git(t, srv.ArchiveDir(), "show", "main:tasks/000001.yaml")
	if !strings.Contains(stored, "Original description.") {
		t.Fatalf("stored task:\n%s", stored)
	}
	race.Arm(t, "tasks/000001.yaml", strings.Replace(stored, "Original description.", "Pushed description.", 1))
	done := taskv1.TaskStatus_TASK_STATUS_DONE
	res, err := ada.Task.UpdateTask(ctx10(t), &taskv1.UpdateTaskRequest{CommandId: coreapi.NewCommandID(), Ref: task.GetRef(),
		ParticipantId: ada.ParticipantID, Status: &done})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !race.Fired() {
		t.Fatal("the foreign change was not made")
	}
	for name, got := range map[string]*taskv1.Task{"the answer": res.GetTask(), "GetTask": getTaskOrNil(t, ada, task.GetRef())} {
		if got.GetStatus() != done || got.GetDescription() != "Pushed description." {
			t.Errorf("%s: status %v, description %q; want DONE and the pushed description", name, got.GetStatus(), got.GetDescription())
		}
	}
	// The archive holds both changes in the last commit.
	final := contracttest.Git(t, srv.ArchiveDir(), "show", "main:tasks/000001.yaml")
	if !strings.Contains(final, "Pushed description.") || !strings.Contains(final, "TASK_STATUS_DONE") {
		t.Errorf("stored task after the update:\n%s", final)
	}
}

// A push removes the file with the highest number of a decision, a task and a deck, and the hub
// restarts. The next number is higher than the removed one, and the other entries stay.
func TestRemovedHighestNumberDoesNotReturnAfterRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	recordDecision(t, ada, "First decision.", "")
	removedDecision := recordDecision(t, ada, "Second decision.", "")
	createTask(t, ada, &taskv1.CreateTaskRequest{Title: "First task"})
	removedTask := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Second task"})
	render(t, ada, budgetDeck())
	removedDeck := render(t, ada, budgetDeck())
	if removedDecision.GetId() != 2 || removedTask.GetRef() != "local:2" || removedDeck.GetId() != "slides/2" {
		t.Fatalf("numbers before the removal: %d, %s, %s", removedDecision.GetId(), removedTask.GetRef(), removedDeck.GetId())
	}

	if code, err := srv.Stop(10 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: exit %d, %v", code, err)
	}
	contracttest.Edit(t, srv.ArchiveDir(), "remove the files with the highest numbers", func(work string) {
		for _, f := range []string{"decisions/000002.yaml", "tasks/000002.yaml", "slides/000002.yaml"} {
			if err := os.Remove(filepath.Join(work, f)); err != nil {
				t.Fatal(err)
			}
		}
	})
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)

	// The other entries are still there, the removed ones are not.
	if ds := listDecisions(t, ada, &decisionv1.ListDecisionsRequest{}); len(ds) != 1 || ds[0].GetId() != 1 {
		t.Errorf("decisions after the restart: %v", ds)
	}
	if ts := listTasks(t, ada, &taskv1.ListTasksRequest{}).GetTasks(); len(ts) != 1 || ts[0].GetRef() != "local:1" {
		t.Errorf("tasks after the restart: %s", refsOf(ts))
	}
	if _, err := ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/1"}); err != nil {
		t.Errorf("the first deck after the restart: %v", err)
	}
	if _, err := ada.Slides.GetDeck(ctx10(t), &slidesv1.GetDeckRequest{Id: "slides/2"}); err == nil {
		t.Error("the removed deck is shown")
	}

	if d := recordDecision(t, ada, "Third decision.", ""); d.GetId() <= removedDecision.GetId() {
		t.Errorf("the next decision has ID %d, want more than %d", d.GetId(), removedDecision.GetId())
	}
	tk := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Third task"})
	if n, err := strconv.Atoi(strings.TrimPrefix(tk.GetRef(), "local:")); err != nil || n <= 2 {
		t.Errorf("the next task has ref %s, want a number above 2", tk.GetRef())
	}
	d := render(t, ada, budgetDeck())
	if n, err := strconv.Atoi(strings.TrimPrefix(d.GetId(), "slides/")); err != nil || n <= 2 {
		t.Errorf("the next deck has ID %s, want a number above 2", d.GetId())
	}
}
