package contract

// Contract tests of the dev preview (office-hub preview): the pages of the stage and the portal
// with fixture data. The preview starts no hub. The tests start the real program, once to write
// the pages as files (as the CI does) and once to serve them. A page that does not render makes
// the program exit with an error, so a broken template cannot ship. No test depends on timing.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

// previewAnchors are the fixture pages that the preview writes, each with the markers that its
// page must hold. The markers (data-* attributes) are the ones that the contract tests of the
// stage and the portal read, so the fixtures keep the shape of the real pages.
var previewAnchors = map[string][]string{
	"stage-live.html":                 {"data-participant=", "data-hand=", `data-activity="working"`, `data-artifact="slides/8"`, "<iframe", "data-utterance="},
	"stage-live-note.html":            {"data-participant=", `data-artifact="data/report.csv"`},
	"stage-agent-unspoken.html":       {`data-activity="unspoken"`, `data-kind="agent"`},
	"stage-empty.html":                {"data-participant=", `data-placeholder="minutes"`},
	"stage-imported.html":             {`data-imported="complete"`, "data-utterance="},
	"stage-importing.html":            {`data-imported="importing"`},
	"stage-ended.html":                {`data-status="ended"`, "data-minutes"},
	"stage-stopped.html":              {`data-status="stopped"`},
	"stage-error.html":                {`data-status="401"`},
	"stage-error-other-session.html":  {`data-status="403"`},
	"portal-home.html":                {"data-nav=", "data-tenant="},
	"portal-home-empty.html":          {`data-empty="tenants"`},
	"portal-tenant.html":              {"data-session=", "data-running", "data-imported", "data-deleted", "data-participant=", "data-role"},
	"portal-tenant-empty.html":        {`data-empty="sessions"`},
	"portal-session-open.html":        {`data-release="open"`, "data-minutes"},
	"portal-session-waiting.html":     {`data-release="waiting"`, "data-deadline", `data-form="release"`, `data-form="object"`},
	"portal-session-held.html":        {`data-release="held"`},
	"portal-session-released.html":    {`data-release="released"`, "data-commit=", "data-released-by"},
	"portal-session-limited.html":     {"data-limited", `data-release="released"`},
	"portal-session-full.html":        {"data-minutes", "data-decision=", "data-task=", "data-note=", "data-utterance=", `data-form="trim"`, "data-not-released", "data-notice"},
	"portal-session-running.html":     {"data-running-minutes", "data-running"},
	"portal-session-imported.html":    {"data-origin"},
	"portal-session-deleted.html":     {`data-form="restore"`},
	"portal-session-quotes.html":      {"data-quotes", `data-form="release-anyway"`, "data-error"},
	"portal-wiki-index.html":          {"data-tags", "data-tag-filter=", "data-entry=", "data-wikibar"},
	"portal-wiki-tag.html":            {`data-filter="customers"`},
	"portal-wiki-empty.html":          {`data-empty="wiki"`},
	"portal-wiki-artifact.html":       {"data-kind", "data-status", "data-owner", "data-review=", "data-front-matter", "data-content", "data-link=", "data-broken-link", "data-last-change", "data-released"},
	"portal-wiki-source.html":         {`data-empty="cited-by"`},
	"portal-wiki-missing-file.html":   {`data-empty="content"`},
	"portal-wiki-search.html":         {"data-entry="},
	"portal-wiki-search-none.html":    {`data-empty="hits"`},
	"portal-wiki-search-form.html":    {`data-empty="query"`},
	"portal-wiki-search-error.html":   {"data-error"},
	"portal-wiki-broken.html":         {"data-broken="},
	"portal-wiki-broken-none.html":    {`data-empty="broken"`},
	"portal-wiki-queue.html":          {"data-entry="},
	"portal-wiki-queue-empty.html":    {`data-empty="queue"`},
	"portal-members.html":             {"data-member=", "data-nav="},
	"portal-member-tenant.html":       {"data-nav=", "data-session="},
	"portal-admin-keys.html":          {"data-key=", "data-participant=", "data-notice"},
	"portal-admin-keys-empty.html":    {"data-nav="},
	"portal-admin-key-new.html":       {`data-form="register"`, "data-error"},
	"portal-admin-key-revoke.html":    {`data-confirm="revoke"`, `data-form="revoke"`},
	"portal-admin-tenants.html":       {"data-tenant="},
	"portal-admin-tenants-empty.html": {"data-nav="},
	"portal-admin-admins.html":        {"data-key="},
	"portal-error.html":               {`id="error"`},
	"portal-error-not-found.html":     {`id="error"`},
}

// previewEnv keeps the preview away from the settings of the developer.
var previewEnv = []string{"HOME=" + os.TempDir()}

