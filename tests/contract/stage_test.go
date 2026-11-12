package contract

// Contract tests of the stage (bb-stage) through if-stage-web. They start the real office-hub,
// create stage links through the identity API, load the page with an HTTP client, parse it with
// an HTML parser, and read the server-sent events directly. No browser is needed.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	sessionstreamv1 "github.com/mrsimpson/agentic-office/hub/session/streamapi"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// stageLink creates a stage link for sid as a.
func stageLink(t *testing.T, a *contracttest.Actor, sid string) string {
	t.Helper()
	res, err := a.Identity.CreateStageLink(ctx10(t), &identityv1.CreateStageLinkRequest{SessionId: sid, CommandId: coreapi.NewCommandID()})
	if err != nil {
		t.Fatalf("create stage link: %v", err)
	}
	return res.GetUrl()
}

// eventsURL is the event stream of a stage link.
func eventsURL(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	u.RawPath = u.EscapedPath() + "/events"
	u.Path += "/events"
	if esc := u.EscapedPath(); !strings.HasSuffix(esc, "/events") {
		t.Fatalf("events path %q", esc)
	}
	return u.String()
}

// withToken replaces the token of a link.
func withToken(t *testing.T, link, token string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if token == "" {
		q.Del("token")
	} else {
		q.Set("token", token)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func get(t *testing.T, srv *contracttest.Hub, link string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx10(t), http.MethodGet, link, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := srv.HTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", link, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

// DOM helpers.

func parseDoc(t *testing.T, s string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(s))
	if err != nil {
		t.Fatalf("parse page: %v", err)
	}
	return doc
}

// parseFragment parses the inner HTML of a region into a div.
func parseFragment(t *testing.T, s string) *html.Node {
	t.Helper()
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(s), root)
	if err != nil {
		t.Fatalf("parse fragment: %v", err)
	}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	return root
}

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// findAll returns all elements below n (and n) for which match is true.
func findAll(n *html.Node, match func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && match(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

func withAttr(key, val string) func(*html.Node) bool {
	return func(n *html.Node) bool { v, ok := attr(n, key); return ok && (val == "" || v == val) }
}

func byTag(tag string) func(*html.Node) bool { return func(n *html.Node) bool { return n.Data == tag } }

// text returns the visible text of n with white space collapsed.
func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// stageStream reads the server-sent events of a stage.
type stageStream struct {
	t      *testing.T
	mu     sync.Mutex
	latest map[string]string // region: last inner HTML
	counts map[string]int    // event name: how often it came
	ended  bool              // the "end" event came
	closed bool              // the stream closed
	notify chan struct{}
	cancel context.CancelFunc
}

func openStage(t *testing.T, srv *contracttest.Hub, link string) *stageStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL(t, link), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	res, err := srv.HTTPClient(t).Do(req)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		res.Body.Close()
		t.Fatalf("event stream: status %d, content type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	s := &stageStream{t: t, latest: map[string]string{}, counts: map[string]int{}, notify: make(chan struct{}, 1), cancel: cancel}
	go func() {
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
		var name string
		var data []string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if name != "" || data != nil {
					s.mu.Lock()
					if name == "" {
						name = "message"
					}
					s.counts[name]++
					if name == "end" {
						s.ended = true
					} else {
						s.latest[name] = strings.Join(data, "\n")
					}
					s.mu.Unlock()
					s.poke()
				}
				name, data = "", nil
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimPrefix(strings.TrimPrefix(line, "event:"), " ")
			case strings.HasPrefix(line, "data:"):
				data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			}
		}
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.poke()
	}()
	return s
}

func (s *stageStream) poke() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *stageStream) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[name]
}

