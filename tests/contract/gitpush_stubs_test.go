package contract

// Contract tests of the push rules for the stubs of staged artifacts (if-git-access,
// concept-session-release, dec-staging-repository): the real git command line tool against the
// real office-hub. A staged artifact waits in the staging area of its session, which Git access
// never serves; the artifact repository holds its stub. No push changes or removes a stub, and no
// push writes one. A push next to a stub passes, and the released file can be changed again.

import (
	"strings"
	"testing"

	recordscheck "github.com/mrsimpson/agentic-office/hub/records/check"
	"github.com/mrsimpson/agentic-office/tests/contracttest"
)

func TestGitPushProtectsStubs(t *testing.T) {
	m := newMeetingArgs(t, "-release-grace-period", "1h")
	ada := m.join("Ada", kHuman)
	const tenant = contracttest.DefaultTenant

	note := m.note(ada, m.sid, "Ben leaves in March, a staged secret.")
	notePath := recordscheck.FilePath(recordscheck.SchemaNote, note.GetId())
	m.stageDocument(ada, "documents/plan.md", "# Plan\n\nThe staged plan is a secret.\n")
	if !isDocumentStub(m.artifact("documents/plan.md")) || m.artifact(notePath) != string(recordscheck.Stub(recordscheck.SchemaNote)) {
		t.Fatalf("the artifact repository does not hold the stubs:\n%s\n%s", m.artifact("documents/plan.md"), m.artifact(notePath))
	}

	// A clone holds the stubs and nothing that is staged.
	p := newPusher(t, m.srv, m.a(ada), "Ada", tenant)
	objects := contracttest.Git(t, p.dir, "cat-file", "--batch-all-objects", "--batch")
	for _, secret := range []string{"staged secret", "staged plan"} {
		if strings.Contains(objects, secret) {
			t.Errorf("the clone holds staged content %q", secret)
		}
	}
	if got := contracttest.Git(t, p.dir, "show", "HEAD:"+notePath); got != string(recordscheck.Stub(recordscheck.SchemaNote)) {
		t.Errorf("the clone does not hold the stub of the note: %q", got)
	}

	reset := func() {
		t.Helper()
		p.mustGit("reset", "-q", "--hard", "origin/main")
	}
	const stubRule = "is the stub of an artifact that waits for the release of its session"

	// A push that changes a stub is refused.
	before := mainOf(t, m.srv, tenant)
	p.commitFile(notePath, "---\nschema: office.note.v1\nid: 1\n---\nMy own note.\n")
	out, code := p.push()
	refused(t, m.srv, tenant, before, out, code, notePath+" "+stubRule)
	reset()
	p.commitFile("documents/plan.md", "# My plan\n")
	out, code = p.push()
	refused(t, m.srv, tenant, before, out, code, "documents/plan.md "+stubRule)
	reset()

	// A push that removes a stub is refused, also together with a valid change.
	p.mustGit("rm", "-q", "documents/plan.md")
	p.write("documents/next.md", "# Next\n")
	p.commit("remove the stub")
	out, code = p.push()
	refused(t, m.srv, tenant, before, out, code, "documents/plan.md "+stubRule)
	reset()

	// A push whose commits change a stub and then restore it changes no stub: only the final
	// state counts.
	p.commitFile("documents/plan.md", "# My plan\n")
	p.mustGit("checkout", "origin/main", "--", "documents/plan.md")
	p.commit("restore the stub")
	p.commitFile("documents/next.md", "# Next\n")
	p.mustPush()

	// No push writes the content of a stub.
	before = mainOf(t, m.srv, tenant)
	p.commitFile("documents/fake.md", m.artifact("documents/plan.md"))
	out, code = p.push()
	refused(t, m.srv, tenant, before, out, code, "documents/fake.md has the content of the stub of an artifact")
	reset()

	// A push next to a stub passes: a file in the same directory, and a document that names the
	// schema of documents. The stubs stay as they were.
	p.commitFile("documents/beside.md", "# Beside the plan\n")
	p.commitFile("documents/named.md", "---\nschema: office.document.v1\ntitle: Named\n---\n# Named\n")
	p.commitFile("notes/beside.md", "A page next to the note.\n")
	p.mustPush()
	if !isDocumentStub(m.artifact("documents/plan.md")) || m.artifact(notePath) != string(recordscheck.Stub(recordscheck.SchemaNote)) {
		t.Error("a push next to the stubs changed them")
	}
	if m.artifact("documents/named.md") == "" {
		t.Error("the document that names its schema is not in the artifact repository")
	}

	// A document that names its schema is checked by the documents part.
	before = mainOf(t, m.srv, tenant)
	p.commitFile("pages/named.md", "---\nschema: office.document.v1\n---\n# Named\n")
	out, code = p.push()
	refused(t, m.srv, tenant, before, out, code, "pages/named.md: a file of the schema office.document.v1 lies in documents/")
	reset()

	// After the release the file is no stub any more, and a push changes it.
	m.endWithStatus(ada)
	if _, err := m.releaseBy(ada); err != nil {
		t.Fatal(err)
	}
	if !m.releasedDoc("documents/plan.md") {
		t.Fatal("the release did not move the plan")
	}
	p.mustGit("pull", "-q", "--rebase", "origin", "main")
	p.commitFile("documents/plan.md", "# Plan\n\nChanged after the release.\n")
	p.mustGit("rm", "-q", notePath)
	p.commit("remove the released note")
	p.mustPush()
	if got := m.artifact("documents/plan.md"); got != "# Plan\n\nChanged after the release.\n" {
		t.Errorf("the plan after the push: %q", got)
	}
	if m.artifact(notePath) != "" {
		t.Error("the released note was not removed")
	}
}
