package contract

// Contract tests of the release of a session in the portal (if-portal-web, dec-web-portal,
// concept-session-release). The forms call the session commands; the hub decides.

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/events"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// portalPage opens a page of the portal and parses it.
func portalPage(t *testing.T, b *http.Client, u string) (*http.Response, *html.Node, string) {
	t.Helper()
	res, body := fetch(t, b, u)
	return res, parseDoc(t, body), body
}

// one returns the only element of the page with the attribute, or fails.
func one(t *testing.T, doc *html.Node, key, val string) *html.Node {
	t.Helper()
	got := findAll(doc, withAttr(key, val))
	if len(got) != 1 {
		t.Fatalf("%d elements with %s=%q, want 1\n%s", len(got), key, val, text(doc))
	}
	return got[0]
}

// formOf returns the fields of a form of the page.
func formOf(t *testing.T, doc *html.Node, name string) url.Values {
	t.Helper()
	f := one(t, doc, "data-form", name)
	v := url.Values{}
	for _, in := range findAll(f, byTag("input")) {
		n, _ := attr(in, "name")
		val, _ := attr(in, "value")
		v.Set(n, val)
	}
	return v
}

// The page of an ended session shows its readers the release status, the deadline and the staged
// artifacts. A human who took part gets the forms to release and to object; an agent and a tenant
// admin who did not take part read without forms. A member who did not take part sees nothing
// before the release. The forms object, release with a refusal because of quotes, and release
// anyway: the refused command again, with the token. After the release, every member sees the released artifacts, who released
// them, and their authors, but not the transcript.
func TestPortalRelease(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	dora := m.register("Dora", kHuman).ParticipantID // a member who never joined
	sentence := "Ben will leave the company in March."
	m.say(ada, "We meet on Monday.")
	secret := m.say(ada, sentence)
	if err := m.markOff(ada, secret.GetId()); err != nil {
		t.Fatal(err)
	}
	m.stageDocument(ada, "documents/plan.md", "# Plan\n\nWe meet on Monday.\n\n"+sentence+"\n")
	m.stageDocument(bot, "documents/bot.md", "# Bot\n\nWritten by the bot.\n")
	m.end(ada)
	page := m.srv.URL + "/portal/t/" + contracttest.DefaultTenant + "/s/" + url.PathEscape(m.sid)

	// A reader who took part: status, deadline, staged artifacts and both forms.
	b, _, _ := openPortal(t, m.srv, m.a(ada), contracttest.DefaultTenant)
	res, doc, _ := portalPage(t, b, page)
	if res.StatusCode != http.StatusOK || text(one(t, doc, "data-release-state", "")) != "waiting" || len(findAll(doc, withAttr("data-deadline", ""))) != 1 {
		t.Fatalf("release status for a reader: %d\n%s", res.StatusCode, text(doc))
	}
	plan := one(t, doc, "data-artifact", "documents/plan.md")
	if _, staged := attr(plan, "data-staged"); !staged || !strings.Contains(text(plan), "We meet on Monday.") {
		t.Errorf("staged document: %q", text(plan))
	}
	one(t, doc, "data-artifact", "documents/bot.md")
	formOf(t, doc, "release")
	object := formOf(t, doc, "object")

	// An agent and the tenant admin read the staged artifacts without forms.
	for _, a := range []*contracttest.Actor{m.a(bot), m.reader} {
		ab, _, _ := openPortal(t, m.srv, a, contracttest.DefaultTenant)
		res, doc, _ := portalPage(t, ab, page)
		if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-artifact", "documents/plan.md"))) != 1 ||
			len(findAll(doc, withAttr("data-form", ""))) != 0 {
			t.Errorf("page for %s: status %d\n%s", a.ParticipantID, res.StatusCode, text(doc))
		}
	}
	// A member who did not take part: the session does not exist before its release.
	db, _, _ := openPortal(t, m.srv, m.a(dora), contracttest.DefaultTenant)
	if res, _, body := portalPage(t, db, page); res.StatusCode != http.StatusNotFound || strings.Contains(body, "documents/plan.md") {
		t.Fatalf("page for a member who did not take part: status %d", res.StatusCode)
	}

	// Object: the session is held, and only the release form is left.
	res, body := post(t, b, page+"/object", object)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || text(one(t, doc, "data-release-state", "")) != "held" || len(findAll(doc, withAttr("data-notice", ""))) != 1 ||
		len(findAll(doc, withAttr("data-form", "object"))) != 0 {
		t.Fatalf("after the objection: status %d\n%s", res.StatusCode, text(doc))
	}
	// The same form again has no second effect.
	if res, _ := post(t, b, page+"/object", object); res.StatusCode != http.StatusOK {
		t.Errorf("the objection form sent twice: status %d", res.StatusCode)
	}

	// Release: refused because of the quote. The page names the place, never the sentence.
	refused := formOf(t, doc, "release")
	res, body = post(t, b, page+"/release", refused)
	doc = parseDoc(t, body)
	quotes := one(t, doc, "data-quotes", "")
	if res.StatusCode != http.StatusConflict || len(findAll(quotes, withAttr("data-quote", "documents/plan.md:5"))) != 1 ||
		strings.Contains(strings.ToLower(text(quotes)), "leave the company") {
		t.Fatalf("release with a quote: status %d\n%s", res.StatusCode, text(doc))
	}
	if m.released(ada) != nil {
		t.Fatal("a refused release released the session")
	}
	anyway := formOf(t, doc, "release-anyway")
	if anyway.Get("accept_quotes") == "" {
		t.Fatalf("the form to release anyway carries no token: %v", anyway)
	}
	// It sends the refused command again: the hub did not remember it as done.
	if anyway.Get("command_id") == "" || anyway.Get("command_id") != refused.Get("command_id") {
		t.Errorf("the form to release anyway has the command %q, want the refused command %q", anyway.Get("command_id"), refused.Get("command_id"))
	}
	res, body = post(t, b, page+"/release", anyway)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || text(one(t, doc, "data-release-state", "")) != "released" || len(findAll(doc, withAttr("data-form", ""))) != 0 {
		t.Fatalf("release anyway: status %d\n%s", res.StatusCode, text(doc))
	}
	if ev := m.released(ada); ev == nil || ev.GetActorParticipantId() != ada || ev.GetType() != events.SessionReleased {
		t.Fatalf("release event %v, want one by Ada", ev)
	}

	// Every member sees the released artifacts, who released them and their authors.
	res, doc, body = portalPage(t, db, page)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-limited", ""))) != 1 ||
		text(one(t, doc, "data-released-by", "")) != "Ada" || text(one(t, doc, "data-authors", "")) != "Bot" {
		t.Fatalf("released session for a member: status %d\n%s", res.StatusCode, text(doc))
	}
	released := one(t, doc, "data-artifact", "documents/bot.md")
	if _, staged := attr(released, "data-staged"); staged || !strings.Contains(text(released), "Written by the bot.") {
		t.Errorf("released document: %q", text(released))
	}
	if len(findAll(doc, withAttr("data-utterance", ""))) != 0 || strings.Contains(body, "We meet on Monday.\n</span>") ||
		len(findAll(doc, withAttr("data-form", ""))) != 0 {
		t.Errorf("a member who did not take part sees more than the release:\n%s", text(doc))
	}
}

