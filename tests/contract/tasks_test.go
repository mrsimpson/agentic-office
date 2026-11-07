package contract

// Contract tests of the tasks (bb-tasks) through the task interface (if-task-api). They start the
// real office-hub, use only the Connect client, and read the archive repository with git.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// createTask creates a task as a and fails the test on an error.
func createTask(t *testing.T, a *contracttest.Actor, req *taskv1.CreateTaskRequest) *taskv1.Task {
	t.Helper()
	if req.CommandId == "" {
		req.CommandId = ids.NewCommandID()
	}
	req.AuthorParticipantId = a.ParticipantID
	res, err := a.Task.CreateTask(ctx10(t), req)
	if err != nil {
		t.Fatalf("create %q as %s: %v", req.GetTitle(), a.ParticipantID, err)
	}
	if res.GetDuplicate() {
		t.Fatalf("create %q: duplicate on the first call", req.GetTitle())
	}
	return res.GetTask()
}

// updateTask changes a task as a and fails the test on an error.
func updateTask(t *testing.T, a *contracttest.Actor, req *taskv1.UpdateTaskRequest) *taskv1.UpdateTaskResponse {
	t.Helper()
	if req.CommandId == "" {
		req.CommandId = ids.NewCommandID()
	}
	req.ParticipantId = a.ParticipantID
	res, err := a.Task.UpdateTask(ctx10(t), req)
	if err != nil {
		t.Fatalf("update %s as %s: %v", req.GetRef(), a.ParticipantID, err)
	}
	return res
}

func getTask(t *testing.T, a *contracttest.Actor, ref string) *taskv1.Task {
	t.Helper()
	res, err := a.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{Ref: ref})
	if err != nil {
		t.Fatalf("get %s: %v", ref, err)
	}
	return res.GetTask()
}

func listTasks(t *testing.T, a *contracttest.Actor, req *taskv1.ListTasksRequest) *taskv1.ListTasksResponse {
	t.Helper()
	res, err := a.Task.ListTasks(ctx10(t), req)
	if err != nil {
		t.Fatalf("list tasks %v: %v", req, err)
	}
	return res
}

func refsOf(ts []*taskv1.Task) string {
	refs := make([]string, len(ts))
	for i, tk := range ts {
		refs[i] = tk.GetRef()
	}
	return strings.Join(refs, ",")
}

const (
	open       = taskv1.TaskStatus_TASK_STATUS_OPEN
	inProgress = taskv1.TaskStatus_TASK_STATUS_IN_PROGRESS
	done       = taskv1.TaskStatus_TASK_STATUS_DONE
	cancelled  = taskv1.TaskStatus_TASK_STATUS_CANCELLED
)

