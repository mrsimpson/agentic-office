package tasks

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
	"github.com/mrsimpson/agentic-office/hub/tasks/check"
)

const (
	// DefaultLimit is the number of tasks ListTasks returns when the request sets no limit.
	DefaultLimit = 100
	// MaxLimit is the largest number of tasks ListTasks returns. A larger limit is reduced.
	MaxLimit = 1000
	// Dir is the directory of the tasks in the archive.
	Dir = check.Dir
)

// maxCreateAttempts is how often a create takes the next number again because a file of that
// number was already in the archive. Every attempt means that a push put a file there meanwhile.
const maxCreateAttempts = 50

// maxWriteAttempts is how often an update reads the file again after a conflict. Every attempt
// means that a push changed the same file meanwhile.
const maxWriteAttempts = 20

// entry is a stored task with the command IDs that changed it, oldest first.
type entry struct {
	num      uint64
	task     *taskv1.Task
	commands []string
	version  string // the version of the file that the entry was read from; empty after a write
}

// Service implements the task interface (if-task-api).
type Service struct {
	arch storage.Archive
	logf func(string, ...any)

	watcher watch.Watcher
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}

	mu    sync.Mutex // one write at a time; guards everything below
	byNum map[uint64]*entry
	byCmd map[string]*entry
	bad   map[string]string // path to the version of a file that was ignored and logged
	high  uint64            // the highest number of any file ever seen, valid or not
	all   []*entry          // ordered by number; stale while dirty
	dirty bool
}

var _ taskv1connect.TaskServiceHandler = (*Service)(nil)

// Open reads all tasks from the archive and returns the service that stores tasks there. The
// service follows the archive through w (if-archive-watch) until Close: it reloads the task files
// that a push changed or added. logf logs the files it ignores; nil means log.Printf.
func Open(ctx context.Context, arch storage.Archive, w watch.Watcher, logf func(string, ...any)) (*Service, error) {
	if logf == nil {
		logf = log.Printf
	}
	// Watch first, then read: a change in between is reported and read again.
	sub, err := w.Watch(Dir)
	if err != nil {
		return nil, fmt.Errorf("tasks: watch the archive: %w", err)
	}
	s := &Service{arch: arch, logf: logf, watcher: w, done: make(chan struct{}),
		byNum: map[uint64]*entry{}, byCmd: map[string]*entry{}, bad: map[string]string{}}
	if err := s.loadAll(ctx, true); err != nil {
		sub.Close()
		return nil, err
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.run(sub)
	return s, nil
}

// Close stops following the archive. The service still answers calls.
func (s *Service) Close() {
	s.cancel()
	<-s.done
}

// run reloads the files that the archive reports until Close. A subscription that was dropped
// (the part was too slow) is replaced, and then every file is read again, because changes may
// have been missed.
func (s *Service) run(sub watch.Subscription) {
	defer close(s.done)
	for {
		s.pump(sub)
		sub.Close()
		if s.ctx.Err() != nil {
			return
		}
		s.logf("tasks: the archive dropped the watcher (%v); watching again and reading all tasks", sub.Err())
		var err error
		if sub, err = s.watcher.Watch(Dir); err != nil {
			s.logf("tasks: cannot watch the archive again, pushed files are not seen until the hub restarts: %v", err)
			return
		}
		if err := s.loadAll(s.ctx, false); err != nil {
			s.logf("%v", err)
		}
	}
}

func (s *Service) pump(sub watch.Subscription) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case changes, ok := <-sub.Changes():
			if !ok {
				return
			}
			if err := s.reload(s.ctx, changes); err != nil {
				s.logf("%v", err)
			}
		}
	}
}

// numberOf returns the number in the name of a task file such as "tasks/000042.yaml".
func numberOf(p string) (uint64, bool) {
	n, err := strconv.ParseUint(strings.TrimSuffix(path.Base(p), ".yaml"), 10, 64)
	return n, err == nil
}