// Issue #220: the hub dies after it staged a document and before its stub lands, and a participant
// pushes their own file at that path while the hub is down. The release keeps the pushed file and
// releases the rest of the session. The staged document stays in the staging area: the readers of
// the session see it on the page as not released, because the path is taken, and everyone else
// reads the pushed file.
func TestPortalShowsArtifactNotReleased(t *testing.T) {
	args := []string{"-release-grace-period", "1h"}
	m := newMeetingArgs(t, args...)
	ada := m.join("Ada", kHuman)
	dora := m.register("Dora", kHuman).ParticipantID // a member who never joined
	m.say(ada, "We meet on Monday.")
	m.stageDocument(ada, "documents/taken.md", "# Taken\n\nThe agent's version.\n")
	m.stageDocument(ada, "documents/free.md", "# Free\n\nReleased as usual.\n")
	m.end(ada)

	// The crash: the stub of documents/taken.md is not in the artifact repository, and Ben's file
	// lies at its path when the hub starts again.
	m.srv.Kill()
	contracttest.Edit(t, m.srv.ArchiveDir(), "Ben's own file", func(work string) {
		contracttest.WriteFile(t, work, "documents/taken.md", "# Ben\n\nBen's own file.\n")
	})
	m.start(nil, args...)

	if _, err := m.releaseBy(ada); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got := contracttest.Git(t, m.srv.ArchiveDir(), "show", "main:documents/taken.md"); got != "# Ben\n\nBen's own file.\n" {
		t.Errorf("the release replaced the pushed file: %q", got)
	}
	if got := contracttest.Git(t, m.srv.ArchiveDir(), "show", "main:documents/free.md"); got != "# Free\n\nReleased as usual.\n" {
		t.Errorf("released document: %q", got)
	}
	if got := contracttest.ReleaseCommits(t, m.srv.ArchiveDir(), m.sid); len(got) != 1 {
		t.Errorf("release commits %v, want one", got)
	}
	rel, err := m.s(ada).GetRelease(m.ctx(), &sessioncommandsv1.GetReleaseRequest{SessionId: m.sid})
	if err != nil {
		t.Fatal(err)
	}
	if st := rel.GetStatus().GetState(); st != sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED ||
		!slices.Equal(rel.GetUnreleased(), []string{"documents/taken.md"}) || !slices.Equal(rel.GetCommit().GetPaths(), []string{"documents/free.md"}) {
		t.Fatalf("release %v", rel)
	}

	page := m.srv.URL + "/portal/t/" + contracttest.DefaultTenant + "/s/" + url.PathEscape(m.sid)
	b, _, _ := openPortal(t, m.srv, m.a(ada), contracttest.DefaultTenant)
	res, doc, _ := portalPage(t, b, page)
	if res.StatusCode != http.StatusOK || text(one(t, doc, "data-release-state", "")) != "released" {
		t.Fatalf("page of the released session: %d\n%s", res.StatusCode, text(doc))
	}
	taken := one(t, doc, "data-artifact", "documents/taken.md")
	if _, kept := attr(taken, "data-not-released"); !kept || !strings.Contains(text(taken), "not released: the path is taken") ||
		!strings.Contains(text(taken), "The agent's version.") {
		t.Errorf("the document that was not released: %q", text(taken))
	}
	free := one(t, doc, "data-artifact", "documents/free.md")
	if _, kept := attr(free, "data-not-released"); kept || !strings.Contains(text(free), "Released as usual.") {
		t.Errorf("the released document: %q", text(free))
	}
	// A member who did not take part sees the release, and no staged artifact.
	db, _, _ := openPortal(t, m.srv, m.a(dora), contracttest.DefaultTenant)
	res, doc, body := portalPage(t, db, page)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-artifact", "documents/taken.md"))) != 0 ||
		strings.Contains(body, "The agent's version.") {
		t.Errorf("page for a member who did not take part: %d\n%s", res.StatusCode, text(doc))
	}
	if f, err := readFile(t, m.a(dora), "documents/taken.md"); err != nil || f.GetStaged() || string(f.GetContent()) != "# Ben\n\nBen's own file.\n" {
		t.Errorf("read of the pushed file by a member: %v, %v", f, err)
	}
}