// wait waits until cond is true for the latest HTML of region and returns the parsed region.
func (s *stageStream) wait(region, what string, cond func(*html.Node) bool) *html.Node {
	s.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		frag, ok := s.latest[region]
		closed := s.closed
		s.mu.Unlock()
		if ok {
			if n := parseFragment(s.t, frag); cond(n) {
				return n
			}
		}
		if closed {
			s.t.Fatalf("stage %s: stream closed before %s; last HTML: %s", region, what, frag)
		}
		select {
		case <-s.notify:
		case <-deadline:
			s.t.Fatalf("stage %s: no %s within 10 s; last HTML: %s", region, what, frag)
		}
	}
}

// waitEnd waits until the stream sent "end" and closed.
func (s *stageStream) waitEnd() {
	s.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		s.mu.Lock()
		done := s.ended && s.closed
		s.mu.Unlock()
		if done {
			return
		}
		select {
		case <-s.notify:
		case <-deadline:
			s.t.Fatalf("stage stream did not send end and close within 10 s")
		}
	}
}

// participantEl returns the list item of a participant in the participants region, or nil.
func participantEl(n *html.Node, pid string) *html.Node {
	if els := findAll(n, withAttr("data-participant", pid)); len(els) > 0 {
		return els[0]
	}
	return nil
}

func hasStatus(status string) func(*html.Node) bool {
	return func(n *html.Node) bool { return len(findAll(n, withAttr("data-status", status))) == 1 }
}

// stageMeeting is a session with a stage link.
type stageMeeting struct {
	*meeting
	link string
}

func newStageMeeting(t *testing.T, args ...string) *stageMeeting {
	t.Helper()
	return newStageMeetingOn(t, contracttest.StartHub(t, nil, args...))
}

// newStageMeetingOn starts a stage meeting on a running hub.
func newStageMeetingOn(t *testing.T, srv *contracttest.Hub) *stageMeeting {
	t.Helper()
	m := &meeting{t: t, srv: srv, sid: "budget/" + coreapi.NewCommandID(), actors: map[string]*contracttest.Actor{},
		reader: srv.Participant(t, "reader", "Reader", agent)}
	return &stageMeeting{meeting: m, link: stageLink(t, m.reader, m.sid)}
}

func (m *stageMeeting) joinAs(name string, kind sessionstreamv1.ParticipantKind, captured bool) string {
	m.t.Helper()
	res, err := m.register(name, kind).Commands.Join(m.ctx(), &sessioncommandsv1.JoinRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), Kind: kind, Captured: captured})
	if err != nil {
		m.t.Fatalf("join %s: %v", name, err)
	}
	return res.GetParticipantId()
}

