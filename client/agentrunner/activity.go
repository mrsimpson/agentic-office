package agentrunner

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	acp "github.com/coder/acp-go-sdk"

	sessionactivityv1 "github.com/mrsimpson/agentic-office/hub/session/activityapi"
	"github.com/mrsimpson/agentic-office/hub/session/activityapi/sessionactivityv1connect"
)

// activity is what the runner reports of its agent (if-session-activity, dec-agent-activity).
type activity struct {
	working  bool
	tool     string // short name of the tool that the agent calls now
	unspoken bool   // the last turn ended with a reply that nobody heard
}

// officeToolName finds the name of an office tool in the title of a tool call, as harnesses name
// MCP tools: "mcp__office__utterance_say", "office_utterance_say".
var officeToolName = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])office[_\W]+([a-z][a-z0-9_]*)`)

// kindName is the form of a tool kind that the runner reports.
var kindName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// shortToolName is the name of a tool call that the runner reports: the name of an office tool,
// else the kind of the tool (for example "fetch" or "search"). Never the title itself: a title
// can carry the arguments of the call, and they can carry what was said off the record.
func shortToolName(title string, kind acp.ToolKind) string {
	if m := officeToolName.FindStringSubmatch(title); m != nil && len(m[1]) <= 64 {
		return strings.ToLower(m[1])
	}
	k := strings.ToLower(string(kind))
	if !kindName.MatchString(k) {
		return "other"
	}
	return k
}

// reporter reports the activity of the agent to the hub, in the background, so the runner never
// waits for it. Only the latest activity counts: a report that waits is replaced by a newer one.
// It renews the activity before it expires. A failing report is logged once and never stops the
// runner: the activity is a view for humans, not part of the meeting.
type reporter struct {
	client  sessionactivityv1connect.SessionActivityServiceClient
	session string
	self    string
	logf    func(string, ...any)
	next    chan activity
	stopped atomic.Bool
}

func newReporter(client sessionactivityv1connect.SessionActivityServiceClient, session, self string, logf func(string, ...any)) *reporter {
	return &reporter{client: client, session: session, self: self, logf: logf, next: make(chan activity, 1)}
}

// stop ends the reports, for example after the session ended or the agent left: the hub would
// refuse them.
func (r *reporter) stop() { r.stopped.Store(true) }

// set reports a, replacing a report that still waits.
func (r *reporter) set(a activity) {
	if r.stopped.Load() {
		return
	}
	for {
		select {
		case r.next <- a:
			return
		default:
		}
		select {
		case <-r.next:
		default:
		}
	}
}

// run sends the reports until ctx ends.
func (r *reporter) run(ctx context.Context) {
	var cur activity
	have, failing := false, false
	renew := time.NewTimer(time.Hour)
	renew.Stop()
	defer renew.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case cur = <-r.next:
			have = true
		case <-renew.C:
		}
		if !have || r.stopped.Load() {
			continue
		}
		req := &sessionactivityv1.ReportActivityRequest{SessionId: r.session, ParticipantId: r.self,
			State: sessionactivityv1.ActivityState_ACTIVITY_STATE_IDLE, UnspokenReply: cur.unspoken}
		if cur.working {
			req.State, req.Tool, req.UnspokenReply = sessionactivityv1.ActivityState_ACTIVITY_STATE_WORKING, cur.tool, false
		}
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		res, err := r.client.ReportActivity(cctx, req)
		cancel()
		ttl := 30 * time.Second
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil:
			if !failing {
				r.logf("cannot report the activity of the agent to the hub (the meeting goes on): %v", err)
			}
			failing = true
		default:
			if failing {
				r.logf("the activity of the agent reaches the hub again")
			}
			failing = false
			if d := res.GetTtl().AsDuration(); d > 0 {
				ttl = d
			}
		}
		renew.Reset(ttl / 3)
	}
}
