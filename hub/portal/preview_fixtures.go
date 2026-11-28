package portal

// Fixtures of the dev preview: the data of fictional pages, in the types that the real handlers
// give to the page template. Plain Go data; no file is read.

import (
	"strings"

	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
)

// previewPage is one fixture page. active is the address of the page and decides which link of
// the navigation is marked.
type previewPage struct {
	name, title, active, tenant string
	me                          *user
	data                        map[string]any
}

type previewError struct{ name, title, heading, message string }

func previewErrors() []previewError {
	return []previewError{
		{"portal-error.html", "Error page: the portal link is not valid", "Portal link not valid",
			"The link is unknown, was used before or has expired. " + askForLink},
		{"portal-error-not-found.html", "Error page: no such tenant", "Not found", "There is no such tenant for you."},
	}
}

const (
	tenantID    = "acme"
	csrfPreview = "preview-token"
)

// admin is a system admin who is also the tenant admin of acme and a member of globex.
var admin = &user{ParticipantID: "ada", Name: "Ada Lovelace", SystemAdmin: true, Human: true,
	Memberships: []*identityv1.TenantMembership{{TenantId: tenantID, TenantAdmin: true}, {TenantId: "globex"}}}

// member is a member of acme without administration.
var member = &user{ParticipantID: "kai", Name: "Kai", Human: true,
	Memberships: []*identityv1.TenantMembership{{TenantId: tenantID}}}

// loner is a user without a tenant.
var loner = &user{ParticipantID: "newcomer", Name: "Newcomer", Human: true}

func chips(cs ...participantChip) []participantChip { return cs }

func human(id, name string) participantChip { return participantChip{ParticipantId: id, Name: name} }
func agent(id, name string) participantChip {
	return participantChip{ParticipantId: id, Name: name, Agent: true}
}

const longSessionID = "2026-11-27-quarterly-planning-with-the-reporting-team-and-all-of-its-stakeholders-part-two"

func sessionList() []sessionRow {
	row := func(id, started, ended string, cs ...participantChip) sessionRow {
		return sessionRow{SessionId: id, Href: web.SessionPath(tenantID, id), Started: started, Ended: ended, Running: ended == "", Participants: cs}
	}
	running := row("weekly-planning", "2026-11-27 09:30", "", human("ada", "Ada Lovelace"), human("grace", "Grace Hopper-von Neumann of the Alexandria Institute"),
		agent("minutes-keeper", "Minutes keeper"), human("kai", "Kai"), human("noor", "Noor Al-Sayed"), agent("researcher", "Researcher"))
	imported := row("customer-interview", "2026-11-22 14:00", "2026-11-22 14:45", human("customer", "Customer contact"), human("interviewer", "Interviewer"))
	imported.Imported = true
	deleted := row("old-experiment", "2026-10-02 10:00", "2026-10-02 10:20", human("ada", "Ada Lovelace"))
	deleted.Deleted = true
	trimmed := row("weekly-planning-ended", "2026-11-20 09:30", "2026-11-20 10:30", human("ada", "Ada Lovelace"), agent("minutes-keeper", "Minutes keeper"))
	trimmed.Trimmed = true
	return []sessionRow{running, row(longSessionID, "2026-11-26 13:00", "2026-11-26 15:10", human("ada", "Ada Lovelace"), human("margaret", "Margaret Hamilton")),
		imported, deleted, trimmed}
}