// TestTasksCreateGetList: a new task is a local draft with a ref, status open by default, the
// author and its kind from the token, and revision 1. It comes back by ref and in the list,
// filtered by status, assignee and session, and paged with the page token. Every task is a
// YAML file in the archive, created in one commit.
func TestTasksCreateGetList(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	before := time.Now().Add(-time.Second)
	t1 := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Write the release notes", Description: "Line one\nline two",
		AssigneeParticipantId: "bot", SessionId: "s1"})
	t2 := createTask(t, bot, &taskv1.CreateTaskRequest{Title: "Book the room", Status: inProgress})
	t3 := createTask(t, bot, &taskv1.CreateTaskRequest{Title: "Check the budget", AssigneeParticipantId: "ada", SessionId: "s1", Status: done})
	if t1.GetRef() == "" || t1.GetRef() == t2.GetRef() || t2.GetRef() == t3.GetRef() || t1.GetRef() == t3.GetRef() {
		t.Fatalf("refs are not distinct: %q %q %q", t1.GetRef(), t2.GetRef(), t3.GetRef())
	}
	for _, tk := range []*taskv1.Task{t1, t2, t3} {
		if tk.GetSource() != taskv1.TaskSource_TASK_SOURCE_LOCAL || tk.GetRevision() != 1 || tk.GetCommandId() == "" ||
			tk.GetUpdatedByParticipantId() != tk.GetAuthorParticipantId() || !proto.Equal(tk.GetCreateTime(), tk.GetUpdateTime()) {
			t.Fatalf("new task: %v", tk)
		}
		if tm := tk.GetCreateTime().AsTime(); tm.Before(before) || tm.After(time.Now().Add(time.Second)) {
			t.Fatalf("create time %v", tm)
		}
	}
	if t1.GetStatus() != open || t1.GetAuthorParticipantId() != "ada" || t1.GetAuthorKind() != taskv1.AuthorKind_AUTHOR_KIND_HUMAN ||
		t1.GetAssigneeParticipantId() != "bot" || t1.GetSessionId() != "s1" || t1.GetDescription() != "Line one\nline two" ||
		t1.GetTitle() != "Write the release notes" {
		t.Fatalf("task 1: %v", t1)
	}
	if t2.GetStatus() != inProgress || t2.GetAuthorKind() != taskv1.AuthorKind_AUTHOR_KIND_AGENT || t2.GetAssigneeParticipantId() != "" {
		t.Fatalf("task 2: %v", t2)
	}
	for _, tk := range []*taskv1.Task{t1, t2, t3} {
		if got := getTask(t, bot, tk.GetRef()); !proto.Equal(got, tk) {
			t.Fatalf("get %s: %v, want %v", tk.GetRef(), got, tk)
		}
	}

	all := refsOf([]*taskv1.Task{t1, t2, t3})
	r := func(ts ...*taskv1.Task) string { return refsOf(ts) }
	for _, c := range []struct {
		name string
		req  *taskv1.ListTasksRequest
		want string
	}{
		{"all", &taskv1.ListTasksRequest{}, all},
		{"open", &taskv1.ListTasksRequest{Statuses: []taskv1.TaskStatus{open}}, r(t1)},
		{"open or done", &taskv1.ListTasksRequest{Statuses: []taskv1.TaskStatus{done, open}}, r(t1, t3)},
		{"cancelled", &taskv1.ListTasksRequest{Statuses: []taskv1.TaskStatus{cancelled}}, ""},
		{"assignee bot", &taskv1.ListTasksRequest{AssigneeParticipantId: "bot"}, r(t1)},
		{"session s1", &taskv1.ListTasksRequest{SessionId: "s1"}, r(t1, t3)},
		{"session s1, done", &taskv1.ListTasksRequest{SessionId: "s1", Statuses: []taskv1.TaskStatus{done}}, r(t3)},
		{"session s1, assignee ada", &taskv1.ListTasksRequest{SessionId: "s1", AssigneeParticipantId: "ada"}, r(t3)},
		{"unknown session", &taskv1.ListTasksRequest{SessionId: "nope"}, ""},
	} {
		res := listTasks(t, ada, c.req)
		if got := refsOf(res.GetTasks()); got != c.want || res.GetNextPageToken() != "" {
			t.Fatalf("list %s: %q (next %q), want %q", c.name, got, res.GetNextPageToken(), c.want)
		}
	}
	if got := listTasks(t, ada, &taskv1.ListTasksRequest{}).GetTasks(); !proto.Equal(got[0], t1) {
		t.Fatalf("listed task 1: %v", got[0])
	}

	// Paging: the token reads the next page, with the filters; the last page has no token.
	p1 := listTasks(t, ada, &taskv1.ListTasksRequest{Limit: 2})
	if refsOf(p1.GetTasks()) != r(t1, t2) || p1.GetNextPageToken() == "" {
		t.Fatalf("page 1: %v", p1)
	}
	p2 := listTasks(t, ada, &taskv1.ListTasksRequest{Limit: 2, PageToken: p1.GetNextPageToken()})
	if refsOf(p2.GetTasks()) != r(t3) || p2.GetNextPageToken() != "" {
		t.Fatalf("page 2: %v", p2)
	}
	f1 := listTasks(t, ada, &taskv1.ListTasksRequest{SessionId: "s1", Limit: 1})
	f2 := listTasks(t, ada, &taskv1.ListTasksRequest{SessionId: "s1", Limit: 1, PageToken: f1.GetNextPageToken()})
	if refsOf(f1.GetTasks()) != r(t1) || refsOf(f2.GetTasks()) != r(t3) || f2.GetNextPageToken() != "" {
		t.Fatalf("pages of s1: %v, %v", f1, f2)
	}
	// A full last page has no token if no more task matches.
	if p := listTasks(t, ada, &taskv1.ListTasksRequest{Limit: 3}); p.GetNextPageToken() != "" {
		t.Fatalf("a page with all tasks has a next token: %v", p)
	}

	// Without a limit the hub returns at most 100 tasks, and never more than 1000.
	for i := range 102 {
		createTask(t, bot, &taskv1.CreateTaskRequest{Title: fmt.Sprintf("task %d", i+4)})
	}
	if res := listTasks(t, bot, &taskv1.ListTasksRequest{}); len(res.GetTasks()) != 100 || res.GetNextPageToken() == "" {
		t.Fatalf("default limit: %d tasks, next %q", len(res.GetTasks()), res.GetNextPageToken())
	}
	if n := len(listTasks(t, bot, &taskv1.ListTasksRequest{Limit: 5000}).GetTasks()); n != 105 {
		t.Fatalf("limit 5000: %d tasks, want 105", n)
	}
	var got []*taskv1.Task
	token := ""
	for {
		page := listTasks(t, bot, &taskv1.ListTasksRequest{PageToken: token, Limit: 40})
		got = append(got, page.GetTasks()...)
		if token = page.GetNextPageToken(); token == "" {
			break
		}
	}
	if len(got) != 105 || got[0].GetRef() != t1.GetRef() || got[104].GetTitle() != "task 105" {
		t.Fatalf("paged tasks: %d", len(got))
	}

	// Every task is its own YAML file in the archive, created in its own commit, with the fields
	// of the contract.
	files := strings.Fields(contracttest.Git(t, srv.ArchiveDir(), "ls-tree", "--name-only", "HEAD", "tasks/"))
	if len(files) != 105 || files[0] != "tasks/000001.yaml" {
		t.Fatalf("task files: %d, first %v", len(files), files[:1])
	}
	y := archivedYAML(t, srv, "tasks/000001.yaml")
	if y["ref"] != t1.GetRef() || y["source"] != "TASK_SOURCE_LOCAL" || y["title"] != "Write the release notes" ||
		y["description"] != "Line one\nline two" || y["assignee_participant_id"] != "bot" || y["status"] != "TASK_STATUS_OPEN" ||
		y["session_id"] != "s1" || y["author_participant_id"] != "ada" || y["author_kind"] != "AUTHOR_KIND_HUMAN" ||
		y["revision"] != 1 || y["command_id"] != t1.GetCommandId() || y["create_time"] == nil {
		t.Fatalf("tasks/000001.yaml: %v", y)
	}
	if raw := contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD:tasks/000001.yaml"); !strings.Contains(raw, "description: |-\n    Line one\n    line two\n") {
		t.Errorf("a text with line breaks is not a literal block:\n%s", raw)
	}
	if n := commitsOf(t, srv, "tasks/000002.yaml"); n != 1 {
		t.Fatalf("tasks/000002.yaml: %d commits, want 1", n)
	}
	if n := commitsOf(t, srv, "tasks"); n != 105 {
		t.Fatalf("tasks: %d commits, want one per task", n)
	}
}

