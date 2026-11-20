package portal

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	decisionv1 "github.com/mrsimpson/agentic-office/hub/records/decision/api"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	minutesv1 "github.com/mrsimpson/agentic-office/hub/records/minutes/api"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	notev1 "github.com/mrsimpson/agentic-office/hub/records/note/api"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	slidesv1 "github.com/mrsimpson/agentic-office/hub/slides/api"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
)

// This file is the release of a session in the portal (concept-session-release): the status, the
// staged or released artifacts, the forms that release and object, and the form that discards an
// artifact that the release kept back. The forms call the session commands, or the interface of
// the type of the artifact, with the token of the user; the hub decides everything.

// maxArtifactShown is the most bytes of one artifact that the page shows.
const maxArtifactShown = 64 << 10

// releaseView is the release of a session as the page shows it.
type releaseView struct {
	State    string // open, waiting, held or released
	Deadline string // set while the session waits, UTC
	// MayRelease shows the forms: the hub says that the user may release or object now.
	MayRelease              bool
	Waiting                 bool // an objection is possible
	ReleasePath, ObjectPath string
	// DiscardPath takes the form that discards an artifact that the release kept back; CSRF is
	// the token of the forms.
	DiscardPath, CSRF string
	// ReleaseCommand and ObjectCommand are the command IDs of the forms, so a form that is sent
	// twice has one effect (dec-idempotent-commands).
	ReleaseCommand, ObjectCommand string
	Commit                        *commitView
	Artifacts                     []artifactView
	// Quotes and QuoteToken are set when the hub refused a release because of quotes. The form
	// "Release anyway" sends the refused command again, AnywayCommand, with the token.
	Quotes        []*sessioncommandsv1.Quote
	QuoteToken    string
	AnywayCommand string
}

// commitView is the release commit: who released, and the authors of the artifacts.
type commitView struct {
	ID, Short, Time string
	ReleasedBy      string // empty: the hub
	Authors         []string
}

type artifactView struct {
	Path, Content string
	Staged        bool
	// NotReleased marks a staged artifact that the release kept back because its path is taken.
	// Its form "Discard" carries the version that the page read and its own command ID.
	NotReleased      bool
	Version, Command string
	Cut              bool // the page shows only the start of the artifact
}

// pageExtra is what a form adds to the page of a session: a message, or the refusal of a release.
type pageExtra struct {
	status        int
	notice, error string
	refusal       *sessioncommandsv1.QuoteRefusal
	refusedCmd    string // the command ID of the refused release
}

// release reads the release of a session and its artifacts with the client of the page.
func release(ctx context.Context, cc *connect.Client, tenant, sid string) (*releaseView, error) {
	res, err := sessioncommandsv1connect.NewSessionCommandsServiceClient(cc).GetRelease(ctx, &sessioncommandsv1.GetReleaseRequest{SessionId: sid})
	if err != nil {
		return nil, err
	}
	v := &releaseView{MayRelease: res.GetMayRelease(), ReleasePath: web.ReleasePath(tenant, sid), ObjectPath: web.ObjectPath(tenant, sid),
		DiscardPath: web.DiscardPath(tenant, sid), ReleaseCommand: commandID(), ObjectCommand: commandID()}
	switch res.GetStatus().GetState() {
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_OPEN:
		v.State = "open"
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_WAITING:
		v.State, v.Waiting = "waiting", true
		if d := res.GetStatus().GetDeadline(); d != nil {
			v.Deadline = d.AsTime().UTC().Format(timeLayout)
		}
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_HELD:
		v.State = "held"
	case sessioncommandsv1.ReleaseState_RELEASE_STATE_RELEASED:
		v.State = "released"
	}
	paths := res.GetStaged()
	if c := res.GetCommit(); c != nil {
		cv := &commitView{ID: c.GetCommitId(), Short: c.GetCommitId(), Time: c.GetTime().AsTime().UTC().Format(timeLayout)}
		if len(cv.Short) > 12 {
			cv.Short = cv.Short[:12]
		}
		if by := c.GetReleasedBy(); by != nil {
			cv.ReleasedBy = by.GetName()
		}
		for _, a := range c.GetAuthors() {
			cv.Authors = append(cv.Authors, a.GetName())
		}
		v.Commit = cv
		paths = c.GetPaths()
	}
	read := archivev1connect.NewArchiveReadServiceClient(cc)
	unreleased := map[string]bool{}
	for _, p := range res.GetUnreleased() {
		unreleased[p] = true
		paths = append(paths, p)
	}
	for _, p := range paths {
		f, err := read.ReadFile(ctx, &archivev1.ReadFileRequest{Path: p})
		if connect.CodeOf(err) == connect.CodeNotFound || connect.CodeOf(err) == connect.CodeInvalidArgument {
			// A released artifact that was removed since, or a file that is no artifact.
			v.Artifacts = append(v.Artifacts, artifactView{Path: p})
			continue
		}
		if err != nil {
			return nil, err
		}
		a := artifactView{Path: p, Staged: f.GetFile().GetStaged(), NotReleased: unreleased[p]}
		if a.NotReleased {
			a.Version, a.Command = f.GetFile().GetVersion(), commandID()
		}
		content := f.GetFile().GetContent()
		if len(content) > maxArtifactShown {
			content, a.Cut = content[:maxArtifactShown], true
		}
		a.Content = strings.ToValidUTF8(string(content), string(utf8.RuneError))
		v.Artifacts = append(v.Artifacts, a)
	}
	return v, nil
}

