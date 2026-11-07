package tasks

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
)

const (
	// DefaultLimit is the number of tasks ListTasks returns when the request sets no limit.
	DefaultLimit = 100
	// MaxLimit is the largest number of tasks ListTasks returns. A larger limit is reduced.
	MaxLimit = 1000
	// Dir is the directory of the tasks in the archive.
	Dir = "tasks"
	// localScheme is the scheme of the refs of local drafts.
	localScheme = "local:"
)

// entry is a stored task with the command IDs that changed it, oldest first.
type entry struct {
	task     *taskv1.Task
	commands []string
}

// Service implements the task interface (if-task-api).
type Service struct {
	arch storage.Archive

	mu    sync.Mutex // one write at a time; guards all and byCmd
	all   []*entry   // index n-1 holds the task with the local number n
	byCmd map[string]*entry
}

var _ taskv1connect.TaskServiceHandler = (*Service)(nil)

// Open reads all tasks from the archive and returns the service that stores tasks there.
func Open(ctx context.Context, arch storage.Archive) (*Service, error) {
	s := &Service{arch: arch, byCmd: map[string]*entry{}}
	files, err := arch.Read(ctx, Dir)
	if err != nil {
		return nil, fmt.Errorf("tasks: read %s: %w", Dir, err)
	}
	byNum := map[uint64]*entry{}
	for _, f := range files {
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		t, cmds, err := decode(f.Data)
		if err != nil {
			return nil, fmt.Errorf("tasks: %s: %w", f.Path, err)
		}
		n, ok := localNumber(t.GetRef())
		switch {
		case !ok:
			return nil, fmt.Errorf("tasks: %s: ref %q is not a local task", f.Path, t.GetRef())
		case f.Path != filePath(n):
			return nil, fmt.Errorf("tasks: %s: ref %q belongs in %s", f.Path, t.GetRef(), filePath(n))
		case byNum[n] != nil:
			return nil, fmt.Errorf("tasks: %s: ref %q is twice", f.Path, t.GetRef())
		case len(cmds) == 0 || cmds[0] != t.GetCommandId() || t.GetCommandId() == "":
			return nil, fmt.Errorf("tasks: %s: command_ids must start with the command_id", f.Path)
		}
		e := &entry{task: t, commands: cmds}
		byNum[n] = e
		for _, c := range cmds {
			if s.byCmd[c] != nil {
				return nil, fmt.Errorf("tasks: %s: command ID %q is used twice", f.Path, c)
			}
			s.byCmd[c] = e
		}
	}
	// The numbers have no gaps: a task is never deleted.
	for n := uint64(1); n <= uint64(len(byNum)); n++ {
		if byNum[n] == nil {
			return nil, fmt.Errorf("tasks: %s is missing", filePath(n))
		}
		s.all = append(s.all, byNum[n])
	}
	return s, nil
}

// Count returns the number of tasks.
func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.all)
}

func filePath(n uint64) string { return fmt.Sprintf("%s/%06d.yaml", Dir, n) }

func localRef(n uint64) string { return localScheme + strconv.FormatUint(n, 10) }