// TestPreviewWritesEveryFixturePage: "preview -out" exits with 0, writes every fixture page as a
// non-empty HTML file with the markers of the real page, and an index that links every file.
func TestPreviewWritesEveryFixturePage(t *testing.T) {
	out := filepath.Join(t.TempDir(), "ui-preview")
	stdout, stderr, code := contracttest.Run(t, "office-hub", previewEnv, "preview", "-out", out)
	if code != 0 {
		t.Fatalf("office-hub preview -out: exit %d\n%s\n%s", code, stdout, stderr)
	}
	index := readPreviewFile(t, filepath.Join(out, "index.html"))
	for name, anchors := range previewAnchors {
		page := readPreviewFile(t, filepath.Join(out, name))
		if !strings.Contains(page, "<html") || !strings.Contains(page, "</html>") {
			t.Errorf("%s is not an HTML document", name)
		}
		for _, a := range anchors {
			if !strings.Contains(page, a) {
				t.Errorf("%s has no %s", name, a)
			}
		}
		if !strings.Contains(index, `href="`+name+`"`) {
			t.Errorf("index.html does not link %s", name)
		}
	}
	// Every file in the folder is expected: a new fixture comes with its markers here.
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, known := previewAnchors[e.Name()]; !known && e.Name() != "index.html" {
			t.Errorf("%s is written but has no markers in this test", e.Name())
		}
	}
	// A page of the portal links to the other pages by file, so the folder works as it is.
	if tenant := readPreviewFile(t, filepath.Join(out, "portal-tenant.html")); !strings.Contains(tenant, `href="portal-wiki-index.html"`) {
		t.Errorf("the navigation of portal-tenant.html does not link the wiki page by file")
	}
	// The pages are static: no reload script, no stream.
	for _, name := range []string{"stage-live.html", "portal-tenant.html", "index.html"} {
		if strings.Contains(readPreviewFile(t, filepath.Join(out, name)), "__version") {
			t.Errorf("%s has the reload script of the live preview", name)
		}
	}
}

func readPreviewFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) == 0 {
		t.Fatalf("%s is empty", p)
	}
	return string(b)
}

// TestPreviewFailsOnBrokenTemplate: a template that does not render makes "preview -out" exit with
// an error that names the page, so the CI job fails.
func TestPreviewFailsOnBrokenTemplate(t *testing.T) {
	src := t.TempDir()
	for _, dir := range []string{"hub/stage/web/embed", "hub/portal/web/embed"} {
		copyDir(t, filepath.Join("..", "..", filepath.FromSlash(dir)), filepath.Join(src, filepath.FromSlash(dir)))
	}
	// A field that no page has.
	broken := filepath.Join(src, "hub", "portal", "web", "embed", "error.html")
	b, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte(strings.Replace(string(b), "{{.Message}}", "{{.Message.NoSuchField}}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ui-preview")
	_, stderr, code := contracttest.Run(t, "office-hub", previewEnv, "preview", "-src", src, "-out", out)
	if code == 0 {
		t.Fatalf("office-hub preview -out with a broken template: exit 0, want an error")
	}
	if !strings.Contains(stderr, "portal-error.html") {
		t.Errorf("the error does not name the page that failed:\n%s", stderr)
	}
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(to, 0o755); err != nil {
		t.Fatal(err)
	}
	es, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

var previewListening = regexp.MustCompile(`preview listening on (http://\S+)`)

// TestPreviewServesStageAndPortal: "preview" serves a page of the stage and a page of the portal
// at the addresses of the real pages, and the start page, from the templates on disk, with the
// reload script. It listens on a loopback address only.
func TestPreviewServesStageAndPortal(t *testing.T) {
	p := contracttest.Start(t, "office-hub", previewEnv, "preview", "-addr", "127.0.0.1:0", "-src", filepath.Join("..", ".."))
	base := p.WaitStderr(t, previewListening, 20*time.Second)
	get := func(path string) (*http.Response, string) {
		t.Helper()
		res, err := http.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	for _, c := range []struct{ path, want string }{
		{"/", `data-preview-page="stage-live.html"`},
		{"/stage/weekly-planning", `data-participant="ada"`},
		{"/portal/t/acme/", `data-session="weekly-planning"`},
		{"/portal/t/acme/wiki", "data-tag-filter="},
		{"/portal/t/acme/wiki?tag=customers", `data-filter="customers"`},
		{"/portal-admin-keys.html", "data-key="},
	} {
		res, body := get(c.path)
		if res.StatusCode != http.StatusOK || !strings.Contains(body, c.want) {
			t.Errorf("GET %s: status %d, want 200 with %s", c.path, res.StatusCode, c.want)
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: content type %q", c.path, ct)
		}
		if !strings.Contains(body, "/__version") {
			t.Errorf("GET %s: no reload script", c.path)
		}
	}
	if res, body := get("/__version"); res.StatusCode != http.StatusOK || strings.TrimSpace(body) == "" {
		t.Errorf("GET /__version: status %d, body %q", res.StatusCode, body)
	}
	if res, _ := get("/stage/nothing-here-that-is-a-fixture-page/at/all"); res.StatusCode != http.StatusNotFound {
		t.Errorf("an address that is no fixture page: status %d, want 404", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, base+"/", strings.NewReader("x"))
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %v, %v, want 405", res, err)
	}
}

// TestPreviewRefusesAddressOutsideTheComputer: the preview has no login, so it refuses an address
// that other computers can reach.
func TestPreviewRefusesAddressOutsideTheComputer(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8099", ":8099", "192.0.2.1:8099"} {
		_, stderr, code := contracttest.Run(t, "office-hub", previewEnv, "preview", "-addr", addr)
		if code == 0 || !strings.Contains(stderr, "loopback") {
			t.Errorf("preview -addr %s: exit %d, stderr %q; want an error that names the loopback rule", addr, code, stderr)
		}
	}
}

// TestPreviewIsNotPartOfTheHub: a normal hub run does not serve the preview pages or its version.
func TestPreviewIsNotPartOfTheHub(t *testing.T) {
	srv := contracttest.StartHub(t, nil)
	hc := srv.HTTPClient(t)
	for _, p := range []string{"/__version", "/index.html", "/stage-live.html", "/portal-home.html"} {
		res, _ := fetch(t, hc, srv.URL+p)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s on the hub: status %d, want 404", p, res.StatusCode)
		}
	}
}