// load reads one file into the service. It returns an error and changes nothing if the file is
// not valid. The caller holds s.mu. A file that is not valid still counts for the next number, so
// the part never takes its number.
func (s *Service) load(f storage.File) error {
	if path.Ext(f.Path) != ".yaml" {
		return nil
	}
	if n, ok := numberOf(f.Path); ok {
		s.high = max(s.high, n)
		if e := s.byNum[n]; e != nil && e.version == f.Version {
			return nil
		}
	}
	// The check of the part: the same one that decides about a push.
	if reasons := check.Validate(check.SchemaTask, f.Path, f.Data); len(reasons) > 0 {
		return fmt.Errorf("%s: %s", f.Path, strings.Join(reasons, "; "))
	}
	t, cmds, err := decode(f.Data)
	if err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	n, _ := check.LocalNumber(t.GetRef())
	for _, c := range cmds {
		if other := s.byCmd[c]; other != nil && other.num != n {
			return fmt.Errorf("%s: command ID %q is used by %s", f.Path, c, other.task.GetRef())
		}
	}
	s.forget(n)
	e := &entry{num: n, task: t, commands: cmds, version: f.Version}
	s.byNum[n] = e
	for _, c := range cmds {
		s.byCmd[c] = e
	}
	delete(s.bad, f.Path)
	s.dirty = true
	return nil
}

// loadLenient is load for a file that a push brought: a file that is not valid is logged once for
// its version and ignored. The task that the path had before stays.
func (s *Service) loadLenient(f storage.File) {
	if err := s.load(f); err != nil && s.bad[f.Path] != f.Version {
		s.bad[f.Path] = f.Version
		s.logf("tasks: ignore the pushed file %v", err)
	}
}

// forget removes the task with number n. The caller holds s.mu.
func (s *Service) forget(n uint64) {
	if e := s.byNum[n]; e != nil {
		for _, c := range e.commands {
			if s.byCmd[c] == e {
				delete(s.byCmd, c)
			}
		}
		delete(s.byNum, n)
		s.dirty = true
	}
	delete(s.bad, check.FilePath(n))
}

// loadAll reads every file of the directory. With strict, the first file that is not valid fails
// it (the start of the hub). Otherwise such a file is logged and ignored, and a task whose file is
// gone is removed.
func (s *Service) loadAll(ctx context.Context, strict bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.arch.Read(ctx, Dir)
	if err != nil {
		return fmt.Errorf("tasks: read %s: %w", Dir, err)
	}
	// The next number never goes below a number that ever lay in the directory, also not below
	// the number of a file that a push removed.
	names, err := s.arch.Names(ctx, Dir)
	if err != nil {
		return fmt.Errorf("tasks: list the names in %s: %w", Dir, err)
	}
	for _, p := range names {
		if n, ok := numberOf(p); ok {
			s.high = max(s.high, n)
		}
	}
	present := map[uint64]bool{}
	for _, f := range files {
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		if n, ok := numberOf(f.Path); ok {
			present[n] = true
		}
		if strict {
			if err := s.load(f); err != nil {
				return fmt.Errorf("tasks: %w", err)
			}
		} else {
			s.loadLenient(f)
		}
	}
	for n := range s.byNum {
		if !present[n] {
			s.forget(n)
		}
	}
	return nil
}

// reloadPaths reads the files at the paths again from the current commit: a file that is there is
// loaded, a file that is gone is removed. The caller holds s.mu.
func (s *Service) reloadPaths(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if path.Ext(p) != ".yaml" {
			continue
		}
		files, err := s.arch.Read(ctx, p)
		if err != nil {
			return fmt.Errorf("tasks: read %s: %w", p, err)
		}
		if len(files) == 0 {
			if n, ok := numberOf(p); ok {
				s.forget(n)
			}
			continue
		}
		for _, f := range files {
			s.loadLenient(f)
		}
	}
	return nil
}

