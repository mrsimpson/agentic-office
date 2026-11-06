package cli

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"

	"github.com/mrsimpson/agentic-office/client/participant"
)

func cmdSay(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		text := strings.Join(args, " ")
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office say <text>")
		}
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
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

func cmdRetract(e *env, fs *flag.FlagSet) func([]string) error {
	last := fs.Bool("last", false, "retract the last own sentence that is not retracted (read from the local log)")
	return func(args []string) error {
		if *last == (len(args) == 1) || len(args) > 1 {
			return usagef("usage: office utterance retract <event-id> | --last")
		}
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
			var id uint32
			if *last {
				pid, err := c.ParticipantID(e.ctx)
				if err != nil {
					return nil, err
				}
				p, err := c.Paths(sid, pid)
				if err != nil {
					return nil, err
				}
				evs, err := participant.ReadLog(p.Log)
				if err != nil {
					return nil, err
				}
				g := participant.NewGateway(pid)
				for _, ev := range evs {
					g.Apply(ev)
				}
				u := g.LastSent()
				if u == nil {
					return nil, errors.New("the local log has no own sentence to retract")
				}
				id = u.EventID
			} else {
				var err error
				if id, err = parseEventID(args[0]); err != nil {
					return nil, err
				}
			}
			return c.Retract(e.ctx, sid, id)
		})
	}
}

func cmdTurnEnd(e *env, fs *flag.FlagSet) func([]string) error {
	who := fs.String("participant", "", "participant whose turn ended (default: you)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
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
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
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
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
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
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
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
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
			return c.UpdateMinutes(e.ctx, sid, text)
		})
	}
}

func cmdStageShow(e *env, fs *flag.FlagSet) func([]string) error {
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office stage show <artifact-id>")
		}
		return e.sessionCommand(func(c *participant.Client, sid string) (*officev1.CommandResult, error) {
			return c.ShowArtifact(e.ctx, sid, args[0])
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

// parseDeck reads a deck: JSON in the form of RenderSlidesRequest ({"title": …, "slides":
// [{"title": …, "bullets": […], "notes": …}]}), or Markdown: "# " is the deck title, "## " starts
// a slide, "- " or "* " is a bullet, other text becomes the notes of the slide.
func parseDeck(path string, b []byte) (*officev1.RenderSlidesRequest, error) {
	req := &officev1.RenderSlidesRequest{}
	if strings.EqualFold(filepath.Ext(path), ".md") {
		var cur *officev1.Slide
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch {
			case strings.HasPrefix(line, "## "):
				cur = &officev1.Slide{Title: strings.TrimSpace(line[3:])}
				req.Slides = append(req.Slides, cur)
			case strings.HasPrefix(line, "# "):
				req.Title = strings.TrimSpace(line[2:])
			case line == "":
			case cur == nil:
				return nil, fmt.Errorf("%s: text before the first slide (\"## title\")", path)
			case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
				cur.Bullets = append(cur.Bullets, strings.TrimSpace(line[2:]))
			default:
				cur.Notes = strings.TrimSpace(cur.Notes + "\n" + line)
			}
		}
		return req, sc.Err()
	}
	if err := protojson.Unmarshal(b, req); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return req, nil
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
		res, err := c.RenderSlides(e.ctx, req)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"artifactId": res.GetArtifact().GetId(), "mediaType": res.GetArtifact().GetMediaType(),
				"duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintln(e.stdout, res.GetArtifact().GetId())
		}
		return nil
	}
}

func cmdArtifactGet(e *env, fs *flag.FlagSet) func([]string) error {
	out := fs.String("out", "", "write the content to this file (default: stdout)")
	return func(args []string) error {
		if len(args) != 1 {
			return usagef("usage: office artifact get <id> [--out FILE]")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		a, err := c.GetArtifact(e.ctx, args[0])
		if err != nil {
			return err
		}
		switch {
		case *out != "":
			if err := os.WriteFile(*out, a.GetContent(), 0o600); err != nil {
				return err
			}
			if e.json {
				e.printJSON(map[string]any{"artifactId": a.GetId(), "mediaType": a.GetMediaType(), "file": *out})
			}
		case e.json:
			e.printJSON(map[string]any{"artifact": protoJSON(a)})
		default:
			_, err = e.stdout.Write(a.GetContent())
			return err
		}
		return nil
	}
}

func parseEntryKind(s string) (officev1.EntryKind, error) {
	if s == "" {
		return officev1.EntryKind_ENTRY_KIND_UNSPECIFIED, nil
	}
	k, ok := officev1.EntryKind_value["ENTRY_KIND_"+strings.ToUpper(s)]
	if !ok || k == 0 {
		return 0, usagef("--kind must be decision, task, minutes or note, not %q", s)
	}
	return officev1.EntryKind(k), nil
}

func kindName(k officev1.EntryKind) string {
	return strings.ToLower(strings.TrimPrefix(k.String(), "ENTRY_KIND_"))
}

func cmdJournalAppend(e *env, fs *flag.FlagSet) func([]string) error {
	kind := fs.String("kind", "", "decision, task, minutes or note")
	file := fs.String("file", "", "read the text from this file (- for stdin)")
	return func(args []string) error {
		k, err := parseEntryKind(*kind)
		if err != nil {
			return err
		}
		if k == 0 {
			return usagef("--kind is required")
		}
		text, err := e.readText(args, *file)
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return usagef("usage: office journal append --kind K <text>")
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		// Only an explicit --session links the entry to a session.
		res, err := c.AppendJournal(e.ctx, k, text, e.session)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"entry": entryJSON(res.GetEntry()), "duplicate": res.GetDuplicate()})
		} else {
			fmt.Fprintf(e.stdout, "entry %d\n", res.GetEntry().GetId())
		}
		return nil
	}
}

func cmdJournalList(e *env, fs *flag.FlagSet) func([]string) error {
	kind := fs.String("kind", "", "only entries of this kind")
	after := fs.Uint("after", 0, "only entries with an ID greater than this")
	limit := fs.Uint("limit", 0, "at most this many entries (0: the server's default)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		k, err := parseEntryKind(*kind)
		if err != nil {
			return err
		}
		c, err := e.client()
		if err != nil {
			return err
		}
		entries, err := c.ListJournal(e.ctx, &officev1.ListEntriesRequest{AfterEntryId: uint32(*after), Kind: k,
			SessionId: e.session, Limit: uint32(*limit)})
		if err != nil {
			return err
		}
		if e.json {
			out := make([]any, len(entries))
			for i, en := range entries {
				out[i] = entryJSON(en)
			}
			e.printJSON(map[string]any{"entries": out})
			return nil
		}
		for _, en := range entries {
			fmt.Fprintf(e.stdout, "#%d %s %s: %s\n", en.GetId(), kindName(en.GetKind()), en.GetAuthorParticipantId(),
				strings.ReplaceAll(en.GetText(), "\n", " "))
		}
		return nil
	}
}
