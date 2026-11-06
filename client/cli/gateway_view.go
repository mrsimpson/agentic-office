package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/term"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"

	"github.com/mrsimpson/agentic-office/client/participant"
)

// The gateway view (dec-gateway-cli) is a filtered view of the local log: the participant's own
// utterances with their status, and whether it is paused. It prints one line per change, so it
// works in a terminal and in a pipe. Keys (single key presses in a terminal, else bytes on
// stdin): p pauses or resumes, r retracts the last sent sentence, q quits.

// localStatus is the status of an utterance that the ears have not sent yet.
type localStatus struct {
	cmdID, text string
	status      participant.Status
}

type gatewayView struct {
	e             *env
	c             *participant.Client
	sid, pid, log string
	// local carries the status from the ears (#8). Nil without ears.
	local  <-chan localStatus
	paused atomic.Bool
	nl     string
	errMu  sync.Mutex
}

func (g *gatewayView) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g.nl = "\n"
	var keys io.Reader = g.e.stdin
	if f, ok := g.e.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if old, err := term.MakeRaw(int(f.Fd())); err == nil {
			defer term.Restore(int(f.Fd()), old)
			g.nl = "\r\n"
		}
	}
	fmt.Fprintf(g.e.stderr, "gateway: session %s as %s; keys: p pause/resume, r retract last sentence, q quit%s", g.sid, g.pid, g.nl)

	evs := make(chan *officev1.Event, 64)
	logErr := make(chan error, 1)
	go func() {
		logErr <- participant.FollowLog(ctx, g.log, true, func(ev *officev1.Event) error {
			select {
			case evs <- ev:
				return nil
			case <-ctx.Done():
				return participant.ErrStop
			}
		})
	}()
	keyCh := make(chan byte, 16)
	if keys != nil {
		go func() {
			buf := make([]byte, 1)
			for {
				if _, err := keys.Read(buf); err != nil {
					return // no more keys; the view keeps running
				}
				select {
				case keyCh <- buf[0]:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	state := participant.NewGateway(g.pid)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-logErr:
			if err != nil {
				return fmt.Errorf("read %s: %w", g.log, err)
			}
			return nil
		case l := <-g.local:
			g.show(state.Local(l.cmdID, l.text, l.status))
		case ev := <-evs:
			for _, ch := range state.Apply(ev) {
				g.show(ch)
			}
			g.paused.Store(state.Paused)
			if state.Ended || state.Left {
				return nil
			}
		case k := <-keyCh:
			switch k {
			case 'q', 'Q', 3, 4: // q, Ctrl-C, Ctrl-D
				return nil
			case 'p', 'P':
				var err error
				if state.Paused {
					_, err = g.c.Resume(ctx, g.sid)
				} else {
					_, err = g.c.Pause(ctx, g.sid)
				}
				g.report(err)
			case 'r', 'R':
				u := state.LastSent()
				if u == nil {
					g.report(errors.New("no sent sentence to retract"))
					continue
				}
				_, err := g.c.Retract(ctx, g.sid, u.EventID)
				g.report(err)
			}
		}
	}
}

// show prints one change: a JSON line with --json, else a short text line.
func (g *gatewayView) show(ch participant.Change) {
	if g.e.json {
		g.e.printJSON(ch)
		return
	}
	var s string
	switch {
	case ch.Utterance != nil:
		id := "-"
		if ch.Utterance.EventID != 0 {
			id = fmt.Sprint(ch.Utterance.EventID)
		}
		s = fmt.Sprintf("#%-5s %-9s %s", id, ch.Utterance.Status, ch.Utterance.Text)
	case ch.Paused != nil && *ch.Paused:
		s = "-- paused: nothing is transcribed (p resumes)"
	case ch.Paused != nil:
		s = "-- resumed"
	case ch.Left:
		s = "-- you left the session"
	case ch.Ended:
		s = "-- the session ended"
	}
	fmt.Fprint(g.e.stdout, strings.ReplaceAll(s, "\n", " ")+g.nl)
}

// earsReport prints progress and measurements of the ears on stderr: {"ears":{…}} with --json,
// else "ears: <text>". Stdout stays reserved for the changes of the view.
func (g *gatewayView) earsReport(text string, fields any) {
	g.errMu.Lock()
	defer g.errMu.Unlock()
	if g.e.json {
		b, err := json.Marshal(map[string]any{"ears": fields})
		if err == nil {
			fmt.Fprintf(g.e.stderr, "%s\n", b)
		}
		return
	}
	nl := g.nl
	if nl == "" {
		nl = "\n"
	}
	fmt.Fprintf(g.e.stderr, "ears: %s%s", strings.ReplaceAll(text, "\n", " "), nl)
}

// report prints an error of a key action and keeps the view running.
func (g *gatewayView) report(err error) {
	if err == nil {
		return
	}
	if g.e.json {
		g.e.printJSON(map[string]any{"error": map[string]string{"code": errorCode(err), "message": err.Error()}})
		return
	}
	fmt.Fprintf(g.e.stderr, "office: %v%s", err, g.nl)
}