// reload reads the task files that the changes name.
func (s *Service) reload(ctx context.Context, changes []watch.Change) error {
	var paths []string
	for _, ch := range changes {
		if strings.HasPrefix(ch.Path, Dir+"/") {
			paths = append(paths, ch.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadPaths(ctx, paths)
}

// sorted returns the entries ordered by number. The caller holds s.mu.
func (s *Service) sorted() []*entry {
	if s.dirty {
		s.all = s.all[:0]
		for _, e := range s.byNum {
			s.all = append(s.all, e)
		}
		slices.SortFunc(s.all, func(a, b *entry) int { return cmp.Compare(a.num, b.num) })
		s.dirty = false
	}
	return s.all
}

// Count returns the number of tasks.
func (s *Service) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byNum)
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
	// The file is created with "must not exist yet". If a push put a file with this number there
	// first, the part reads the file, takes the next free number and tries again.
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
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
		n := s.high + 1
		now := timestamppb.Now()
		t := &taskv1.Task{Ref: check.LocalRef(n), Source: taskv1.TaskSource_TASK_SOURCE_LOCAL, Title: req.GetTitle(),
			Description: req.GetDescription(), AssigneeParticipantId: req.GetAssigneeParticipantId(), Status: status,
			SessionId: req.GetSessionId(), AuthorParticipantId: req.GetAuthorParticipantId(), AuthorKind: kind,
			CreateTime: now, UpdatedByParticipantId: req.GetAuthorParticipantId(), UpdateTime: now, Revision: 1,
			CommandId: req.GetCommandId()}
		e := &entry{num: n, task: t, commands: []string{req.GetCommandId()}}
		err := s.write(ctx, n, e, fmt.Sprintf("Task %s: %s", t.GetRef(), summary(t.GetTitle())), storage.NoFile)
		if storage.IsConflict(err) {
			// A push came first. Read what is there; its number is taken from now on.
			if err := s.reloadPaths(context.WithoutCancel(ctx), []string{check.FilePath(n)}); err != nil {
				return nil, connect.NewError(connect.CodeInternal, err.Error())
			}
			s.high = max(s.high, n)
			continue
		}
		if err != nil {
			return nil, err
		}
		s.forget(n)
		s.high = n
		s.byNum[n] = e
		s.byCmd[req.GetCommandId()] = e
		s.dirty = true
		return &taskv1.CreateTaskResponse{Task: t}, nil
	}
	return nil, connect.NewError(connect.CodeInternal, "store the task in the archive: too many files were pushed meanwhile; repeat the command")
}

// write stores the task of e as the file of the local number n, in one commit. base is the
// Base of the file (see storage.File). A conflict is returned as it is.
func (s *Service) write(ctx context.Context, n uint64, e *entry, message, base string) error {
	data, err := encode(e.task, e.commands)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "encode the task: "+err.Error())
	}
	if reasons := check.Validate(check.SchemaTask, check.FilePath(n), data); len(reasons) > 0 {
		return connect.NewError(connect.CodeInternal, "the task is not valid: "+strings.Join(reasons, "; "))
	}
	// A cancelled commit could leave the repository locked, so the commit is not cancelled with
	// the call.
	if _, err := s.arch.Commit(context.WithoutCancel(ctx), storage.Change{Message: message,
		Files: []storage.File{{Path: check.FilePath(n), Data: data, Base: base}}}); err != nil {
		if storage.IsConflict(err) {
			return err
		}
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
	n, ok := check.LocalNumber(ref)
	if !ok || s.byNum[n] == nil {
		return nil, 0, connect.NewError(connect.CodeNotFound, "no task "+ref)
	}
	return s.byNum[n], n, nil
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
	for _, e := range s.sorted() {
		if e.num <= after {
			continue
		}
		t := e.task
		if (len(statuses) > 0 && !statuses[t.GetStatus()]) ||
			(req.GetAssigneeParticipantId() != "" && t.GetAssigneeParticipantId() != req.GetAssigneeParticipantId()) ||
			(req.GetSessionId() != "" && t.GetSessionId() != req.GetSessionId()) {
			continue
		}
		if len(res.Tasks) == limit {
			// One more task matches: the page ends after the last task in it.
			last, _ := check.LocalNumber(res.Tasks[limit-1].GetRef())
			res.NextPageToken = strconv.FormatUint(last, 10)
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
	n, ok := check.LocalNumber(req.GetRef())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, "no task "+req.GetRef())
	}
	// The file is written with the version that was read. If a push changed it meanwhile, the
	// part reads it again, applies the request to the new state and tries again.
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		files, err := s.arch.Read(ctx, check.FilePath(n))
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "read the task from the archive: "+err.Error())
		}
		if len(files) == 0 {
			s.forget(n)
			return nil, connect.NewError(connect.CodeNotFound, "no task "+req.GetRef())
		}
		if err := s.load(files[0]); err != nil {
			return nil, connect.NewError(connect.CodeInternal, "the task file is not valid: "+err.Error())
		}
		e, _, err := s.find(req.GetRef())
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
		err = s.write(ctx, n, next, fmt.Sprintf("Task %s, revision %d: %s", t.GetRef(), t.GetRevision(),
			strings.Join(changed, ", ")), files[0].Version)
		if storage.IsConflict(err) {
			continue // a push came first: read the file again
		}
		if err != nil {
			return nil, err
		}
		// The entry keeps its identity, so byCmd stays valid for the older command IDs.
		e.task, e.commands, e.version = next.task, next.commands, ""
		s.byCmd[req.GetCommandId()] = e
		return &taskv1.UpdateTaskResponse{Task: t}, nil
	}
	return nil, connect.NewError(connect.CodeAborted, "the task was changed by pushes again and again; nothing was written, repeat the command")
}
