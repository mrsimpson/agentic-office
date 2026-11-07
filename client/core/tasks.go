package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrsimpson/agentic-office/client/core/ids"
	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
)

// Tasks are local drafts in the hub for now; a later sync replaces them with the tasks of the
// team's task tool (#40). A task is named by its ref, for example "local:42". Treat a ref as
// opaque: never build or parse one.

// TaskStatusNames are the names of the task statuses in the CLI and the MCP server, in order.
var TaskStatusNames = []string{"open", "in_progress", "done", "cancelled"}

// ParseTaskStatus reads a status name: open, in_progress (or in-progress), done or cancelled,
// in any case, or the name of the contract (TASK_STATUS_DONE).
func ParseTaskStatus(s string) (taskv1.TaskStatus, error) {
	n := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
	if v, ok := taskv1.TaskStatus_value["TASK_STATUS_"+n]; ok && v != 0 {
		return taskv1.TaskStatus(v), nil
	}
	if v, ok := taskv1.TaskStatus_value[n]; ok && v != 0 {
		return taskv1.TaskStatus(v), nil
	}
	return 0, fmt.Errorf("%w: unknown task status %q (one of %s)", ErrBadInput, s, strings.Join(TaskStatusNames, ", "))
}

// TaskStatusName returns the short name of a status, for example "in_progress".
func TaskStatusName(s taskv1.TaskStatus) string {
	return strings.ToLower(strings.TrimPrefix(s.String(), "TASK_STATUS_"))
}

// TaskDraft is a new task. Status and the other fields except Title may be empty.
type TaskDraft struct {
	Title, Description, Assignee, Status, SessionID string
}

// TaskChange lists the fields of a task to change. A nil field stays as it is; an empty Assignee
// or SessionID removes it.
type TaskChange struct {
	Title, Description, Assignee, Status, SessionID *string
}

// TaskFilter selects tasks. Empty fields match every task. PageToken is the NextPageToken of the
// page before; Limit 0 is the default of the hub.
type TaskFilter struct {
	Statuses            []string
	Assignee, SessionID string
	PageToken           string
	Limit               uint32
}

// CreateTask creates a task as a local draft. The author is the caller.
func (c *Client) CreateTask(ctx context.Context, d TaskDraft) (*taskv1.CreateTaskResponse, error) {
	if strings.TrimSpace(d.Title) == "" {
		return nil, fmt.Errorf("%w: a task needs a title", ErrBadInput)
	}
	var st taskv1.TaskStatus
	if d.Status != "" {
		var err error
		if st, err = ParseTaskStatus(d.Status); err != nil {
			return nil, err
		}
	}
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &taskv1.CreateTaskRequest{CommandId: ids.NewCommandID(), Title: d.Title, Description: d.Description,
		AssigneeParticipantId: d.Assignee, Status: st, SessionId: d.SessionID, AuthorParticipantId: pid}
	var res *taskv1.CreateTaskResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Task.CreateTask(ctx, req)
		return err
	})
	return res, err
}

// GetTask returns the task of a ref.
func (c *Client) GetTask(ctx context.Context, ref string) (*taskv1.Task, error) {
	var res *taskv1.GetTaskResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Task.GetTask(ctx, &taskv1.GetTaskRequest{Ref: ref})
		return err
	})
	return res.GetTask(), err
}

// ListTasks returns one page of the tasks that match f, oldest first, and the token of the next
// page ("" if there is none).
func (c *Client) ListTasks(ctx context.Context, f TaskFilter) ([]*taskv1.Task, string, error) {
	req := &taskv1.ListTasksRequest{AssigneeParticipantId: f.Assignee, SessionId: f.SessionID, PageToken: f.PageToken, Limit: f.Limit}
	for _, s := range f.Statuses {
		st, err := ParseTaskStatus(s)
		if err != nil {
			return nil, "", err
		}
		req.Statuses = append(req.Statuses, st)
	}
	var res *taskv1.ListTasksResponse
	err := Retry(ctx, func() (err error) {
		res, err = c.Task.ListTasks(ctx, req)
		return err
	})
	return res.GetTasks(), res.GetNextPageToken(), err
}

// UpdateTask changes the fields of a task that ch sets. The caller makes the change.
func (c *Client) UpdateTask(ctx context.Context, ref string, ch TaskChange) (*taskv1.UpdateTaskResponse, error) {
	if ch.Title == nil && ch.Description == nil && ch.Assignee == nil && ch.Status == nil && ch.SessionID == nil {
		return nil, fmt.Errorf("%w: nothing to change: give a title, description, assignee, status or session", ErrBadInput)
	}
	req := &taskv1.UpdateTaskRequest{CommandId: ids.NewCommandID(), Ref: ref, Title: ch.Title, Description: ch.Description,
		AssigneeParticipantId: ch.Assignee, SessionId: ch.SessionID}
	if ch.Status != nil {
		st, err := ParseTaskStatus(*ch.Status)
		if err != nil {
			return nil, err
		}
		req.Status = &st
	}
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req.ParticipantId = pid
	var res *taskv1.UpdateTaskResponse
	err = Retry(ctx, func() (err error) {
		res, err = c.Task.UpdateTask(ctx, req)
		return err
	})
	return res, err
}
