package portal

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect/v2"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/portal/web"
	sessioncommandsv1 "github.com/mrsimpson/agentic-office/hub/session/commandsapi"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
)

// This file is the release of a session in the portal (concept-session-release): the status, the
// staged or released artifacts, and the forms that release and object. The forms call the session
// commands with the token of the user; the hub decides everything.

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
	Cut           bool // the page shows only the start of the artifact
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
		ReleaseCommand: commandID(), ObjectCommand: commandID()}
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
		a := artifactView{Path: p, Staged: f.GetFile().GetStaged()}
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
	cmds := sessioncommandsv1connect.NewSessionCommandsServiceClient(connect.NewClient(h.opts.Hub, auth.StaticToken(token), auth.Tenant(tenant)))
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
	}
	http.Redirect(w, r, web.SessionPath(tenant, sid)+"?done="+done, http.StatusSeeOther)
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
