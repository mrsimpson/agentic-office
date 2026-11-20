package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
)

func cmdSay(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		text := strings.Join(args, " ")
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office say <text>")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.Say(e.ctx, sid, text)
		})
	}
}

func parseEventID(s string) (uint32, error) {
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0, usagef("%q is not an event ID", s)
	}
	return uint32(n), nil
}

func cmdOffTheRecord(e *env, fs *flag.FlagSet) func([]string) error {
	last := fs.Bool("last", false, "mark the last own sentence off the record, if it is not yet (read from the local log)")
	return func(args []string) error {
		if *last == (len(args) == 1) || len(args) > 1 {
			return usagef("usage: office utterance off-the-record <event-id> | --last")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			if *last {
				return c.MarkOffTheRecordLast(e.ctx, sid)
			}
			id, err := parseEventID(args[0])
			if err != nil {
				return nil, err
			}
			return c.MarkOffTheRecord(e.ctx, sid, id)
		})
	}
}

func cmdTurnEnd(e *env, fs *flag.FlagSet) func([]string) error {
	who := fs.String("participant", "", "participant whose turn ended (default: you)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.TurnEnd(e.ctx, sid, *who)
		})
	}
}

func cmdSilenceLong(e *env, fs *flag.FlagSet) func([]string) error {
	d := fs.Duration("duration", 0, "length of the silence, for example 10s")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *d <= 0 {
			return usagef("--duration is required")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.SilenceLong(e.ctx, sid, *d)
		})
	}
}

func cmdAddressed(e *env, fs *flag.FlagSet) func([]string) error {
	who := fs.String("participant", "", "participant whose name was mentioned")
	utt := fs.Uint("utterance", 0, "event ID of the utterance that mentioned the name")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if *who == "" || *utt == 0 {
			return usagef("--participant and --utterance are required")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.Addressed(e.ctx, sid, *who, uint32(*utt))
		})
	}
}

func cmdRaiseHand(e *env, fs *flag.FlagSet) func([]string) error {
	topic := fs.String("topic", "", "what you want to say")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.RaiseHand(e.ctx, sid, *topic)
		})
	}
}

// readText returns the positional text or the content of file ("-" is stdin).
func (e *env) readText(args []string, file string) (string, error) {
	switch {
	case file != "" && len(args) > 0:
		return "", usagef("give the text or --file, not both")
	case file == "-":
		b, err := io.ReadAll(e.stdin)
		return string(b), err
	case file != "":
		b, err := os.ReadFile(file)
		return string(b), err
	}
	return strings.Join(args, " "), nil
}

func cmdMinutes(e *env, fs *flag.FlagSet) func([]string) error {
	file := fs.String("file", "", "read the minutes from this file (- for stdin)")
	return func(args []string) error {
		text, err := e.readText(args, *file)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office minutes update <text> | --file F")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.UpdateMinutes(e.ctx, sid, text)
		})
	}
}

func cmdStageFocus(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office stage focus <artifact-id>")
		}
		return e.sessionCommand(func(c coreapi.Client, sid string) (*sessioncommandsv1.CommandResult, error) {
			return c.FocusArtifact(e.ctx, sid, args[0])
		})
	}
}

func cmdStageLink(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		sid, err := e.sessionID(c)
		if err != nil {
			return err
		}
		res, err := c.StageLink(e.ctx, sid)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"url": res.GetUrl(), "expiresAt": res.GetExpiresAt().AsTime().Format(time.RFC3339)})
		} else {
			fmt.Fprintln(e.stdout, res.GetUrl())
		}
		return nil
	}
}

// parseDeck reads a deck file (see coreapi.ParseDeck).
func parseDeck(path string, b []byte) (*slidesv1.RenderDeckRequest, error) {
	req, err := coreapi.ParseDeck(path, b)
	if errors.Is(err, coreapi.ErrBadInput) {
		return nil, usagef("%v", err)
	}
	return req, err
}

func cmdSlidesRender(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office slides render <deck.json | deck.md>")
		}
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		req, err := parseDeck(args[0], b)
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		req.SessionId = e.session
		res, err := c.RenderDeck(e.ctx, req)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"id": res.GetDeck().GetId(), "mediaType": res.GetDeck().GetMediaType(),
				"staged": res.GetDeck().GetStaged(), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintln(e.stdout, res.GetDeck().GetId())
		}
		return nil
	}
}