// TestTasksUpdate: an update changes only the fields it sets, grows the revision, records who
// changed it, and rewrites the file in a new commit. An update that changes nothing makes no
// commit.
func TestTasksUpdate(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)
	tk := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Plan", Description: "Old", AssigneeParticipantId: "ada", SessionId: "s1"})
	other := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Other"})

	res := updateTask(t, bot, &taskv1.UpdateTaskRequest{Ref: tk.GetRef(), Status: ptr(inProgress), AssigneeParticipantId: ptr("bot")})
	u := res.GetTask()
	if res.GetDuplicate() || u.GetRevision() != 2 || u.GetStatus() != inProgress || u.GetAssigneeParticipantId() != "bot" ||
		u.GetTitle() != "Plan" || u.GetDescription() != "Old" || u.GetSessionId() != "s1" || u.GetRef() != tk.GetRef() ||
		u.GetAuthorParticipantId() != "ada" || u.GetUpdatedByParticipantId() != "bot" || u.GetCommandId() != tk.GetCommandId() ||
		!proto.Equal(u.GetCreateTime(), tk.GetCreateTime()) || !u.GetUpdateTime().AsTime().After(tk.GetCreateTime().AsTime()) {
		t.Fatalf("after the first update: %v", u)
	}
	u = updateTask(t, ada, &taskv1.UpdateTaskRequest{Ref: tk.GetRef(), Title: ptr("Plan v2"), Description: ptr(""),
		AssigneeParticipantId: ptr(""), SessionId: ptr(""), Status: ptr(done)}).GetTask()
	if u.GetRevision() != 3 || u.GetTitle() != "Plan v2" || u.GetDescription() != "" || u.GetAssigneeParticipantId() != "" ||
		u.GetSessionId() != "" || u.GetStatus() != done || u.GetUpdatedByParticipantId() != "ada" {
		t.Fatalf("after the second update: %v", u)
	}
	u = updateTask(t, ada, &taskv1.UpdateTaskRequest{Ref: tk.GetRef(), SessionId: ptr("s2")}).GetTask()
	if u.GetRevision() != 4 || u.GetSessionId() != "s2" {
		t.Fatalf("link to s2: %v", u)
	}
	if got := getTask(t, bot, tk.GetRef()); !proto.Equal(got, u) {
		t.Fatalf("get after update: %v, want %v", got, u)
	}
	if got := refsOf(listTasks(t, bot, &taskv1.ListTasksRequest{Statuses: []taskv1.TaskStatus{done}, SessionId: "s2"}).GetTasks()); got != tk.GetRef() {
		t.Fatalf("list by the new status and session: %q", got)
	}
	if got := getTask(t, bot, other.GetRef()); !proto.Equal(got, other) {
		t.Fatalf("the other task changed: %v", got)
	}

	// Git keeps the history: one commit per change, and the file holds the current task.
	path := "tasks/000001.yaml"
	if n := commitsOf(t, srv, path); n != 4 {
		t.Fatalf("%s: %d commits, want 4", path, n)
	}
	if y := archivedYAML(t, srv, path); y["title"] != "Plan v2" || y["status"] != "TASK_STATUS_DONE" || y["revision"] != 4 ||
		y["session_id"] != "s2" || y["updated_by_participant_id"] != "ada" || y["author_participant_id"] != "ada" {
		t.Fatalf("%s: %v", path, y)
	}
	if first := contracttest.Git(t, srv.ArchiveDir(), "show", "HEAD~3:"+path); !strings.Contains(first, "TASK_STATUS_OPEN") {
		t.Fatalf("the first version of the task is not in the history:\n%s", first)
	}

	// An update to the values the task already has changes nothing.
	same := updateTask(t, bot, &taskv1.UpdateTaskRequest{Ref: tk.GetRef(), Title: ptr("Plan v2"), Status: ptr(done)})
	if same.GetDuplicate() || !proto.Equal(same.GetTask(), u) {
		t.Fatalf("update without a change: %v", same)
	}
	if n := commitsOf(t, srv, path); n != 4 {
		t.Fatalf("an update without a change made a commit: %d", n)
	}
}