// TestStagePageNeedsStageLink: the page and its event stream load with a valid stage link for the
// session. Without a token, with a bad token, with a participant token, or with a link for
// another session, they fail with 401 or 403 and show no session data.
func TestStagePageNeedsStageLink(t *testing.T) {
	m := newStageMeeting(t)
	ada := m.joinAs("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, true)
	m.say(ada, "the secret plan")

	res, body := get(t, m.srv, m.link)
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("page: status %d, content type %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") ||
		!strings.Contains(res.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("page headers: %v", res.Header)
	}
	doc := parseDoc(t, body)
	if s := findAll(doc, withAttr("id", "session")); len(s) != 1 || text(s[0]) != m.sid {
		t.Errorf("page does not name the session %q", m.sid)
	}
	mains := findAll(doc, withAttr("id", "stage"))
	if len(mains) != 1 {
		t.Fatalf("page has no element #stage")
	}
	events, _ := attr(mains[0], "data-events")
	if u, err := url.Parse(events); err != nil || u.Query().Get("token") != stageToken(t, m.link) ||
		u.EscapedPath() != "/stage/"+url.PathEscape(m.sid)+"/events" {
		t.Errorf("data-events %q does not point to the event stream of the session", events)
	}
	for _, id := range []string{"status", "origin", "participants", "artifact", "artifacts", "artifactlog", "minutes", "utterances"} {
		if len(findAll(doc, withAttr("id", id))) != 1 {
			t.Errorf("page has no region #%s", id)
		}
	}
	// No external assets: no element loads anything from an address.
	for _, n := range findAll(doc, func(n *html.Node) bool { _, s := attr(n, "src"); _, h := attr(n, "href"); return s || h }) {
		t.Errorf("page loads an asset: <%s %v>", n.Data, n.Attr)
	}
	// The one inline script is allowed by its hash in the Content-Security-Policy, nothing else is.
	if scripts := findAll(doc, byTag("script")); len(scripts) != 1 || scripts[0].FirstChild == nil {
		t.Errorf("page should have exactly one inline script")
	} else {
		sum := sha256.Sum256([]byte(scripts[0].FirstChild.Data))
		want := "script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, want) {
			t.Errorf("CSP %q does not allow the inline script (%s)", csp, want)
		}
	}

	other := stageLink(t, m.reader, "another session")
	cases := []struct {
		name string
		link string
		want int
	}{
		{"no token", withToken(t, m.link, ""), http.StatusUnauthorized},
		{"unknown token", withToken(t, m.link, "ost_not-a-token"), http.StatusUnauthorized},
		{"participant token", withToken(t, m.link, func() string {
			tok, err := m.reader.Tokens.Token(ctx10(t))
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}()), http.StatusForbidden},
		{"link for another session", withToken(t, m.link, stageToken(t, other)), http.StatusForbidden},
	}
	for _, c := range cases {
		for _, target := range []string{c.link, eventsURL(t, c.link)} {
			res, body := get(t, m.srv, target)
			if res.StatusCode != c.want || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
				t.Errorf("%s, %s: status %d (%s), want %d with an error page", c.name, target, res.StatusCode,
					res.Header.Get("Content-Type"), c.want)
			}
			doc := parseDoc(t, body)
			if e := findAll(doc, withAttr("id", "error")); len(e) != 1 || text(e[0]) == "" {
				t.Errorf("%s: no error message in %s", c.name, body)
			}
			if strings.Contains(body, "secret plan") || strings.Contains(body, "Ada") || len(findAll(doc, withAttr("id", "stage"))) != 0 {
				t.Errorf("%s: the error page shows session data: %s", c.name, body)
			}
		}
	}
	// The link of the other session works for that session.
	if res, _ := get(t, m.srv, other); res.StatusCode != http.StatusOK {
		t.Errorf("link of another session: status %d", res.StatusCode)
	}
	if res, _ := get(t, m.srv, m.srv.URL+"/stage/"+url.PathEscape(m.sid)+"/nothing?token="+stageToken(t, m.link)); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown stage path: status %d, want 404", res.StatusCode)
	}
}