// cmdSlidesChange is "slides change": it replaces a staged deck of the session --session before
// its release.
func cmdSlidesChange(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 2 {
			return usagef("usage: office slides change <id> <deck.json | deck.md> --session S")
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		req, err := parseDeck(args[1], b)
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.ChangeDeck(e.ctx, args[0], e.session, req.GetTitle(), req.GetSlides())
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"id": res.GetDeck().GetId(), "mediaType": res.GetDeck().GetMediaType(),
				"staged": res.GetDeck().GetStaged(), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "%s changed\n", res.GetDeck().GetId())
		}
		return nil
	}
}

// cmdSlidesRemove is "slides remove": it removes a staged deck of the session --session before
// its release.
func cmdSlidesRemove(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office slides remove <id> --session S")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.RemoveDeck(e.ctx, args[0], e.session)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"id": args[0], "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "%s removed\n", args[0])
		}
		return nil
	}
}

func cmdSlidesGet(e *env, fs *flag.FlagSet) func([]string) error {
	out := fs.String("out", "", "write the rendered deck to this file (default: stdout)")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office slides get <id> [--out FILE]")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		a, err := c.GetDeck(e.ctx, args[0])
		if err != nil {
			return err
		}
		switch {
		case *out != "":
			if err := os.WriteFile(*out, a.GetContent(), 0o600); err != nil {
				return err
			}
			if e.json {
				e.printJSON(map[string]any{"id": a.GetId(), "mediaType": a.GetMediaType(), "file": *out})
			}
		case e.json:
			e.printJSON(map[string]any{"deck": coreapi.MessageJSON(a)})
		default:
			_, err = e.stdout.Write(a.GetContent())
			return err
		}
		return nil
	}
}

// recordJSON returns a record (a decision, minutes or a note) in the JSON form of its contract.
func recordJSON(m proto.Message) json.RawMessage { return coreapi.MessageJSON(m) }

// oneLine returns a text on one line, for the text output of lists.
func oneLine(s string) string { return strings.ReplaceAll(strings.TrimSpace(s), "\n", " ") }

// listLine is the text output of one record in a list: "#ID who: text". A staged record says so,
// and a stub (a staged record that the caller may not read) shows only its ID.
func listLine(id uint32, stub, staged bool, who, text string) string {
	switch {
	case stub:
		return fmt.Sprintf("#%d (waits for the release of its session)", id)
	case staged:
		return fmt.Sprintf("#%d %s (staged): %s", id, who, oneLine(text))
	}
	return fmt.Sprintf("#%d %s: %s", id, who, oneLine(text))
}

// readRecordText reads the text of a new record from the arguments or --file.
func (e *env) readRecordText(args []string, file, use string) (string, error) {
	text, err := e.readText(args, file)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", usagef("usage: office %s <text> | --file F", use)
	}
	return text, nil
}

// listFlags adds the flags of the list commands: the cursor and the limit.
func listFlags(fs *flag.FlagSet, what string) (after, limit *uint) {
	after = fs.Uint("after", 0, "only "+what+" with an ID greater than this")
	limit = fs.Uint("limit", 0, "at most this many "+what+" (0: the hub's default)")
	return after, limit
}

func cmdDecisionRecord(e *env, fs *flag.FlagSet) func([]string) error {
	file := fs.String("file", "", "read the text from this file (- for stdin)")
	return func(args []string) error {
		text, err := e.readRecordText(args, *file, "decision record")
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		// Only an explicit --session links the decision to a session.
		res, err := c.RecordDecision(e.ctx, text, e.session)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"decision": recordJSON(res.GetDecision()), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "decision %d\n", res.GetDecision().GetId())
		}
		return nil
	}
}

func cmdDecisionList(e *env, fs *flag.FlagSet) func([]string) error {
	after, limit := listFlags(fs, "decisions")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ds, err := c.ListDecisions(e.ctx, uint32(*after), e.session, uint32(*limit))
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(ds))
			for i, d := range ds {
				out[i] = recordJSON(d)
			}
			e.printJSON(map[string]any{"decisions": out})
			return nil
		}
		for _, d := range ds {
			fmt.Fprintln(e.stdout, listLine(d.GetId(), d.GetStub(), d.GetStaged(), d.GetAuthorParticipantId(), d.GetText()))
		}
		return nil
	}
}