// TestTasksIdempotent: a repeated create or update has no extra effect. Reusing a command ID for
// other content, for another task, or across create and update fails.
func TestTasksIdempotent(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	bot := srv.Participant(t, "bot", "Bot", agent)

	creq := &taskv1.CreateTaskRequest{CommandId: ids.NewCommandID(), Title: "Plan", Description: "d", AssigneeParticipantId: "bot",
		SessionId: "s1", AuthorParticipantId: "ada"}
	first, err := ada.Task.CreateTask(ctx10(t), creq)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		again, err := ada.Task.CreateTask(ctx10(t), creq)
		if err != nil || !again.GetDuplicate() || !proto.Equal(again.GetTask(), first.GetTask()) {
			t.Fatalf("repeated create: %v, %v", again, err)
		}
	}
	// The default status is the same as open.
	withOpen := proto.Clone(creq).(*taskv1.CreateTaskRequest)
	withOpen.Status = open
	if again, err := ada.Task.CreateTask(ctx10(t), withOpen); err != nil || !again.GetDuplicate() {
		t.Fatalf("repeat with status open: %v, %v", again, err)
	}
	for name, change := range map[string]func(r *taskv1.CreateTaskRequest){
		"other title":       func(r *taskv1.CreateTaskRequest) { r.Title = "Other" },
		"other description": func(r *taskv1.CreateTaskRequest) { r.Description = "" },
		"other assignee":    func(r *taskv1.CreateTaskRequest) { r.AssigneeParticipantId = "ada" },
		"other session":     func(r *taskv1.CreateTaskRequest) { r.SessionId = "" },
		"other status":      func(r *taskv1.CreateTaskRequest) { r.Status = done },
	} {
		r := proto.Clone(creq).(*taskv1.CreateTaskRequest)
		change(r)
		_, err := ada.Task.CreateTask(ctx10(t), r)
		wantCode(t, "create with "+name, err, connect.CodeAlreadyExists)
	}
	byBot := proto.Clone(creq).(*taskv1.CreateTaskRequest)
	byBot.AuthorParticipantId = "bot"
	_, err = bot.Task.CreateTask(ctx10(t), byBot)
	wantCode(t, "create by another author", err, connect.CodeAlreadyExists)

	ref := first.GetTask().GetRef()
	ureq := &taskv1.UpdateTaskRequest{CommandId: ids.NewCommandID(), Ref: ref, ParticipantId: "bot", Status: ptr(done)}
	u1, err := bot.Task.UpdateTask(ctx10(t), ureq)
	if err != nil || u1.GetDuplicate() || u1.GetTask().GetRevision() != 2 {
		t.Fatalf("update: %v, %v", u1, err)
	}
	// A later change does not undo the earlier one when the earlier one is repeated.
	updateTask(t, ada, &taskv1.UpdateTaskRequest{Ref: ref, Status: ptr(cancelled)})
	again, err := bot.Task.UpdateTask(ctx10(t), ureq)
	if err != nil || !again.GetDuplicate() || again.GetTask().GetRevision() != 3 || again.GetTask().GetStatus() != cancelled {
		t.Fatalf("repeated update: %v, %v", again, err)
	}
	// After a change, a repeated create still returns the task.
	if again, err := ada.Task.CreateTask(ctx10(t), creq); err != nil || !again.GetDuplicate() || again.GetTask().GetRevision() != 3 {
		t.Fatalf("repeated create after updates: %v, %v", again, err)
	}

	other := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Other"})
	_, err = bot.Task.UpdateTask(ctx10(t), &taskv1.UpdateTaskRequest{CommandId: ureq.GetCommandId(), Ref: other.GetRef(),
		ParticipantId: "bot", Status: ptr(done)})
	wantCode(t, "update of another task with a used command ID", err, connect.CodeAlreadyExists)
	_, err = ada.Task.UpdateTask(ctx10(t), &taskv1.UpdateTaskRequest{CommandId: creq.GetCommandId(), Ref: ref,
		ParticipantId: "ada", Title: ptr("x")})
	wantCode(t, "update with the command ID of a create", err, connect.CodeAlreadyExists)
	_, err = ada.Task.CreateTask(ctx10(t), &taskv1.CreateTaskRequest{CommandId: ureq.GetCommandId(), Title: "Plan", AuthorParticipantId: "ada"})
	wantCode(t, "create with the command ID of an update", err, connect.CodeAlreadyExists)

	if got := refsOf(listTasks(t, ada, &taskv1.ListTasksRequest{}).GetTasks()); got != ref+","+other.GetRef() {
		t.Fatalf("tasks: %q", got)
	}
	if n := commitsOf(t, srv, "tasks"); n != 4 {
		t.Fatalf("tasks: %d commits, want 4 (two creates, two updates)", n)
	}
}