func previewPages() []previewPage {
	var ps []previewPage
	add := func(p previewPage) { ps = append(ps, p) }
	tenantPage := func(name, title string, rows []sessionRow, deleted bool) previewPage {
		return previewPage{name: name, title: title, active: web.TenantPath(tenantID), tenant: tenantID, me: admin,
			data: map[string]any{"Title": tenantID, "Section": "Tenant", "Mode": "tenant", "Tenant": tenantID, "Role": "tenant admin",
				"Sessions": rows, "ShowDeleted": deleted, "TenantHref": web.TenantPath(tenantID)}}
	}

	add(previewPage{name: "portal-home.html", title: "Start page: the tenants of a system admin", active: web.HomePath, me: admin,
		data: map[string]any{"Title": "Your tenants", "Section": "Portal", "Mode": "home", "SystemAdmin": true,
			"Tenants": []tenantRow{{TenantId: tenantID, Role: "tenant admin", Href: web.TenantPath(tenantID)}, {TenantId: "globex", Role: "member", Href: web.TenantPath("globex")}}}})
	add(previewPage{name: "portal-home-empty.html", title: "Start page: a user without a tenant", active: web.HomePath, me: loner,
		data: map[string]any{"Title": "Your tenants", "Section": "Portal", "Mode": "home"}})

	add(tenantPage("portal-tenant.html", "Tenant: sessions that run, were imported, trimmed and have a long name", sessionList(), false))
	empty := tenantPage("portal-tenant-empty.html", "Tenant without a session", nil, false)
	empty.active = web.TenantPath("globex")
	empty.tenant = "globex"
	empty.data["Title"], empty.data["Tenant"], empty.data["Role"], empty.data["TenantHref"] = "globex", "globex", "member", web.TenantPath("globex")
	add(empty)

	// The session page in every release state.
	base := func(id string) sessionView {
		return sessionView{SessionId: id, Started: "2026-11-20 09:30", Ended: "2026-11-20 10:30",
			Participants: chips(human("ada", "Ada Lovelace"), agent("minutes-keeper", "Minutes keeper")),
			Minutes:      markdownHTML("# Weekly planning\n\nWe agreed to ship the preview first."),
			Lines: []lineView{{EventId: 3, Time: "09:31:02", Speaker: "Ada Lovelace", SpeakerId: "ada", Text: "Let us start."},
				{EventId: 4, Time: "09:31:40", Speaker: "Minutes keeper", SpeakerId: "minutes-keeper", Agent: true, Text: "I take the minutes."}}}
	}
	release := func(id, state string) *releaseView {
		r := &releaseView{State: state, ReleasePath: web.ReleasePath(tenantID, id), ObjectPath: web.ObjectPath(tenantID, id),
			DiscardPath: web.DiscardPath(tenantID, id), CSRF: csrfPreview, ReleaseCommand: "c1", ObjectCommand: "c2",
			Artifacts: []artifactView{{Path: "minutes/000001.md", Content: "# Weekly planning\n\nWe agreed to ship the preview first.\n", Staged: state != "released"},
				{Path: "decisions/000001.yaml", Content: "text: Ship the preview first.\n", Staged: state != "released"}}}
		switch state {
		case "waiting":
			r.Waiting, r.MayRelease, r.Deadline = true, true, "2026-11-28 10:30"
		case "held":
			r.MayRelease = true
		case "open":
			r.MayRelease = true
		case "released":
			r.Commit = &commitView{ID: "3f2a9c1d7e5b4a6c8d9e0f1a2b3c4d5e6f708192", Short: "3f2a9c1d7e5b", Time: "2026-11-28 10:30",
				Authors: []string{"Ada Lovelace", "Minutes keeper"}}
		}
		return r
	}
	sessionPage := func(name, title, id string, v sessionView, notice, errMsg string) previewPage {
		v.Release.CSRF = csrfPreview
		return previewPage{name: name, title: title, active: web.TenantPath(tenantID), tenant: tenantID, me: admin,
			data: map[string]any{"Title": id, "Section": "Session", "Mode": "session", "Session": v, "Back": web.TenantPath(tenantID),
				"CSRF": csrfPreview, "Notice": notice, "Error": errMsg}}
	}
	for _, state := range []string{"open", "waiting", "held", "released"} {
		id := "release-" + state
		v := base(id)
		v.Release = release(id, state)
		if state == "open" {
			v.Running, v.Ended = true, ""
		}
		p := sessionPage("portal-session-"+state+".html", "Session page: release state "+state, id, v, "", "")
		p.active = web.SessionPath(tenantID, id)
		add(p)
	}
	limited := sessionView{SessionId: "release-limited", Limited: true, Release: release("release-limited", "released")}
	lp := sessionPage("portal-session-limited.html", "Session page: a member who did not take part sees only the release", "release-limited", limited, "", "")
	lp.active = web.SessionPath(tenantID, "release-limited")
	add(lp)

	// A full session: minutes, decisions, tasks, notes, transcript, trim and delete.
	full := base("weekly-planning-ended")
	full.Release = release("weekly-planning-ended", "waiting")
	full.Minutes = markdownHTML("# Weekly planning\n\n## Decisions\n\n- Ship the **stage redesign** behind a preview first.\n\n| Topic | Owner |\n|---|---|\n| Migration plan | Grace |\n")
	full.Decisions = []recordView{{Id: 1, Author: "ada", Time: "2026-11-20 09:50", Text: markdownHTML("Ship the **preview** first.")},
		{Id: 2, Author: "minutes-keeper", Time: "2026-11-20 10:05", Text: markdownHTML("Move the reporting database in two steps.\n\n1. Copy.\n2. Switch.")}}
	full.Notes = []recordView{{Id: 1, Author: "minutes-keeper", Time: "2026-11-20 10:10", Text: markdownHTML("The customer reads the minutes after a meeting."),
		Tags: []string{"customers", "research", "a-tag-with-a-rather-long-name-to-check-wrapping"}}}
	full.Tasks = []taskView{{Ref: "T-1", Title: "Review the migration plan", Status: "open", Assignee: "grace"},
		{Ref: "T-2", Title: "Write the wiki home", Status: "in progress", Assignee: "ada"}, {Ref: "T-3", Title: "Drop the old table", Status: "done", Assignee: "kai"},
		{Ref: "T-4", Title: "A cancelled task with a long title that goes on and on to check how the table cell wraps", Status: "cancelled"}}
	for i := 5; i < 40; i++ {
		full.Lines = append(full.Lines, lineView{EventId: uint32(i), Time: "09:40:00", Speaker: "Ada Lovelace", SpeakerId: "ada",
			Text: strings.Repeat("A sentence of the transcript. ", 1+i%4), Corrected: i%9 == 0})
	}
	full.Edit = &editView{Trimmed: true, TrimmedAfter: 20, EndId: 41, TrimPath: web.TrimPath(tenantID, "weekly-planning-ended"),
		DeletePath: web.DeletePath(tenantID, "weekly-planning-ended"), RestorePath: web.RestorePath(tenantID, "weekly-planning-ended"),
		TrimCommand: "t1", DeleteCommand: "t2", RestoreCommand: "t3",
		Choices: []choiceView{{EventId: 10, Label: "09:35:00 Ada Lovelace: A sentence of the transcript."}, {EventId: 20, Label: "09:40:00 Ada Lovelace: Another sentence", Selected: true}}}
	full.Release.Artifacts = append(full.Release.Artifacts, artifactView{Path: "notes/000003.md", Staged: true, NotReleased: true, Version: "v1", Command: "d1",
		Content: "A note whose path is taken."})
	fp := sessionPage("portal-session-full.html", "Session page: minutes, decisions, tasks, notes, transcript, trim and delete", "weekly-planning-ended", full,
		"The session is trimmed. Events after the chosen sentence no longer count.", "")
	fp.active = web.SessionPath(tenantID, "weekly-planning-ended")
	add(fp)

	running := base("weekly-planning")
	running.Running, running.Ended = true, ""
	running.Release = release("weekly-planning", "open")
	running.MinutesRunning = true
	rp := sessionPage("portal-session-running.html", "Session page: a running session shows the session as it was when opened", "weekly-planning", running, "", "")
	rp.active = web.SessionPath(tenantID, "weekly-planning")
	add(rp)

	imp := base("customer-interview")
	imp.Source, imp.ImportTitle, imp.Importer = "teams", "Customer interview, 22 November", "ada"
	imp.Release = release("customer-interview", "released")
	ip := sessionPage("portal-session-imported.html", "Session page: imported session", "customer-interview", imp, "", "")
	ip.active = web.SessionPath(tenantID, "customer-interview")
	add(ip)

	del := base("old-experiment")
	del.Release = release("old-experiment", "held")
	del.Edit = &editView{Deleted: true, EndId: 9, RestorePath: web.RestorePath(tenantID, "old-experiment"), RestoreCommand: "r1"}
	dp := sessionPage("portal-session-deleted.html", "Session page: deleted session", "old-experiment", del, "", "")
	dp.active = web.SessionPath(tenantID, "old-experiment")
	add(dp)

	quotes := base("quote-refusal")
	quotes.Release = release("quote-refusal", "waiting")
	quotes.Release.Quotes = []*sessioncommandsv1.Quote{{Path: "notes/000001.md", Line: 4, UtteranceEventId: 12}, {Path: "minutes/000001.md", Line: 9, UtteranceEventId: 15}}
	quotes.Release.QuoteToken, quotes.Release.AnywayCommand = "quote-token", "c1"
	qp := sessionPage("portal-session-quotes.html", "Session page: the hub refused the release because of quotes", "quote-refusal", quotes, "", "The hub refused the command.")
	qp.active = web.SessionPath(tenantID, "quote-refusal")
	add(qp)

	// The wiki.
	tag := func(t string, n uint32, on bool) tagView {
		return tagView{Tag: t, Count: n, Href: web.WikiTagPath(tenantID, t), On: on}
	}
	entry := func(p, title, summary, schema string, source bool, tags ...string) entryView {
		e := entryView{Path: p, Href: web.WikiArtifactPath(tenantID, p), Title: title, Summary: summary, Schema: schema, Source: source}
		for _, t := range tags {
			e.Tags = append(e.Tags, tag(t, 0, false))
		}
		return e
	}
	newWiki := func(page string) *wikiView {
		return &wikiView{Page: page, IndexHref: web.WikiPath(tenantID), SearchHref: web.WikiSearchPath(tenantID),
			BrokenHref: web.WikiBrokenPath(tenantID), QueueHref: web.WikiQueuePath(tenantID)}
	}
	wikiPage := func(name, title, active string, v *wikiView, errMsg string) previewPage {
		return previewPage{name: name, title: title, active: active, tenant: tenantID, me: admin,
			data: map[string]any{"Title": title, "Section": tenantID + " · Wiki", "Mode": "wiki", "Wiki": v, "Error": errMsg}}
	}
	var manyTags []tagView
	for _, t := range strings.Fields("architecture customers decisions database migration planning research release roadmap security slides stage portal wiki onboarding operations performance privacy testing a-tag-with-a-rather-long-name-to-check-wrapping") {
		manyTags = append(manyTags, tag(t, uint32(len(t)), false))
	}
	docs := []entryView{
		entry("documents/roadmap.md", "Roadmap", "What the team builds next, and why.", "office.document.v1", false, "roadmap", "planning"),
		entry("documents/a-document-with-a-very-long-title/the-title-is-long-and-so-is-the-path.md", "A document with a very long title that must wrap in its table cell without breaking the page", "A summary that is long, too. "+strings.Repeat("It goes on. ", 12), "office.document.v1", false, "architecture", "database", "migration", "a-tag-with-a-rather-long-name-to-check-wrapping"),
		entry("decisions/000001.yaml", "Ship the preview first", "", "office.decision.v1", true, "decisions"),
		entry("notes/000001.md", "Customer reads the minutes", "The customer reads the minutes after a meeting.", "office.note.v1", true, "customers", "research"),
	}
	idx := newWiki("index")
	idx.Tags, idx.Artifacts = manyTags, docs
	add(wikiPage("portal-wiki-index.html", "Wiki index with many tags", web.WikiPath(tenantID), idx, ""))
	filtered := newWiki("index")
	filtered.Tag = "customers"
	filtered.Tags = append([]tagView{tag("customers", 2, true)}, manyTags[:5]...)
	filtered.Artifacts = docs[3:]
	add(wikiPage("portal-wiki-tag.html", "Wiki index filtered by a tag", web.WikiPath(tenantID)+"?tag=customers", filtered, ""))
	add(wikiPage("portal-wiki-empty.html", "Wiki index of an empty wiki", web.WikiPath(tenantID)+"?empty", newWiki("index"), ""))

	art := newWiki("artifact")
	art.Artifact = &artifactPage{Entry: docs[0], Kind: "page", FrontMatter: "status: approved\nowner: ada\nreviews:\n  - kai 2026-11-25\n",
		Body: wikiMarkdown("documents/roadmap.md", []byte("# Roadmap\n\nThe team builds the **preview** first. See [the decision](../decisions/000001.yaml) and [a missing page](missing.md).\n\n- Stage\n- Portal\n"),
			map[string]string{"decisions/000001.yaml": web.WikiArtifactPath(tenantID, "decisions/000001.yaml")}),
		Links:     []linkView{{Path: "decisions/000001.yaml", Href: web.WikiArtifactPath(tenantID, "decisions/000001.yaml"), Title: "Ship the preview first"}, {Path: "documents/missing.md", Broken: true}, {Path: "documents/"}},
		Sources:   []linkView{{Path: "notes/000001.md", Href: web.WikiArtifactPath(tenantID, "notes/000001.md"), Title: "Customer reads the minutes"}},
		Backlinks: docs[1:2],
		Trust: trustView{Status: "approved", Owner: "ada", Reviews: []reviewView{{ParticipantID: "kai", Date: "2026-11-25"}, {ParticipantID: "grace", Date: "2026-11-26"}},
			LastAuthor: &authorView{Name: "Minutes keeper", ParticipantID: "minutes-keeper", Kind: "agent"}, LastChange: "2026-11-26 16:00",
			NewestSource: "2026-11-25 10:00", ReleasedAt: "2026-11-26 16:30", ReleasedBy: nil}}
	add(wikiPage("portal-wiki-artifact.html", "Roadmap", web.WikiArtifactPath(tenantID, "documents/roadmap.md"), art, ""))
	src := newWiki("artifact")
	src.Artifact = &artifactPage{Entry: docs[2], Kind: "source: a record that is written once", Text: "text: Ship the preview first.\nauthor: ada\n", Staged: true,
		Trust: trustView{LastAuthor: &authorView{Name: "Ada Lovelace", ParticipantID: "ada", Kind: "human"}, LastChange: "2026-11-20 09:50"}}
	add(wikiPage("portal-wiki-source.html", "Ship the preview first", web.WikiArtifactPath(tenantID, "decisions/000001.yaml"), src, ""))
	gone := newWiki("artifact")
	gone.Artifact = &artifactPage{Entry: docs[3], Kind: "source: a record that is written once", Missing: true}
	add(wikiPage("portal-wiki-missing-file.html", "Customer reads the minutes", web.WikiArtifactPath(tenantID, "notes/000001.md"), gone, ""))

	hits := newWiki("search")
	hits.Query, hits.Artifacts = "minutes", docs[3:]
	add(wikiPage("portal-wiki-search.html", "Search the wiki: hits", web.WikiSearchPath(tenantID)+"?q=minutes", hits, ""))
	none := newWiki("search")
	none.Query = "nothing"
	add(wikiPage("portal-wiki-search-none.html", "Search the wiki: no hit", web.WikiSearchPath(tenantID)+"?q=nothing", none, ""))
	add(wikiPage("portal-wiki-search-form.html", "Search the wiki: no query", web.WikiSearchPath(tenantID), newWiki("search"), ""))
	bad := newWiki("search")
	bad.Query = "!!"
	add(wikiPage("portal-wiki-search-error.html", "Search the wiki: invalid query", web.WikiSearchPath(tenantID)+"?q=!!", bad, "The search needs at least one word of letters or digits."))

	broken := newWiki("broken")
	broken.Broken = []brokenView{{FromPath: "documents/roadmap.md", FromHref: web.WikiArtifactPath(tenantID, "documents/roadmap.md"), ToPath: "documents/missing.md"},
		{FromPath: "documents/a-document-with-a-very-long-title/the-title-is-long-and-so-is-the-path.md", FromHref: "#", ToPath: "notes/000099.md", Source: true}}
	add(wikiPage("portal-wiki-broken.html", "Broken links", web.WikiBrokenPath(tenantID), broken, ""))
	add(wikiPage("portal-wiki-broken-none.html", "Broken links: none", web.WikiBrokenPath(tenantID)+"?none", newWiki("broken"), ""))
	queue := newWiki("queue")
	queue.Artifacts = docs[2:]
	add(wikiPage("portal-wiki-queue.html", "Ingest queue", web.WikiQueuePath(tenantID), queue, ""))
	add(wikiPage("portal-wiki-queue-empty.html", "Ingest queue: empty", web.WikiQueuePath(tenantID)+"?empty", newWiki("queue"), ""))

	// Members and administration.
	add(previewPage{name: "portal-members.html", title: "Members of a tenant", active: web.MembersPath(tenantID), tenant: tenantID, me: admin,
		data: map[string]any{"Title": "Members", "Section": tenantID, "Mode": "members",
			"Members": []memberRow{{UserId: "ada", Role: "tenant admin", Since: "2026-01-05"}, {UserId: "kai", Role: "member", Since: "2026-03-14"},
				{UserId: "a-user-with-a-rather-long-identifier-to-check-the-table", Role: "member", Since: "2026-11-01"}}}})
	keys := []keyView{
		{ParticipantId: "ada", Name: "Ada Lovelace", KindName: "human", KeyId: "fixture-key-ada-laptop-first", Admin: true},
		{ParticipantId: "ada", Name: "Ada Lovelace", KindName: "human", KeyId: "fixture-key-ada-laptop-old", Revoked: true, Admin: true},
		{ParticipantId: "minutes-keeper", Name: "Minutes keeper", KindName: "agent", KeyId: "fixture-key-minutes-keeper"},
		{ParticipantId: "old-user", Name: "Old User", KindName: "human", KeyId: "fixture-key-old-user-laptop", Removed: true},
	}
	add(previewPage{name: "portal-admin-keys.html", title: "Administration: users and keys", active: web.KeysPath, me: admin,
		data: map[string]any{"Title": "Users and keys", "Section": "Administration", "Keys": keys, "Notice": "The key is registered."}})
	add(previewPage{name: "portal-admin-keys-empty.html", title: "Administration: no keys", active: web.KeysPath + "?empty", me: admin,
		data: map[string]any{"Title": "Users and keys", "Section": "Administration"}})
	add(previewPage{name: "portal-admin-key-new.html", title: "Administration: register a key (with an error)", active: web.NewKeyPath, me: admin,
		data: map[string]any{"Title": "Register a key", "Section": "Administration", "Mode": "register", "CSRF": csrfPreview,
			"Form": formValues{ParticipantID: "ada", DisplayName: "Ada", Kind: "agent", PublicKey: "not a key"}, "Error": "The public key is not a base64 Ed25519 public key. Use the one that office key show prints."}})
	add(previewPage{name: "portal-admin-key-revoke.html", title: "Administration: confirm to revoke a key", active: web.RevokePath, me: admin,
		data: map[string]any{"Title": "Revoke a key", "Section": "Administration", "Mode": "revoke", "CSRF": csrfPreview, "Key": keys[2]}})
	add(previewPage{name: "portal-admin-tenants.html", title: "Administration: tenants", active: web.TenantsPath, me: admin,
		data: map[string]any{"Title": "Tenants", "Section": "Administration", "Mode": "tenants",
			"Tenants": []tenantView{{TenantId: tenantID, Name: "Acme Corporation"}, {TenantId: "globex", Name: "Globex"}, {TenantId: "initech", Name: "Initech", Removed: true}}}})
	add(previewPage{name: "portal-admin-tenants-empty.html", title: "Administration: no tenants", active: web.TenantsPath + "?empty", me: admin,
		data: map[string]any{"Title": "Tenants", "Section": "Administration", "Mode": "tenants"}})
	add(previewPage{name: "portal-admin-admins.html", title: "Administration: system admins", active: web.AdminsPath, me: admin,
		data: map[string]any{"Title": "System admins", "Section": "Administration", "Mode": "admins", "Keys": keys[:2]}})

	// A member without administration sees a smaller navigation.
	add(previewPage{name: "portal-member-tenant.html", title: "Tenant page of a member (no administration, no members link)", active: web.TenantPath(tenantID), tenant: tenantID, me: member,
		data: map[string]any{"Title": tenantID, "Section": "Tenant", "Mode": "tenant", "Tenant": tenantID, "Role": "member",
			"Sessions": sessionList()[:2], "TenantHref": web.TenantPath(tenantID)}})
	return ps
}
