package coreapi

import (
	"fmt"
	"strings"

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