// sessionAction takes the release form and the objection form of a session page. It calls the
// session command with the user as the participant and shows the answer on the page.
func (h *Handler) sessionAction(w http.ResponseWriter, r *http.Request, escapedTenant, escapedSession, action string) {
	c, token, ok := h.checkForm(w, r)
	if !ok {
		return
	}
	tenant, err := url.PathUnescape(escapedTenant)
	if _, member := c.me.membership(tenant); err != nil || !member {
		h.fail(w, http.StatusNotFound, "Not found", "There is no such tenant for you.")
		return
	}
	sid, err := url.PathUnescape(escapedSession)
	if err != nil || sid == "" {
		h.fail(w, http.StatusNotFound, "Not found", "There is no such session for you.")
		return
	}
	cc := connect.NewClient(h.opts.Hub, auth.StaticToken(token), auth.Tenant(tenant))
	cmds := sessioncommandsv1connect.NewSessionCommandsServiceClient(cc)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cmd := r.PostForm.Get("command_id")
	if cmd == "" {
		cmd = commandID()
	}
	done := ""
	switch action {
	case "release":
		_, err := cmds.ReleaseSession(ctx, &sessioncommandsv1.ReleaseSessionRequest{SessionId: sid, CommandId: cmd,
			ParticipantId: c.me.ParticipantID, AcceptQuotes: r.PostForm.Get("accept_quotes")})
		if q := sessioncommandsv1.RefusalOf(err); q != nil {
			h.sessionPage(w, r, c, tenant, sid, pageExtra{status: http.StatusConflict, refusal: q, refusedCmd: cmd})
			return
		}
		if err != nil {
			h.actionFailed(w, r, c, tenant, sid, err)
			return
		}
		done = "released"
	case "object":
		if _, err := cmds.ObjectToRelease(ctx, &sessioncommandsv1.ObjectToReleaseRequest{SessionId: sid, CommandId: cmd,
			ParticipantId: c.me.ParticipantID}); err != nil {
			h.actionFailed(w, r, c, tenant, sid, err)
			return
		}
		done = "held"
	case "discard":
		if err := discard(ctx, cc, sid, c.me.ParticipantID, cmd, r.PostForm.Get("path"), r.PostForm.Get("version")); err != nil {
			h.actionFailed(w, r, c, tenant, sid, err)
			return
		}
		done = "discarded"
	}
	http.Redirect(w, r, web.SessionPath(tenant, sid)+"?done="+done, http.StatusSeeOther)
}

// discard removes a staged artifact that the release of a session kept back through the
// interface of its type, which the directory of the path names: the removal of a staged record,
// deck or document (concept-session-release). The hub decides whether the user may.
func discard(ctx context.Context, cc *connect.Client, sid, pid, cmd, p, version string) error {
	dir, file := path.Split(p)
	number := func() (uint32, error) {
		n, err := strconv.ParseUint(strings.TrimSuffix(file, path.Ext(file)), 10, 32)
		if err != nil || n == 0 {
			return 0, connect.NewError(connect.CodeInvalidArgument, "the portal cannot discard "+p)
		}
		return uint32(n), nil
	}
	switch dir {
	case "decisions/":
		id, err := number()
		if err == nil {
			_, err = decisionv1connect.NewDecisionServiceClient(cc).RemoveDecision(ctx, &decisionv1.RemoveDecisionRequest{
				CommandId: cmd, Id: id, SessionId: sid, AuthorParticipantId: pid})
		}
		return err
	case "minutes/":
		id, err := number()
		if err == nil {
			_, err = minutesv1connect.NewMinutesServiceClient(cc).RemoveMinutes(ctx, &minutesv1.RemoveMinutesRequest{
				CommandId: cmd, Id: id, SessionId: sid, AuthorParticipantId: pid})
		}
		return err
	case "notes/":
		id, err := number()
		if err == nil {
			_, err = notev1connect.NewNoteServiceClient(cc).RemoveNote(ctx, &notev1.RemoveNoteRequest{
				CommandId: cmd, Id: id, SessionId: sid, AuthorParticipantId: pid})
		}
		return err
	case "slides/":
		id, err := number()
		if err == nil {
			_, err = slidesv1connect.NewSlidesServiceClient(cc).RemoveDeck(ctx, &slidesv1.RemoveDeckRequest{
				CommandId: cmd, Id: "slides/" + strconv.FormatUint(uint64(id), 10), SessionId: sid, AuthorParticipantId: pid})
		}
		return err
	case "documents/":
		_, err := documentv1connect.NewDocumentServiceClient(cc).RemoveDocument(ctx, &documentv1.RemoveDocumentRequest{
			Path: p, CommandId: cmd, SessionId: sid, BaseVersion: version})
		return err
	}
	return connect.NewError(connect.CodeInvalidArgument, "the portal cannot discard "+p)
}

// actionFailed shows a refused release or objection on the page of the session, or the error page
// when the user may not see the session.
func (h *Handler) actionFailed(w http.ResponseWriter, r *http.Request, c *backend, tenant, sid string, err error) {
	status := http.StatusConflict
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied:
		status = http.StatusForbidden
	case connect.CodeInvalidArgument:
		status = http.StatusBadRequest
	case connect.CodeFailedPrecondition, connect.CodeAlreadyExists, connect.CodeNotFound:
	default:
		h.denied(w, err)
		return
	}
	h.sessionPage(w, r, c, tenant, sid, pageExtra{status: status, error: messageOf(err)})
}