// localNumber returns the number of a local ref such as "local:42".
func localNumber(ref string) (uint64, bool) {
	rest, ok := strings.CutPrefix(ref, localScheme)
	if !ok || rest == "" || rest[0] == '0' || rest[0] == '+' {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 64)
	return n, err == nil
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// caller returns the participant of the access token and its author kind. A stage link has no
// participant.
func caller(ctx context.Context) (string, taskv1.AuthorKind, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	if !ok {
		return "", 0, connect.NewError(connect.CodeUnauthenticated, "no access token")
	}
	if c.ParticipantID == "" {
		return "", 0, connect.NewError(connect.CodePermissionDenied, "a stage link cannot use the tasks")
	}
	switch c.Kind {
	case tokencheck.KindHuman:
		return c.ParticipantID, taskv1.AuthorKind_AUTHOR_KIND_HUMAN, nil
	case tokencheck.KindAgent:
		return c.ParticipantID, taskv1.AuthorKind_AUTHOR_KIND_AGENT, nil
	}
	return c.ParticipantID, taskv1.AuthorKind_AUTHOR_KIND_UNSPECIFIED, nil
}

// checkCaller fails unless participantID is the participant of the access token.
func checkCaller(ctx context.Context, participantID, field string) (taskv1.AuthorKind, error) {
	pid, k, err := caller(ctx)
	if err != nil {
		return 0, err
	}
	if participantID != pid {
		return 0, connect.NewError(connect.CodePermissionDenied, field+" must be the participant of the access token")
	}
	return k, nil
}

func validStatus(s taskv1.TaskStatus) bool {
	_, ok := taskv1.TaskStatus_name[int32(s)]
	return ok && s != taskv1.TaskStatus_TASK_STATUS_UNSPECIFIED
}

// CreateTask creates a local draft.
func (s *Service) CreateTask(ctx context.Context, req *taskv1.CreateTaskRequest) (*taskv1.CreateTaskResponse, error) {
	status := req.GetStatus()
	if status == taskv1.TaskStatus_TASK_STATUS_UNSPECIFIED {
		status = taskv1.TaskStatus_TASK_STATUS_OPEN
	}
	switch {
	case req.GetCommandId() == "":
		return nil, invalid("command_id is required")
	case blank(req.GetTitle()):
		return nil, invalid("title is required")
	case !validStatus(status):
		return nil, invalid("status is not known")
	case req.GetAuthorParticipantId() == "":
		return nil, invalid("author_participant_id is required")
	}
	kind, err := checkCaller(ctx, req.GetAuthorParticipantId(), "author_participant_id")
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if prev := s.byCmd[req.GetCommandId()]; prev != nil {
		t := prev.task
		// After a change, the stored task no longer shows what was created; then only the
		// author is compared.
		other := prev.commands[0] != req.GetCommandId() || t.GetAuthorParticipantId() != req.GetAuthorParticipantId() ||
			(t.GetRevision() == 1 && (t.GetTitle() != req.GetTitle() || t.GetDescription() != req.GetDescription() ||
				t.GetAssigneeParticipantId() != req.GetAssigneeParticipantId() || t.GetSessionId() != req.GetSessionId() ||
				t.GetStatus() != status))
		if other {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another task or change")
		}
		return &taskv1.CreateTaskResponse{Task: t, Duplicate: true}, nil
	}
	n := uint64(len(s.all)) + 1
	now := timestamppb.Now()
	t := &taskv1.Task{Ref: localRef(n), Source: taskv1.TaskSource_TASK_SOURCE_LOCAL, Title: req.GetTitle(),
		Description: req.GetDescription(), AssigneeParticipantId: req.GetAssigneeParticipantId(), Status: status,
		SessionId: req.GetSessionId(), AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: kind,
		CreateTime: now, UpdatedByParticipantId: req.GetAuthorParticipantId(), UpdateTime: now, Revision: 1,
		CommandId: req.GetCommandId()}
	e := &entry{task: t, commands: []string{req.GetCommandId()}}
	if err := s.write(ctx, n, e, fmt.Sprintf("Task %s: %s", t.GetRef(), summary(t.GetTitle()))); err != nil {
		return nil, err
	}
	s.all = append(s.all, e)
	s.byCmd[req.GetCommandId()] = e
	return &taskv1.CreateTaskResponse{Task: t}, nil
}

// write stores the task of e as the file of the local number n, in one commit.
func (s *Service) write(ctx context.Context, n uint64, e *entry, message string) error {
	data, err := encode(e.task, e.commands)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "encode the task: "+err.Error())
	}
	// A cancelled commit could leave the repository locked, so the commit is not cancelled with
	// the call.
	if _, err := s.arch.Commit(context.WithoutCancel(ctx), storage.Change{Message: message,
		Files: []storage.File{{Path: filePath(n), Data: data}}}); err != nil {
		return connect.NewError(connect.CodeInternal, "store the task in the archive: "+err.Error())
	}
	return nil
}

// summary returns the first line of a text, short enough for a commit message.
func summary(text string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if rs := []rune(t); len(rs) > 60 {
		t = string(rs[:60]) + "…"
	}
	return t
}

// find returns the stored task of a ref. The caller holds s.mu.
func (s *Service) find(ref string) (*entry, uint64, error) {
	if ref == "" {
		return nil, 0, invalid("ref is required")
	}
	n, ok := localNumber(ref)
	if !ok || n > uint64(len(s.all)) {
		return nil, 0, connect.NewError(connect.CodeNotFound, "no task "+ref)
	}
	return s.all[n-1], n, nil
}

// GetTask returns one task.
func (s *Service) GetTask(ctx context.Context, req *taskv1.GetTaskRequest) (*taskv1.GetTaskResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, _, err := s.find(req.GetRef())
	if err != nil {
		return nil, err
	}
	return &taskv1.GetTaskResponse{Task: e.task}, nil
}