// TestStageShowsTheSession: every change of the session log appears in the live stream of the
// stage: participants with their capture status and agents marked, raised hands, the shown
// artifact (and a note for an unknown one), minutes, utterances (off the record ones disappear), and
// the end of the session.
func TestStageShowsTheSession(t *testing.T) {
	m := newStageMeeting(t)
	st := openStage(t, m.srv, m.link)
	st.wait("participants", "empty list", func(n *html.Node) bool { return len(findAll(n, withAttr("data-participant", ""))) == 0 })
	st.wait("status", "live status", hasStatus("live"))
	st.wait("artifact", "nothing shown", func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 0 })

	// Participants: captured, not captured (an agent), paused.
	ada := m.joinAs("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, true)
	bot := m.joinAs("Minute Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)
	carl := m.joinAs("Carl", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, true)
	if _, err := m.s(carl).Pause(m.ctx(), &sessioncommandsv1.PauseRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(), ParticipantId: carl}); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{ada: {"human", "captured"}, bot: {"agent", "not-captured"}, carl: {"human", "paused"}}
	n := st.wait("participants", "three participants with their status", func(n *html.Node) bool {
		for pid, w := range want {
			el := participantEl(n, pid)
			if el == nil {
				return false
			}
			if c, _ := attr(el, "data-capture"); c != w[1] {
				return false
			}
		}
		return true
	})
	for pid, w := range want {
		el := participantEl(n, pid)
		kind, _ := attr(el, "data-kind")
		visible := text(el)
		badge := len(findAll(el, withAttr("data-kind", "agent"))) > 0
		if kind != w[0] || badge != (w[0] == "agent") || !strings.Contains(visible, strings.ReplaceAll(w[1], "-", " ")) {
			t.Errorf("participant %s: kind %q, agent badge %v, text %q; want %v", pid, kind, badge, visible, w)
		}
	}
	if !strings.Contains(text(participantEl(n, bot)), "Minute Bot agent") {
		t.Errorf("agent is not marked as agent: %q", text(participantEl(n, bot)))
	}
	// Resume makes Carl captured again.
	if _, err := m.s(carl).Resume(m.ctx(), &sessioncommandsv1.ResumeRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(), ParticipantId: carl}); err != nil {
		t.Fatal(err)
	}
	st.wait("participants", "Carl captured again", func(n *html.Node) bool {
		el := participantEl(n, carl)
		c, _ := attr(el, "data-capture")
		return c == "captured"
	})

	// A raised hand with its topic.
	if _, err := m.s(bot).RaiseHand(m.ctx(), &sessioncommandsv1.RaiseHandRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, Topic: "budget <b>numbers</b>"}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("participants", "hand of the bot", func(n *html.Node) bool {
		el := participantEl(n, bot)
		if el == nil {
			return false
		}
		_, up := attr(el, "data-hand")
		return up
	})
	if got := text(participantEl(n, bot)); !strings.Contains(got, "budget <b>numbers</b>") {
		t.Errorf("raised hand: %q", got)
	}
	if hand, _ := attr(participantEl(n, bot), "data-hand"); hand != "1" {
		t.Errorf("the hand is number %q in the queue, want 1", hand)
	}
	if counter := findAll(n, withAttr("data-hands", "1")); len(counter) != 1 {
		t.Errorf("no counter of raised hands: %q", text(n))
	}
	if len(findAll(n, byTag("b"))) != 0 {
		t.Errorf("the topic became HTML")
	}

	// The shown artifact is embedded in a sandboxed iframe with the content of the artifact.
	art := render(t, m.actors[bot], budgetDeck())
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, ArtifactId: art.GetId()}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("artifact", "iframe of "+art.GetId(), func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 1 })
	frame := findAll(n, byTag("iframe"))[0]
	srcdoc, _ := attr(frame, "srcdoc")
	sandbox, sandboxed := attr(frame, "sandbox")
	if srcdoc != string(art.GetContent()) || !sandboxed || sandbox != "" {
		t.Errorf("iframe: sandbox %q (%v), srcdoc equal to the artifact: %v", sandbox, sandboxed, srcdoc == string(art.GetContent()))
	}
	if id, _ := attr(frame, "data-artifact"); id != art.GetId() {
		t.Errorf("iframe names artifact %q, want %q", id, art.GetId())
	}
	artifactEvents := st.count("artifact")

	// Minutes.
	if _, err := m.s(bot).UpdateMinutes(m.ctx(), &sessioncommandsv1.UpdateMinutesRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, Text: "# Minutes\n- Ada opened the meeting"}); err != nil {
		t.Fatal(err)
	}
	st.wait("minutes", "the minutes", func(n *html.Node) bool {
		return strings.Contains(text(n), "Ada opened the meeting") && len(findAll(n, byTag("h1"))) == 1
	})

	// Utterances; a off the record one disappears.
	m.say(ada, "Welcome everyone")
	secret := m.say(ada, "my password is hunter2")
	st.wait("utterances", "the secret", func(n *html.Node) bool { return strings.Contains(text(n), "hunter2") })
	if _, err := m.s(ada).MarkOffTheRecord(m.ctx(), &sessioncommandsv1.MarkOffTheRecordRequest{SessionId: m.sid,
		CommandId: coreapi.NewCommandID(), ParticipantId: ada, UtteranceEventId: secret.GetId()}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("utterances", "the secret gone", func(n *html.Node) bool { return !strings.Contains(text(n), "hunter2") })
	if !strings.Contains(text(n), "Ada: Welcome everyone") {
		t.Errorf("utterances after the off-the-record mark: %q", text(n))
	}

	// When the bot speaks, its hand goes down; agent contributions are marked.
	m.say(bot, "The budget is on the stage.")
	st.wait("participants", "no raised hand", func(n *html.Node) bool {
		el := participantEl(n, bot)
		if el == nil {
			return false
		}
		_, up := attr(el, "data-hand")
		return !up && len(findAll(n, withAttr("data-hands", ""))) == 0
	})
	st.wait("utterances", "the bot's contribution", func(n *html.Node) bool {
		return strings.Contains(text(n), "Minute Bot agent: The budget is on the stage.")
	})
	if got := st.count("artifact"); got != artifactEvents {
		t.Errorf("the artifact region was sent again %d times without a change: the iframe would reload", got-artifactEvents)
	}

	// An unknown artifact gives a note.
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, ArtifactId: "slides/999"}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("artifact", "a note for slides/999", func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 0 })
	if notes := findAll(n, withAttr("data-artifact", "slides/999")); len(notes) != 1 || !strings.Contains(text(notes[0]), "slides/999") {
		t.Errorf("no note for the unknown artifact: %q", text(n))
	}
	// An artifact ID of a type that the stage does not know gives a note too.
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, ArtifactId: "plans/1"}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("artifact", "a note for plans/1", func(n *html.Node) bool { return len(findAll(n, withAttr("data-artifact", "plans/1"))) == 1 })
	if len(findAll(n, byTag("iframe"))) != 0 || !strings.Contains(text(n), "plans/1") {
		t.Errorf("no note for an artifact of another type: %q", text(n))
	}

	// A second page that opens now sees the same state at once.
	late := openStage(t, m.srv, m.link)
	late.wait("utterances", "the history", func(n *html.Node) bool {
		return strings.Contains(text(n), "Welcome everyone") && !strings.Contains(text(n), "hunter2")
	})
	late.wait("participants", "the participants", func(n *html.Node) bool { return participantEl(n, carl) != nil })

	// A participant who leaves is shown as left.
	if _, err := m.s(carl).Leave(m.ctx(), &sessioncommandsv1.LeaveRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(), ParticipantId: carl}); err != nil {
		t.Fatal(err)
	}
	st.wait("participants", "Carl left", func(n *html.Node) bool {
		c, _ := attr(participantEl(n, carl), "data-capture")
		return c == "left"
	})

	// The end of the session.
	m.end(ada)
	for _, s := range []*stageStream{st, late} {
		s.wait("status", "ended", hasStatus("ended"))
		s.waitEnd()
	}
	// After the end the page still loads and the stream replays the log and ends.
	if res, _ := get(t, m.srv, m.link); res.StatusCode != http.StatusOK {
		t.Errorf("page after the end: status %d", res.StatusCode)
	}
	again := openStage(t, m.srv, m.link)
	again.wait("status", "ended", hasStatus("ended"))
	again.wait("minutes", "the minutes", func(n *html.Node) bool { return strings.Contains(text(n), "Ada opened the meeting") })
	again.waitEnd()
}