// TestTasksErrors: bad requests, an unknown task, a wrong participant, no token and stage links.
func TestTasksErrors(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	tk := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Plan"})
	okC := func() *taskv1.CreateTaskRequest {
		return &taskv1.CreateTaskRequest{CommandId: ids.NewCommandID(), Title: "x", AuthorParticipantId: "ada"}
	}
	okU := func() *taskv1.UpdateTaskRequest {
		return &taskv1.UpdateTaskRequest{CommandId: ids.NewCommandID(), Ref: tk.GetRef(), ParticipantId: "ada", Title: ptr("y")}
	}
	for name, change := range map[string]func(*taskv1.CreateTaskRequest){
		"no command_id":  func(r *taskv1.CreateTaskRequest) { r.CommandId = "" },
		"no title":       func(r *taskv1.CreateTaskRequest) { r.Title = "" },
		"blank title":    func(r *taskv1.CreateTaskRequest) { r.Title = " \n\t" },
		"unknown status": func(r *taskv1.CreateTaskRequest) { r.Status = 99 },
		"no author":      func(r *taskv1.CreateTaskRequest) { r.AuthorParticipantId = "" },
	} {
		r := okC()
		change(r)
		_, err := ada.Task.CreateTask(ctx10(t), r)
		wantCode(t, "create with "+name, err, connect.CodeInvalidArgument)
	}
	for name, change := range map[string]func(*taskv1.UpdateTaskRequest){
		"no command_id":      func(r *taskv1.UpdateTaskRequest) { r.CommandId = "" },
		"no ref":             func(r *taskv1.UpdateTaskRequest) { r.Ref = "" },
		"no participant":     func(r *taskv1.UpdateTaskRequest) { r.ParticipantId = "" },
		"no field":           func(r *taskv1.UpdateTaskRequest) { r.Title = nil },
		"blank title":        func(r *taskv1.UpdateTaskRequest) { r.Title = ptr(" ") },
		"unspecified status": func(r *taskv1.UpdateTaskRequest) { r.Status = ptr(taskv1.TaskStatus_TASK_STATUS_UNSPECIFIED) },
		"unknown status":     func(r *taskv1.UpdateTaskRequest) { r.Status = ptr(taskv1.TaskStatus(42)) },
	} {
		r := okU()
		change(r)
		_, err := ada.Task.UpdateTask(ctx10(t), r)
		wantCode(t, "update with "+name, err, connect.CodeInvalidArgument)
	}
	_, err := ada.Task.ListTasks(ctx10(t), &taskv1.ListTasksRequest{Statuses: []taskv1.TaskStatus{taskv1.TaskStatus_TASK_STATUS_UNSPECIFIED}})
	wantCode(t, "list with an unspecified status", err, connect.CodeInvalidArgument)
	_, err = ada.Task.ListTasks(ctx10(t), &taskv1.ListTasksRequest{PageToken: "not a token"})
	wantCode(t, "list with a bad page token", err, connect.CodeInvalidArgument)
	_, err = ada.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{})
	wantCode(t, "get without a ref", err, connect.CodeInvalidArgument)

	// Unknown tasks, also refs of other schemes, are not found.
	for _, ref := range []string{"local:2", "local:0", "local:01", "local:x", "github:owner/repo#12", "nope"} {
		_, err := ada.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{Ref: ref})
		wantCode(t, "get "+ref, err, connect.CodeNotFound)
		u := okU()
		u.Ref = ref
		_, err = ada.Task.UpdateTask(ctx10(t), u)
		wantCode(t, "update "+ref, err, connect.CodeNotFound)
	}

	c := okC()
	c.AuthorParticipantId = "bob"
	_, err = ada.Task.CreateTask(ctx10(t), c)
	wantCode(t, "create for another author", err, connect.CodePermissionDenied)
	u := okU()
	u.ParticipantId = "bob"
	_, err = ada.Task.UpdateTask(ctx10(t), u)
	wantCode(t, "update as another participant", err, connect.CodePermissionDenied)

	link, err := ada.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: "s1", CommandId: ids.NewCommandID()})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		cl   contracttest.Clients
		code connect.Code
	}{
		"without token":     {srv.Anonymous(t), connect.CodeUnauthenticated},
		"with a bad token":  {srv.WithToken(t, "not-a-token"), connect.CodeUnauthenticated},
		"with a stage link": {srv.WithToken(t, stageToken(t, link.GetUrl())), connect.CodePermissionDenied},
	} {
		_, err := c.cl.Task.CreateTask(ctx10(t), okC())
		wantCode(t, "create "+name, err, c.code)
		_, err = c.cl.Task.GetTask(ctx10(t), &taskv1.GetTaskRequest{Ref: tk.GetRef()})
		wantCode(t, "get "+name, err, c.code)
		_, err = c.cl.Task.ListTasks(ctx10(t), &taskv1.ListTasksRequest{})
		wantCode(t, "list "+name, err, c.code)
		_, err = c.cl.Task.UpdateTask(ctx10(t), okU())
		wantCode(t, "update "+name, err, c.code)
	}
	if got := listTasks(t, ada, &taskv1.ListTasksRequest{}).GetTasks(); len(got) != 1 || !proto.Equal(got[0], tk) {
		t.Fatalf("failed calls changed the tasks: %v", got)
	}
	if n := commitsOf(t, srv, "tasks"); n != 1 {
		t.Fatalf("failed calls made commits: %d", n)
	}
}

