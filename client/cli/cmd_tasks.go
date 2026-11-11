package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// Tasks are local drafts in the hub for now (#40 syncs them with the team's task tool). A ref
// such as "local:42" is printed and passed back as it is.

// taskErr turns bad input into a usage error (exit code 2).
func taskErr(err error) error {
	if errors.Is(err, coreapi.ErrBadInput) {
		return usagef("%v", err)
	}
	return err
}

// setFlags returns the names of the flags given on the command line.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// taskText reads a description from --description or --description-file.
func (e *env) taskText(text, file string, set map[string]bool) (string, bool, error) {
	switch {
	case set["description"] && set["description-file"]:
		return "", false, usagef("give --description or --description-file, not both")
	case set["description-file"]:
		t, err := e.readText(nil, file)
		return t, true, err
	}
	return text, set["description"], nil
}

func cmdTaskCreate(e *env, fs *flag.FlagSet) func([]string) error {
	desc := fs.String("description", "", "description of the task")
	descFile := fs.String("description-file", "", "read the description from this file (- for stdin)")
	assignee := fs.String("assignee", "", "participant who does the task")
	status := fs.String("status", "", "status: "+strings.Join(coreapi.TaskStatusNames, ", ")+" (default open)")
	return func(args []string) error {
		title := strings.Join(args, " ")
		if strings.TrimSpace(title) == "" {
			return usagef("usage: office task create <title> [--description D | --description-file F] [--assignee P] [--status S]")
		}
		d, _, err := e.taskText(*desc, *descFile, setFlags(fs))
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		// --session links the task to a session.
		res, err := c.CreateTask(e.ctx, coreapi.TaskDraft{Title: title, Description: d, Assignee: *assignee, Status: *status, SessionID: e.session})
		if err != nil {
			return taskErr(err)
		}
		if e.json {
			e.printJSON(map[string]any{"task": coreapi.MessageJSON(res.GetTask()), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "task %s\n", res.GetTask().GetRef())
		}
		return nil
	}
}

func cmdTaskGet(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office task get <ref>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		t, err := c.GetTask(e.ctx, args[0])
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"task": coreapi.MessageJSON(t)})
			return nil
		}
		fmt.Fprintf(e.stdout, "%s [%s] %s\n", t.GetRef(), coreapi.TaskStatusName(t.GetStatus()), oneLine(t.GetTitle()))
		for _, kv := range [][2]string{{"assignee", t.GetAssigneeParticipantId()}, {"session", t.GetSessionId()},
			{"author", t.GetAuthorParticipantId()}, {"revision", fmt.Sprint(t.GetRevision())}} {
			if kv[1] != "" {
				fmt.Fprintf(e.stdout, "%s: %s\n", kv[0], kv[1])
			}
		}
		if d := strings.TrimRight(t.GetDescription(), "\n"); d != "" {
			fmt.Fprintf(e.stdout, "\n%s\n", d)
		}
		return nil
	}
}

func cmdTaskList(e *env, fs *flag.FlagSet) func([]string) error {
	status := fs.String("status", "", "only tasks with one of these statuses, comma-separated: "+strings.Join(coreapi.TaskStatusNames, ", "))
	assignee := fs.String("assignee", "", "only tasks of this participant")
	page := fs.String("page-token", "", "read the page after the one that printed this token")
	limit := fs.Uint("limit", 0, "at most this many tasks (0: the hub's default)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		f := coreapi.TaskFilter{Assignee: *assignee, SessionID: e.session, PageToken: *page, Limit: uint32(*limit)}
		for _, s := range strings.Split(*status, ",") {
			if s = strings.TrimSpace(s); s != "" {
				f.Statuses = append(f.Statuses, s)
			}
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ts, next, err := c.ListTasks(e.ctx, f)
		if err != nil {
			return taskErr(err)
		}
		if e.json {
			out := make([]any, len(ts))
			for i, t := range ts {
				out[i] = coreapi.MessageJSON(t)
			}
			e.printJSON(map[string]any{"tasks": out, "nextPageToken": next})
			return nil
		}
		for _, t := range ts {
			who := ""
			if a := t.GetAssigneeParticipantId(); a != "" {
				who = " @" + a
			}
			fmt.Fprintf(e.stdout, "%s [%s]%s %s\n", t.GetRef(), coreapi.TaskStatusName(t.GetStatus()), who, oneLine(t.GetTitle()))
		}
		if next != "" {
			fmt.Fprintf(e.stderr, "more tasks: --page-token %s\n", next)
		}
		return nil
	}
}

func cmdTaskUpdate(e *env, fs *flag.FlagSet) func([]string) error {
	title := fs.String("title", "", "new title")
	desc := fs.String("description", "", "new description")
	descFile := fs.String("description-file", "", "read the new description from this file (- for stdin)")
	assignee := fs.String("assignee", "", "new assignee; empty removes the assignee")
	status := fs.String("status", "", "new status: "+strings.Join(coreapi.TaskStatusNames, ", "))
	return func(args []string) error {
		const use = "usage: office task update <ref> [--title T] [--description D | --description-file F] [--assignee P] [--status S] [--session S]"
		if len(args) != 1 {
			return usagef(use)
		}
		set := setFlags(fs)
		var ch coreapi.TaskChange
		if set["title"] {
			ch.Title = title
		}
		d, ok, err := e.taskText(*desc, *descFile, set)
		if err != nil {
			return err
		}
		if ok {
			ch.Description = &d
		}
		if set["assignee"] {
			ch.Assignee = assignee
		}
		if set["status"] {
			ch.Status = status
		}
		// Only an explicit --session links the task to a session; --session "" removes the link.
		if set["session"] {
			ch.SessionID = &e.session
		}
		if ch == (coreapi.TaskChange{}) {
			return usagef("%s: nothing to change", use)
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.UpdateTask(e.ctx, args[0], ch)
		if err != nil {
			return taskErr(err)
		}
		if e.json {
			e.printJSON(map[string]any{"task": coreapi.MessageJSON(res.GetTask()), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "task %s revision %d\n", res.GetTask().GetRef(), res.GetTask().GetRevision())
		}
		return nil
	}
}