// TestStageLinkExpires: an expired stage link gives 401, and an open stage shows that the link
// has expired and stops.
func TestStageLinkExpires(t *testing.T) {
	m := newStageMeeting(t, "-stage-link-ttl", "3s")
	m.joinAs("Ada", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_HUMAN, true)
	st := openStage(t, m.srv, m.link)
	st.wait("participants", "Ada", func(n *html.Node) bool { return participantEl(n, "ada") != nil })
	st.wait("status", "stopped", hasStatus("stopped"))
	st.waitEnd()
	for _, target := range []string{m.link, eventsURL(t, m.link)} {
		if res, body := get(t, m.srv, target); res.StatusCode != http.StatusUnauthorized || strings.Contains(body, "Ada") {
			t.Errorf("expired link %s: status %d, want 401 without session data", target, res.StatusCode)
		}
	}
}

// TestStageShowsDeckAfterRestart: a deck rendered before a restart of the hub is read back from
// the archive, and the stage shows it by its ID.
func TestStageShowsDeckAfterRestart(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	deck := render(t, srv.Participant(t, "ada", "Ada", human), budgetDeck())
	if code, err := srv.Stop(5 * time.Second); err != nil || code != 0 {
		t.Fatalf("stop: %d, %v", code, err)
	}
	srv = contracttest.StartHubIn(t, srv.DataDir, srv.AdminKey, nil)
	m := newStageMeetingOn(t, srv)
	st := openStage(t, m.srv, m.link)
	bot := m.joinAs("Minute Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, ArtifactId: deck.GetId()}); err != nil {
		t.Fatal(err)
	}
	n := st.wait("artifact", "iframe of "+deck.GetId(), func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 1 })
	if srcdoc, _ := attr(findAll(n, byTag("iframe"))[0], "srcdoc"); srcdoc != string(deck.GetContent()) {
		t.Error("the stage does not show the stored deck")
	}
}