func cmdNoteAdd(e *env, fs *flag.FlagSet) func([]string) error {
	file := fs.String("file", "", "read the text from this file (- for stdin)")
	var tags stringList
	fs.Var(&tags, "tag", "a tag of the note; repeat it for more tags")
	return func(args []string) error {
		text, err := e.readRecordText(args, *file, "note add")
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		// Only an explicit --session links the note to a session.
		res, err := c.AddNote(e.ctx, text, e.session, tags)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"note": recordJSON(res.GetNote()), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "note %d\n", res.GetNote().GetId())
		}
		return nil
	}
}

func cmdNoteList(e *env, fs *flag.FlagSet) func([]string) error {
	after, limit := listFlags(fs, "notes")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ns, err := c.ListNotes(e.ctx, uint32(*after), e.session, uint32(*limit))
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(ns))
			for i, n := range ns {
				out[i] = recordJSON(n)
			}
			e.printJSON(map[string]any{"notes": out})
			return nil
		}
		for _, n := range ns {
			fmt.Fprintln(e.stdout, listLine(n.GetId(), n.GetStub(), n.GetStaged(), n.GetAuthorParticipantId(), n.GetText()))
		}
		return nil
	}
}

// cmdMinutesShow prints the stored minutes of an ended session: the session given as argument,
// else --session, else the only session joined from this machine.
func cmdMinutesShow(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) > 1 {
			return usagef("usage: office minutes show [<session>]")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		sid := ""
		if len(args) == 1 {
			sid = args[0]
		} else if sid, err = e.sessionID(c); err != nil {
			return err
		}
		m, err := c.GetMinutes(e.ctx, sid)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"minutes": recordJSON(m)})
			return nil
		}
		text := m.GetText()
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		_, err = io.WriteString(e.stdout, text)
		return err
	}
}

func cmdMinutesList(e *env, fs *flag.FlagSet) func([]string) error {
	after, limit := listFlags(fs, "minutes")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ms, err := c.ListMinutes(e.ctx, uint32(*after), e.session, uint32(*limit))
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(ms))
			for i, m := range ms {
				out[i] = recordJSON(m)
			}
			e.printJSON(map[string]any{"minutes": out})
			return nil
		}
		for _, m := range ms {
			fmt.Fprintln(e.stdout, listLine(m.GetId(), m.GetStub(), m.GetStaged(), m.GetSessionId()+" "+m.GetAuthorParticipantId(), m.GetText()))
		}
		return nil
	}
}

// cmdEvents reads the events after a cursor from the server (dec-events-from-cursor). It prints
// them in the line format of the local log, so the same filters work.
func cmdEvents(e *env, fs *flag.FlagSet) func([]string) error {
	after := fs.Uint("after", 0, "only events with an ID greater than this (the cursor)")
	limit := fs.Uint("limit", 0, "at most this many events (0: the server's default, at most 1000)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		sid, err := e.sessionID(c)
		if err != nil {
			return err
		}
		evs, more, err := c.Events(e.ctx, sid, uint32(*after), uint32(*limit))
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(evs))
			for i, ev := range evs {
				out[i] = coreapi.EventJSON(ev)
			}
			e.printJSON(map[string]any{"events": out, "more": more})
			return nil
		}
		for _, ev := range evs {
			fmt.Fprintln(e.stdout, string(coreapi.EventJSON(ev)))
		}
		if more {
			fmt.Fprintln(e.stderr, "more events: read again with --after", evs[len(evs)-1].GetId())
		}
		return nil
	}
}

// Documents (if-document-api). The text is the positional text after the path, or --file.

func cmdDocumentCreate(e *env, fs *flag.FlagSet) func([]string) error {
	file := fs.String("file", "", "read the content from this file (- for stdin)")
	return func(args []string) error {
		if len(args) < 1 {
			return usagef("usage: office document create <path> <text> | --file F")
		}
		text, err := e.readText(args[1:], *file)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office document create <path> <text> | --file F")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.CreateDocument(e.ctx, args[0], text, e.session)
		if err != nil {
			return err
		}
		return e.printDocument(res.GetDocument().GetPath(), res.GetDocument().GetVersion(), res.GetDocument().GetStaged(), res.GetDuplicate())
	}
}