// ListTasks returns the tasks that match the filters, oldest first.
func (s *Service) ListTasks(ctx context.Context, req *taskv1.ListTasksRequest) (*taskv1.ListTasksResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	statuses := map[taskv1.TaskStatus]bool{}
	for _, st := range req.GetStatuses() {
		if !validStatus(st) {
			return nil, invalid("statuses has a status that is not known")
		}
		statuses[st] = true
	}
	var after uint64
	if tok := req.GetPageToken(); tok != "" {
		n, err := strconv.ParseUint(tok, 10, 64)
		if err != nil || n == 0 {
			return nil, invalid("page_token is not valid")
		}
		after = n
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	s.mu.Lock()
	defer s.mu.Unlock()
	res := &taskv1.ListTasksResponse{}
	for i := after; i < uint64(len(s.all)); i++ {
		t := s.all[i].task
		if (len(statuses) > 0 && !statuses[t.GetStatus()]) ||
			(req.GetAssigneeParticipantId() != "" && t.GetAssigneeParticipantId() != req.GetAssigneeParticipantId()) ||
			(req.GetSessionId() != "" && t.GetSessionId() != req.GetSessionId()) {
			continue
		}
		if len(res.Tasks) == limit {
			// One more task matches: the page ends after the last task in it.
			res.NextPageToken = strconv.FormatUint(i, 10)
			break
		}
		res.Tasks = append(res.Tasks, t)
	}
	return res, nil
}

// UpdateTask changes the fields of a task that the request sets.
func (s *Service) UpdateTask(ctx context.Context, req *taskv1.UpdateTaskRequest) (*taskv1.UpdateTaskResponse, error) {
	switch {
	case req.GetCommandId() == "":
		return nil, invalid("command_id is required")
	case req.GetRef() == "":
		return nil, invalid("ref is required")
	case req.GetParticipantId() == "":
		return nil, invalid("participant_id is required")
	case req.Title == nil && req.Description == nil && req.AssigneeParticipantId == nil && req.Status == nil && req.SessionId == nil:
		return nil, invalid("the request changes no field")
	case req.Title != nil && blank(req.GetTitle()):
		return nil, invalid("title must not be blank")
	case req.Status != nil && !validStatus(req.GetStatus()):
		return nil, invalid("status is not known")
	}
	if _, err := checkCaller(ctx, req.GetParticipantId(), "participant_id"); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	e, n, err := s.find(req.GetRef())
	if err != nil {
		return nil, err
	}
	if prev := s.byCmd[req.GetCommandId()]; prev != nil {
		if prev != e || prev.commands[0] == req.GetCommandId() {
			return nil, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another task or change")
		}
		return &taskv1.UpdateTaskResponse{Task: e.task, Duplicate: true}, nil
	}
	t := proto.Clone(e.task).(*taskv1.Task)
	var changed []string
	set := func(name string, dst *string, v *string) {
		if v != nil && *dst != *v {
			*dst = *v
			changed = append(changed, name)
		}
	}
	set("title", &t.Title, req.Title)
	set("description", &t.Description, req.Description)
	set("assignee", &t.AssigneeParticipantId, req.AssigneeParticipantId)
	set("session", &t.SessionId, req.SessionId)
	if req.Status != nil && t.Status != req.GetStatus() {
		t.Status = req.GetStatus()
		changed = append(changed, "status")
	}
	if len(changed) == 0 {
		// Nothing to store: the command ID is not kept, and a repeat is answered the same way.
		return &taskv1.UpdateTaskResponse{Task: e.task}, nil
	}
	t.Revision++
	t.UpdatedByParticipantId = req.GetParticipantId()
	t.UpdateTime = timestamppb.Now()
	next := &entry{task: t, commands: append(append([]string{}, e.commands...), req.GetCommandId())}
	if err := s.write(ctx, n, next, fmt.Sprintf("Task %s, revision %d: %s", t.GetRef(), t.GetRevision(),
		strings.Join(changed, ", "))); err != nil {
		return nil, err
	}
	// The entry keeps its identity, so byCmd stays valid for the older command IDs.
	e.task, e.commands = next.task, next.commands
	s.byCmd[req.GetCommandId()] = e
	return &taskv1.UpdateTaskResponse{Task: t}, nil
}