// TestStageShowsDocuments: a document in focus is read from the archive by its path and shown as
// formatted text in a sandboxed frame. It shows a change, lists the artifacts of the session with
// the focused one marked, and gives a note for a path that cannot be read. Raw HTML of the author
// does not reach the page.
func TestStageShowsDocuments(t *testing.T) {
	m := newStageMeeting(t)
	st := openStage(t, m.srv, m.link)
	bot := m.joinAs("Minute Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)
	a := m.actors[bot]
	focus := func(path string) {
		t.Helper()
		if _, err := a.Commands.FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid,
			CommandId: coreapi.NewCommandID(), ParticipantId: bot, ArtifactId: path}); err != nil {
			t.Fatal(err)
		}
	}
	srcdoc := func(n *html.Node) string {
		frames := findAll(n, byTag("iframe"))
		if len(frames) != 1 {
			return ""
		}
		s, _ := attr(frames[0], "srcdoc")
		return s
	}

	res, err := a.Document.CreateDocument(m.ctx(), &documentv1.CreateDocumentRequest{Path: "documents/plan.md",
		Content: "# Budget\n\nHire **two** people.\n\n<script>alert(1)</script>\n", CommandId: coreapi.NewCommandID(), SessionId: m.sid})
	if err != nil {
		t.Fatal(err)
	}
	focus("documents/plan.md")
	n := st.wait("artifact", "the document as formatted text", func(n *html.Node) bool {
		return strings.Contains(srcdoc(n), "<h1>Budget</h1>")
	})
	doc := srcdoc(n)
	if !strings.Contains(doc, "<strong>two</strong>") || strings.Contains(doc, "<script") {
		t.Errorf("rendered document:\n%s", doc)
	}
	frame := findAll(n, byTag("iframe"))[0]
	if sandbox, ok := attr(frame, "sandbox"); !ok || sandbox != "" {
		t.Errorf("the frame has sandbox %q (%v), want an empty sandbox", sandbox, ok)
	}
	if id, _ := attr(frame, "data-artifact"); id != "documents/plan.md" {
		t.Errorf("frame names %q", id)
	}

	// A change of the document in focus appears without a new focus.
	if _, err := a.Document.ChangeDocument(m.ctx(), &documentv1.ChangeDocumentRequest{Path: "documents/plan.md", Content: "# Budget 2\n",
		BaseVersion: res.GetDocument().GetVersion(), CommandId: coreapi.NewCommandID(), SessionId: m.sid}); err != nil {
		t.Fatal(err)
	}
	st.wait("artifact", "the changed document", func(n *html.Node) bool { return strings.Contains(srcdoc(n), "<h1>Budget 2</h1>") })

	// Two artifacts: the list marks the one in focus.
	if _, err := a.Document.CreateDocument(m.ctx(), &documentv1.CreateDocumentRequest{Path: "documents/notes.md", Content: "Notes",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid}); err != nil {
		t.Fatal(err)
	}
	focus("documents/notes.md")
	st.wait("artifacts", "two artifacts, notes in focus", func(n *html.Node) bool {
		chips := findAll(n, withAttr("data-artifact", ""))
		if len(chips) != 2 {
			return false
		}
		for _, c := range chips {
			p, _ := attr(c, "data-artifact")
			cls, _ := attr(c, "class")
			if (p == "documents/notes.md") != strings.Contains(cls, "focused") {
				return false
			}
		}
		return true
	})

	// The artifact log names who did what, newest first.
	st.wait("artifactlog", "the log of the artifacts", func(n *html.Node) bool {
		items := findAll(n, withAttr("data-log", ""))
		if len(items) < 4 {
			return false
		}
		first := text(items[0])
		return strings.Contains(first, "Minute Bot") && strings.Contains(first, "focused") && strings.Contains(first, "documents/notes.md")
	})

	// The session logs cannot be shown.
	focus("sessions/x/log.yaml")
	n = st.wait("artifact", "a note for a path that cannot be read", func(n *html.Node) bool {
		return len(findAll(n, byTag("iframe"))) == 0 && len(findAll(n, withAttr("data-artifact", "sessions/x/log.yaml"))) == 1
	})
	if !strings.Contains(text(n), "cannot show") {
		t.Errorf("note: %q", text(n))
	}
}