func cmdDocumentChange(e *env, fs *flag.FlagSet) func([]string) error {
	file := fs.String("file", "", "read the content from this file (- for stdin)")
	base := fs.String("base", "", "the version of the document that you read (required)")
	return func(args []string) error {
		if len(args) < 1 || *base == "" {
			return usagef("usage: office document change <path> --base VERSION <text> | --file F")
		}
		text, err := e.readText(args[1:], *file)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office document change <path> --base VERSION <text> | --file F")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.ChangeDocument(e.ctx, args[0], text, *base, e.session)
		if err != nil {
			return err
		}
		return e.printDocument(res.GetDocument().GetPath(), res.GetDocument().GetVersion(), res.GetDocument().GetStaged(), res.GetDuplicate())
	}
}

func cmdDocumentRemove(e *env, fs *flag.FlagSet) func([]string) error {
	base := fs.String("base", "", "the version of the document that you read (required)")
	return func(args []string) error {
		if len(args) != 1 || *base == "" {
			return usagef("usage: office document remove <path> --base VERSION --session S")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		res, err := c.RemoveDocument(e.ctx, args[0], *base, e.session)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"path": args[0], "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "%s removed\n", args[0])
		}
		return nil
	}
}

// printDocument prints the result of a create or a change: the version, or JSON.
func (e *env) printDocument(path, version string, staged, duplicate bool) error {
	if e.json {
		e.printJSON(map[string]any{"path": path, "version": version, "staged": staged, "duplicate": duplicate})
	} else {
		fmt.Fprintln(e.stdout, version)
	}
	return nil
}

func cmdDocumentGet(e *env, fs *flag.FlagSet) func([]string) error {
	out := fs.String("out", "", "write the content to this file (default: stdout)")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office document get <path> [--out FILE]")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		f, err := c.ReadFile(e.ctx, args[0])
		if err != nil {
			return err
		}
		switch {
		case *out != "":
			if err := os.WriteFile(*out, f.GetContent(), 0o600); err != nil {
				return err
			}
			if e.json {
				e.printJSON(map[string]any{"path": f.GetPath(), "version": f.GetVersion(), "file": *out})
			}
		case e.json:
			e.printJSON(map[string]any{"path": f.GetPath(), "version": f.GetVersion(), "staged": f.GetStaged(), "content": string(f.GetContent())})
		default:
			_, err = e.stdout.Write(f.GetContent())
			return err
		}
		return nil
	}
}

func cmdDocumentList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		dir := "documents"
		switch len(args) {
		case 1:
			dir = args[0]
		case 0:
		default:
			return usagef("usage: office document list [<dir>]")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		files, err := c.ListFiles(e.ctx, dir)
		if err != nil {
			return err
		}
		if e.json {
			list := make([]map[string]any, 0, len(files))
			for _, f := range files {
				list = append(list, map[string]any{"path": f.GetPath(), "version": f.GetVersion(), "staged": f.GetStaged()})
			}
			e.printJSON(map[string]any{"files": list})
			return nil
		}
		for _, f := range files {
			fmt.Fprintf(e.stdout, "%s\t%s\n", f.GetPath(), f.GetVersion())
		}
		return nil
	}
}

func cmdPortalLink(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		tenant := e.cfg.Tenant
		res, err := c.CreatePortalLink(e.ctx, tenant)
		if connect.CodeOf(err) == connect.CodeNotFound && tenant != "" {
			return fmt.Errorf("you are not a member of the tenant %q: name another one with --tenant, or open the start page with --tenant \"\"", tenant)
		}
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"url": res.GetUrl(), "expiresAt": res.GetExpiresAt().AsTime().Format(time.RFC3339)})
		} else {
			fmt.Fprintln(e.stdout, res.GetUrl())
		}
		return nil
	}
}

// cmdSessionList lists the sessions that the participant may see (if-session-list).
func cmdSessionList(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		ss, err := c.ListSessions(e.ctx)
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(ss))
			for i, s := range ss {
				out[i] = recordJSON(s)
			}
			e.printJSON(map[string]any{"sessions": out})
			return nil
		}
		for _, s := range ss {
			end := "running"
			if s.GetEndedAt() != nil {
				end = s.GetEndedAt().AsTime().UTC().Format(time.RFC3339)
			}
			var who []string
			for _, p := range s.GetParticipants() {
				who = append(who, p.GetParticipantId())
			}
			mark := ""
			if s.GetImported() {
				mark = " (imported)"
			}
			fmt.Fprintf(e.stdout, "%s%s\t%s\t%s\t%s\n", s.GetSessionId(), mark, s.GetStartedAt().AsTime().UTC().Format(time.RFC3339), end,
				strings.Join(who, ", "))
		}
		return nil
	}
}