// TestTasksSurviveRestart: the hub reads the tasks back from the archive when it starts. Refs,
// revisions and idempotency of creates and updates go on after the restart.
func TestTasksSurviveRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	ada := srv.Participant(t, "ada", "Ada", human)
	t1 := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "123", Description: "Line one\n\n  indented: yes # not a comment\n"})
	ureq := &taskv1.UpdateTaskRequest{CommandId: ids.NewCommandID(), Ref: t1.GetRef(), Status: ptr(inProgress)}
	u1 := updateTask(t, ada, ureq).GetTask()
	t2 := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "Second", AssigneeParticipantId: "ada", SessionId: "s1"})
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}

	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	ada = srv.WithKey(t, ada.Key)
	got := listTasks(t, ada, &taskv1.ListTasksRequest{}).GetTasks()
	if len(got) != 2 || !proto.Equal(got[0], u1) || !proto.Equal(got[1], t2) {
		t.Fatalf("tasks after restart: %v, want %v and %v", got, u1, t2)
	}
	again, err := ada.Task.UpdateTask(ctx10(t), ureq)
	if err != nil || !again.GetDuplicate() || again.GetTask().GetRevision() != 2 {
		t.Fatalf("repeated update after restart: %v, %v", again, err)
	}
	againC, err := ada.Task.CreateTask(ctx10(t), &taskv1.CreateTaskRequest{CommandId: t2.GetCommandId(), Title: "Second",
		AssigneeParticipantId: "ada", SessionId: "s1", AuthorParticipantId: "ada"})
	if err != nil || !againC.GetDuplicate() || againC.GetTask().GetRef() != t2.GetRef() {
		t.Fatalf("repeated create after restart: %v, %v", againC, err)
	}
	if u := updateTask(t, ada, &taskv1.UpdateTaskRequest{Ref: t1.GetRef(), Status: ptr(done)}).GetTask(); u.GetRevision() != 3 {
		t.Fatalf("update after restart: %v", u)
	}
	t3 := createTask(t, ada, &taskv1.CreateTaskRequest{Title: "After the restart"})
	if t3.GetRef() == t1.GetRef() || t3.GetRef() == t2.GetRef() {
		t.Fatalf("the ref of a new task is reused: %s", t3.GetRef())
	}
	if y := archivedYAML(t, srv, "tasks/000001.yaml"); y["title"] != "123" || y["description"] != t1.GetDescription() {
		t.Fatalf("texts must stay strings: %v", y)
	}
}

func ptr[T any](v T) *T { return &v }