// TestStageShowsMinutesInTheMiddle: while no artifact is in focus, the minutes take the middle of
// the stage. Until there are minutes, a placeholder asks for an agent that takes them. When an
// artifact gets the focus, the minutes move to the side.
func TestStageShowsMinutesInTheMiddle(t *testing.T) {
	m := newStageMeeting(t)
	st := openStage(t, m.srv, m.link)
	bot := m.joinAs("Minute Bot", sessionstreamv1.ParticipantKind_PARTICIPANT_KIND_AGENT, false)

	n := st.wait("artifact", "the placeholder", func(n *html.Node) bool {
		return len(findAll(n, withAttr("data-placeholder", "minutes"))) == 1
	})
	if !strings.Contains(text(n), "Ask an agent to join") {
		t.Errorf("placeholder: %q", text(n))
	}
	st.wait("minutes", "no side minutes", func(n *html.Node) bool { return text(n) == "" })

	if _, err := m.s(bot).UpdateMinutes(m.ctx(), &sessioncommandsv1.UpdateMinutesRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, Text: "# Weekly\n\n- Ada opened <b>the</b> meeting\n"}); err != nil {
		t.Fatal(err)
	}
	n = st.wait("artifact", "the minutes in the middle", func(n *html.Node) bool {
		return len(findAll(n, withAttr("data-minutes", ""))) == 1 && len(findAll(n, withAttr("data-placeholder", ""))) == 0
	})
	if len(findAll(n, byTag("h1"))) != 1 || len(findAll(n, byTag("li"))) != 1 || len(findAll(n, byTag("b"))) != 0 {
		t.Errorf("the minutes are not formatted text without raw HTML: %s", text(n))
	}

	// An artifact in focus takes the middle; the minutes go to the side.
	if _, err := m.actors[bot].Document.CreateDocument(m.ctx(), &documentv1.CreateDocumentRequest{Path: "documents/a.md", Content: "# A\n",
		CommandId: coreapi.NewCommandID(), SessionId: m.sid}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.s(bot).FocusArtifact(m.ctx(), &sessioncommandsv1.FocusArtifactRequest{SessionId: m.sid, CommandId: coreapi.NewCommandID(),
		ParticipantId: bot, ArtifactId: "documents/a.md"}); err != nil {
		t.Fatal(err)
	}
	st.wait("artifact", "the document", func(n *html.Node) bool { return len(findAll(n, byTag("iframe"))) == 1 })
	st.wait("minutes", "the minutes at the side", func(n *html.Node) bool { return strings.Contains(text(n), "Ada opened") })
}