// recordID reads the ID of a record from the first argument.
func recordID(args []string, use string) (uint32, []string, error) {
	if len(args) == 0 {
		return 0, nil, usagef("usage: office %s", use)
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(args[0], "#"), 10, 32)
	if err != nil || n == 0 {
		return 0, nil, usagef("the ID %q is not a record ID; usage: office %s", args[0], use)
	}
	return uint32(n), args[1:], nil
}

// cmdRecordChange is "decision change", "note change" and "minutes change": they change a staged
// record of the session --session before its release.
func cmdRecordChange(what string, tagged bool, change func(e *env, c coreapi.Client, id uint32, text string, tags []string) (proto.Message, bool, error)) func(*env, *flag.FlagSet) func([]string) error {
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		file := fs.String("file", "", "read the text from this file (- for stdin)")
		var tags stringList
		if tagged {
			fs.Var(&tags, "tag", "a tag of the note; repeat it for more tags. The tags replace the old ones")
		}
		use := what + " change <id> <text> | --file F --session S"
		return func(args []string) error {
			id, rest, err := recordID(args, use)
			if err != nil {
				return err
			}
			text, err := e.readRecordText(rest, *file, what+" change <id>")
			if err != nil {
				return err
			}
			c, err := e.client()
			if err != nil {
				return err
			}
			r, dup, err := change(e, c, id, text, tags)
			if err != nil {
				return err
			}
			if e.json {
				e.printJSON(map[string]any{what: recordJSON(r), "duplicate": dup})
			} else {
				fmt.Fprintf(e.stdout, "%s %d changed\n", what, id)
			}
			return nil
		}
	}
}

// cmdRecordRemove is "decision remove", "note remove" and "minutes remove": they remove a staged
// record of the session --session before its release.
func cmdRecordRemove(what string, remove func(e *env, c coreapi.Client, id uint32) (bool, error)) func(*env, *flag.FlagSet) func([]string) error {
	return func(e *env, fs *flag.FlagSet) func([]string) error {
		return func(args []string) error {
			id, rest, err := recordID(args, what+" remove <id> --session S")
			if err != nil {
				return err
			}
			if err := noArgs(rest); err != nil {
				return err
			}
			c, err := e.client()
			if err != nil {
				return err
			}
			dup, err := remove(e, c, id)
			if err != nil {
				return err
			}
			if e.json {
				e.printJSON(map[string]any{"id": id, "duplicate": dup})
			} else {
				fmt.Fprintf(e.stdout, "%s %d removed\n", what, id)
			}
			return nil
		}
	}
}

var (
	cmdDecisionChange = cmdRecordChange("decision", false, func(e *env, c coreapi.Client, id uint32, text string, _ []string) (proto.Message, bool, error) {
		res, err := c.ChangeDecision(e.ctx, id, e.session, text)
		return res.GetDecision(), res.GetDuplicate(), err
	})
	cmdDecisionRemove = cmdRecordRemove("decision", func(e *env, c coreapi.Client, id uint32) (bool, error) {
		res, err := c.RemoveDecision(e.ctx, id, e.session)
		return res.GetDuplicate(), err
	})
	cmdNoteChange = cmdRecordChange("note", true, func(e *env, c coreapi.Client, id uint32, text string, tags []string) (proto.Message, bool, error) {
		res, err := c.ChangeNote(e.ctx, id, e.session, text, tags)
		return res.GetNote(), res.GetDuplicate(), err
	})
	cmdNoteRemove = cmdRecordRemove("note", func(e *env, c coreapi.Client, id uint32) (bool, error) {
		res, err := c.RemoveNote(e.ctx, id, e.session)
		return res.GetDuplicate(), err
	})
	cmdMinutesChange = cmdRecordChange("minutes", false, func(e *env, c coreapi.Client, id uint32, text string, _ []string) (proto.Message, bool, error) {
		res, err := c.ChangeMinutes(e.ctx, id, e.session, text)
		return res.GetMinutes(), res.GetDuplicate(), err
	})
	cmdMinutesRemove = cmdRecordRemove("minutes", func(e *env, c coreapi.Client, id uint32) (bool, error) {
		res, err := c.RemoveMinutes(e.ctx, id, e.session)
		return res.GetDuplicate(), err
	})
)