// An ended session that nobody released can be trimmed and deleted in the portal
// (dec-session-trim-delete). The trim cuts the transcript and the stored minutes at the chosen
// sentence; the delete takes the session out of the list of the tenant until it is restored.
// An agent gets no forms.
func TestPortalTrimAndDelete(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	bot := m.join("Bot", kAgent)
	first := m.say(ada, "The meeting is cancelled.")
	setMinutes := func(text string) {
		if _, err := m.s(ada).UpdateMinutes(m.ctx(), &sessioncommandsv1.UpdateMinutesRequest{SessionId: m.sid,
			CommandId: coreapi.NewCommandID(), ParticipantId: ada, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	setMinutes("# Minutes\n\nCancelled.")
	m.say(ada, "Everybody forgot the meeting.")
	setMinutes("# Minutes\n\nA long discussion that never happened.")
	m.end(ada)
	tenant := contracttest.DefaultTenant
	page := m.srv.URL + "/portal/t/" + tenant + "/s/" + url.PathEscape(m.sid)
	b, _, _ := openPortal(t, m.srv, m.a(ada), tenant)

	// An agent gets no forms.
	ab, _, _ := openPortal(t, m.srv, m.a(bot), tenant)
	if _, doc, _ := portalPage(t, ab, page); len(findAll(doc, withAttr("data-form", "trim"))) != 0 {
		t.Error("an agent got the trim form")
	}

	// Trim after the first sentence.
	_, doc, _ := portalPage(t, b, page)
	trim := formOf(t, doc, "trim")
	// The minutes after the first sentence (event first+1) belong to it.
	trim.Set("last_event_id", strconv.Itoa(int(first.GetId())+1))
	res, body := post(t, b, page+"/trim", trim)
	doc = parseDoc(t, body)
	if res.StatusCode != http.StatusOK || len(findAll(doc, withAttr("data-trimmed", ""))) != 1 {
		t.Fatalf("after the trim: status %d\n%s", res.StatusCode, text(doc))
	}
	if len(findAll(doc, withAttr("data-utterance", ""))) != 1 || strings.Contains(body, "forgot the meeting") {
		t.Errorf("the transcript shows what the trim cut off\n%s", text(doc))
	}
	if minutes := text(one(t, doc, "data-minutes", "")); !strings.Contains(minutes, "Cancelled.") || strings.Contains(minutes, "never happened") {
		t.Errorf("the minutes after the trim: %q", minutes)
	}

	// Delete: the session leaves the list, and comes back with "show deleted".
	list := m.srv.URL + "/portal/t/" + tenant + "/"
	_, doc, _ = portalPage(t, b, page)
	if res, _ := post(t, b, page+"/delete", formOf(t, doc, "delete")); res.StatusCode != http.StatusOK {
		t.Fatalf("delete: status %d", res.StatusCode)
	}
	if _, doc, _ := portalPage(t, b, list); len(findAll(doc, withAttr("data-session", m.sid))) != 0 {
		t.Error("a deleted session is in the list")
	}
	if _, doc, _ := portalPage(t, b, list+"?deleted=1"); len(findAll(doc, withAttr("data-deleted", ""))) == 0 {
		t.Error("the list with deleted sessions does not show it")
	}
	_, doc, _ = portalPage(t, b, page)
	if res, _ := post(t, b, page+"/restore", formOf(t, doc, "restore")); res.StatusCode != http.StatusOK {
		t.Fatalf("restore: status %d", res.StatusCode)
	}
	if _, doc, _ := portalPage(t, b, list); len(findAll(doc, withAttr("data-session", m.sid))) != 1 {
		t.Error("a restored session is not in the list")
	}
}
