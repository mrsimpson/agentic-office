package core

import (
	"context"
	"fmt"
	"strings"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	taskv1 "github.com/mrsimpson/agentic-office/hub/tasks/api"
)

// CreateTask creates a task as a local draft. The author is the caller.
func (c *hubClient) CreateTask(ctx context.Context, d coreapi.TaskDraft) (*taskv1.CreateTaskResponse, error) {
	if strings.TrimSpace(d.Title) == "" {
		return nil, fmt.Errorf("%w: a task needs a title", coreapi.ErrBadInput)
	}
	var st taskv1.TaskStatus
	if d.Status != "" {
		var err error
		if st, err = coreapi.ParseTaskStatus(d.Status); err != nil {
			return nil, err
		}
	}
	pid, err := c.ParticipantID(ctx)
	if err != nil {
		return nil, err
	}
	req := &taskv1.CreateTaskRequest{CommandId: coreapi.NewCommandID(), Title: d.Title, Description: d.Description,
		AssigneeParticipantId: d.Assignee, Status: st, SessionId: d.SessionID, AuthorParticipantId: pid}
	var res *taskv1.CreateTaskResponse
	err = retry(ctx, func() (err error) {
		res, err = c.task.CreateTask(ctx, req)
		return err
	})
	return res, err
}

// GetTask returns the task of a ref.
func (c *hubClient) GetTask(ctx context.Context, ref string) (*taskv1.Task, error) {
	var res *taskv1.GetTaskResponse
	err := retry(ctx, func() (err error) {
		res, err = c.task.GetTask(ctx, &taskv1.GetTaskRequest{Ref: ref})
		return err
	})
	return res.GetTask(), err
}

// ListTasks returns one page of the tasks that match f, oldest first, and the token of the next
// page ("" if there is none).
func (c *hubClient) ListTasks(ctx context.Context, f coreapi.TaskFilter) ([]*taskv1.Task, string, error) {
	req := &taskv1.ListTasksRequest{AssigneeParticipantId: f.Assignee, SessionId: f.SessionID, PageToken: f.PageToken, Limit: f.Limit}
	for _, s := range f.Statuses {
		st, err := coreapi.ParseTaskStatus(s)
		if err != nil {
			return nil, "", err
		}
		req.Statuses = append(req.Statuses, st)
	}
	var res *taskv1.ListTasksResponse
	err := retry(ctx, func() (err error) {
		res, err = c.task.ListTasks(ctx, req)
		return err
	})
	return res.GetTasks(), res.GetNextPageToken(), err
}

// UpdateTask changes the fields of a task that ch sets. The caller makes the change.
func (c *hubClient) UpdateTask(ctx context.Context, ref string, ch coreapi.TaskChange) (*taskv1.UpdateTaskResponse, error) {
	if ch.Title == nil && ch.Description == nil && ch.Assignee == nil && ch.Status == nil && ch.SessionID == nil {
		return nil, fmt.Errorf("%w: nothing to change: give a title, description, assignee, status or session", coreapi.ErrBadInput)
	}
	req := &taskv1.UpdateTaskRequest{CommandId: coreapi.NewCommandID(), Ref: ref, Title: ch.Title, Description: ch.Description,
		AssigneeParticipantId: ch.Assignee, SessionId: ch.SessionID}
	if ch.Status != nil {
		st, err := coreapi.ParseTaskStatus(*ch.Status)
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
	err = retry(ctx, func() (err error) {
		res, err = c.task.UpdateTask(ctx, req)
		return err
	})
	return res, err
}
