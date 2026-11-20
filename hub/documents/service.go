package documents

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	"github.com/mrsimpson/agentic-office/hub/documents/check"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/session/notice"
	"github.com/mrsimpson/agentic-office/hub/session/release"
)

// maxContent is the largest content of a document in bytes.
const maxContent = 1 << 20

// StubMessage is the commit message of every stub. It says nothing about the document, and it is
// the message of the stubs of the other parts too.
const StubMessage = "Stub: an artifact waits for the release of its session"

// Options are what the documents part of a tenant works with.
type Options struct {
	// Archive is the artifact repository of the tenant (if-archive-storage). Required.
	Archive storage.Archive
	// Staging is the staging repository of the tenant (if-archive-storage). Required.
	Staging storage.Staging
	// Sessions gives the release status of the sessions (if-session-release). Required.
	Sessions release.Checker
	// Notify tells a session about a write that names it (if-session-notice). Nil: no session is
	// told.
	Notify notice.Notifier
	// Logf logs what the part repairs at start. Nil means log.Printf.
	Logf func(string, ...any)
}

// Service implements the documents interface (if-document-api).
type Service struct {
	opts Options

	mu   sync.Mutex // one write at a time; guards done
	done map[string]result
}

// result is what a command did, for its command_id.
type result struct {
	op string // "create", "change" or "remove"
	v  *documentv1.DocumentVersion
}

var _ documentv1connect.DocumentServiceHandler = (*Service)(nil)

// Open returns the service. A staged document whose stub is missing, which a crash between the two
// commits of a create leaves, gets its stub.
func Open(ctx context.Context, opts Options) (*Service, error) {
	if opts.Archive == nil || opts.Staging == nil || opts.Sessions == nil {
		return nil, errors.New("documents: the archive, the staging repository and the release status are required")
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	s := &Service{opts: opts, done: map[string]result{}}
	areas, err := opts.Staging.Areas(ctx)
	if err != nil {
		return nil, fmt.Errorf("documents: read the staging areas: %w", err)
	}
	for _, a := range areas {
		files, err := opts.Staging.Read(ctx, a.Session, check.Dir)
		if errors.Is(err, storage.ErrNoArea) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("documents: read the staging area of %s: %w", a.Session, err)
		}
		for _, f := range files {
			if checkPath(f.Path) != nil {
				continue
			}
			art, err := opts.Archive.Read(ctx, f.Path)
			if err != nil {
				return nil, fmt.Errorf("documents: read %s: %w", f.Path, err)
			}
			if len(art) > 0 {
				continue
			}
			if err := s.writeStub(ctx, a.Session, f.Path); err != nil {
				opts.Logf("documents: write the missing stub of %s: %v", f.Path, err)
			}
		}
	}
	return s, nil
}

// writeStub writes the stub of a staged document through the staging area of its session, so the
// area records it (storage.Staging.Stub). The file must not exist yet.
func (s *Service) writeStub(ctx context.Context, session, p string) error {
	_, err := s.opts.Staging.Stub(context.WithoutCancel(ctx), session, storage.Change{Message: StubMessage,
		Files: []storage.File{{Path: p, Data: check.Stub(), Base: storage.NoFile}}})
	return err
}

// CreateDocument implements the RPC.
func (s *Service) CreateDocument(ctx context.Context, req *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	c, err := s.command(ctx, req.GetPath(), req.GetCommandId(), true, req.GetContent())
	if err != nil {
		return nil, err
	}
	v, dup, err := s.create(ctx, c, req.GetPath(), req.GetContent(), req.GetCommandId(), req.GetSessionId())
	if err != nil {
		return nil, err
	}
	return &documentv1.CreateDocumentResponse{Document: v, Duplicate: dup, Hints: s.hints(ctx, req.GetPath(), req.GetContent())}, nil
}

// ChangeDocument implements the RPC.
func (s *Service) ChangeDocument(ctx context.Context, req *documentv1.ChangeDocumentRequest) (*documentv1.ChangeDocumentResponse, error) {
	c, err := s.command(ctx, req.GetPath(), req.GetCommandId(), true, req.GetContent())
	if err != nil {
		return nil, err
	}
	if req.GetBaseVersion() == "" || req.GetBaseVersion() == storage.NoFile {
		return nil, invalid("base_version is required")
	}
	v, dup, err := s.edit(ctx, c, "change", req.GetPath(), req.GetContent(), req.GetCommandId(), req.GetSessionId(), req.GetBaseVersion())
	if err != nil {
		return nil, err
	}
	return &documentv1.ChangeDocumentResponse{Document: v, Duplicate: dup, Hints: s.hints(ctx, req.GetPath(), req.GetContent())}, nil
}

// RemoveDocument implements the RPC.
func (s *Service) RemoveDocument(ctx context.Context, req *documentv1.RemoveDocumentRequest) (*documentv1.RemoveDocumentResponse, error) {
	c, err := s.command(ctx, req.GetPath(), req.GetCommandId(), false, "")
	if err != nil {
		return nil, err
	}
	if req.GetBaseVersion() == "" || req.GetBaseVersion() == storage.NoFile {
		return nil, invalid("base_version is required")
	}
	_, dup, err := s.edit(ctx, c, "remove", req.GetPath(), "", req.GetCommandId(), req.GetSessionId(), req.GetBaseVersion())
	if err != nil {
		return nil, err
	}
	return &documentv1.RemoveDocumentResponse{Duplicate: dup}, nil
}

// command checks the caller and the fields of a command.
func (s *Service) command(ctx context.Context, p, commandID string, hasContent bool, content string) (tokencheck.Caller, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	switch {
	case !ok:
		return c, connect.NewError(connect.CodeUnauthenticated, "no access token")
	case c.ParticipantID == "":
		return c, connect.NewError(connect.CodePermissionDenied, "a stage link cannot write documents")
	}
	if commandID == "" {
		return c, invalid("command_id is required")
	}
	if err := checkPath(p); err != nil {
		return c, err
	}
	if hasContent {
		if content == "" || len(content) > maxContent || !utf8.ValidString(content) {
			return c, invalid("content must be valid UTF-8 text, not empty, at most 1 MiB")
		}
		if check.IsStub([]byte(content)) {
			return c, invalid("the content is the stub of a document; a document never has this content")
		}
		if reasons := check.Check(p, []byte(content)); len(reasons) > 0 {
			return c, invalid("the document is not valid: " + strings.Join(reasons, "; "))
		}
	}
	return c, nil
}

// hints returns the hints of a document that was just written: a link that leads to no file of
// the artifact repository. A stub is a file there, so a link to a staged document works. A path
// that cannot be read counts as a file, so no hint is wrong.
func (s *Service) hints(ctx context.Context, p, content string) []string {
	return check.Hints(p, []byte(content), func(target string) bool {
		if storage.CheckPath(target) != nil {
			return false
		}
		files, err := s.opts.Archive.List(ctx, target)
		return err != nil || len(files) > 0
	})
}

// previous returns the result of an earlier command with the same command_id, and false if there
// was none. A command_id that was used for another command is AlreadyExists.
func (s *Service) previous(key, op, p, version string) (*documentv1.DocumentVersion, bool, error) {
	r, ok := s.done[key]
	if !ok {
		return nil, false, nil
	}
	if r.op != op || r.v.GetPath() != p || (op != "remove" && r.v.GetVersion() != version) {
		return nil, false, connect.NewError(connect.CodeAlreadyExists, "command_id is used by another command")
	}
	return r.v, true, nil
}

func (s *Service) previousLocked(key, op, p, version string) (*documentv1.DocumentVersion, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.previous(key, op, p, version)
}

// errNotParticipant is the answer for a session that the caller may not name. A session that does
// not exist gets the same answer.
func errNotParticipant() error {
	return connect.NewError(connect.CodePermissionDenied, "only a participant of the session may name it")
}

// tookPart asks the meeting session whether the caller took part in a session.
func (s *Service) tookPart(ctx context.Context, sessionID, pid string) error {
	ok, err := s.opts.Sessions.TookPart(ctx, sessionID, pid)
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if !ok {
		return errNotParticipant()
	}
	return nil
}

// errReleased tells a caller of releasing that the session is released: its area is gone, or its
// release is done and the area keeps only what the release kept back.
var errReleased = errors.New("released")

// releasing returns errReleased when the session is released and its staging area is gone or its
// release is done, and an error when the session is released but its area is still moving, so a
// write waits.
func (s *Service) releasing(ctx context.Context, sessionID string) error {
	st, err := s.opts.Sessions.State(ctx, sessionID)
	if errors.Is(err, release.ErrNoSession) {
		return errNotParticipant()
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, "ask the meeting session: "+err.Error())
	}
	if st.Status != release.Released {
		return nil
	}
	if a, err := s.opts.Staging.Area(ctx, sessionID); err == nil {
		if a.Release != nil && a.Release.Done {
			return errReleased // the area keeps only what the release kept back
		}
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	} else if !errors.Is(err, storage.ErrNoArea) {
		return connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
	}
	return errReleased
}

// state is what a path is.
type state int

const (
	stateNone     state = iota // no document
	stateReleased              // a document in the artifact repository
	stateStaged                // a document in the staging area of a session
	stateRemoved               // a staged document that its session removed; its stub stays until the release
	stateStub                  // a stub that no area names
)

// place is where a document lies.
type place struct {
	state   state
	session string        // the area that names the path
	file    *storage.File // the released or the staged file
}

// locate reads what lies at a path: the file of the artifact repository and the staged file of the
// area that names the path. A document in the artifact repository wins over a staged one.
func (s *Service) locate(ctx context.Context, p string) (place, error) {
	art, err := s.opts.Archive.Read(ctx, p)
	if err != nil {
		return place{}, connect.NewError(connect.CodeInternal, "read the archive: "+err.Error())
	}
	if len(art) > 0 && !check.IsStub(art[0].Data) {
		return place{state: stateReleased, file: &art[0]}, nil
	}
	areas, err := s.opts.Staging.Areas(ctx)
	if err != nil {
		return place{}, connect.NewError(connect.CodeInternal, "read the staging areas: "+err.Error())
	}
	for _, a := range areas {
		if !slices.Contains(a.Names, p) {
			continue
		}
		files, err := s.opts.Staging.Read(ctx, a.Session, p)
		if errors.Is(err, storage.ErrNoArea) {
			break // released in the meantime
		}
		if err != nil {
			return place{}, connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
		}
		if len(files) == 0 {
			if len(art) == 0 {
				return place{}, nil
			}
			return place{state: stateRemoved, session: a.Session}, nil
		}
		return place{state: stateStaged, session: a.Session, file: &files[0]}, nil
	}
	if len(art) > 0 {
		return place{state: stateStub}, nil
	}
	return place{}, nil
}

// author is the author of a commit of the caller.
func author(c tokencheck.Caller) storage.Author {
	name := c.DisplayName
	if name == "" {
		name = c.ParticipantID
	}
	return storage.Author{Name: name, Email: c.ParticipantID + "@office.invalid"}
}

// storeErr maps an error of a write to the staging area.
func storeErr(err error) error {
	switch {
	case errors.Is(err, storage.ErrNoArea):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the release status of the session and its staging area disagree: the session has no staging area")
	case errors.Is(err, storage.ErrAreaClosed):
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	}
	return connect.NewError(connect.CodeInternal, "the document could not be stored")
}

// create stores a new document: in the staging area of the session it names while the session is
// not released, else in the artifact repository (concept-session-release).
func (s *Service) create(ctx context.Context, c tokencheck.Caller, p, content, commandID, sessionID string) (*documentv1.DocumentVersion, bool, error) {
	key := c.ParticipantID + "\x00" + commandID
	version := versionOf([]byte(content))
	if v, dup, err := s.previousLocked(key, "create", p, version); err != nil || dup {
		return v, dup, s.tell(ctx, err, sessionID, c, commandID, true, v)
	}
	// Ask the meeting session before the lock: it never waits for this part.
	staged := false
	if sessionID != "" {
		if err := s.tookPart(ctx, sessionID, c.ParticipantID); err != nil {
			return nil, false, err
		}
		switch err := s.releasing(ctx, sessionID); {
		case errors.Is(err, errReleased):
		case err != nil:
			return nil, false, err
		default:
			staged = true
		}
	}
	// A cancelled commit could leave the repository locked, so the commits are not cancelled with
	// the call.
	wctx := context.WithoutCancel(ctx)
	s.mu.Lock()
	v, dup, err := s.previous(key, "create", p, version)
	if err == nil && !dup {
		if staged {
			err = s.stage(wctx, c, p, content, sessionID)
		} else {
			err = s.release(wctx, c, p, content)
		}
		if err == nil {
			v = &documentv1.DocumentVersion{Path: p, Version: version, Staged: staged}
			s.done[key] = result{op: "create", v: v}
		}
	}
	s.mu.Unlock()
	return v, dup, s.tell(ctx, err, sessionID, c, commandID, true, v)
}

// release creates a document in the artifact repository. The caller holds s.mu.
func (s *Service) release(ctx context.Context, c tokencheck.Caller, p, content string) error {
	_, err := s.opts.Archive.Commit(ctx, storage.Change{Message: "Create " + p, Author: author(c),
		Files: []storage.File{{Path: p, Data: []byte(content), Base: storage.NoFile}}})
	switch {
	case storage.IsConflict(err):
		return connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p)
	case err != nil:
		return connect.NewError(connect.CodeInternal, "the document could not be stored")
	}
	return nil
}

// stage creates a document in the staging area of a session, then its stub in the artifact
// repository. A path whose staged document the same session removed keeps its stub. The caller
// holds s.mu.
func (s *Service) stage(ctx context.Context, c tokencheck.Caller, p, content, sessionID string) error {
	at, err := s.locate(ctx, p)
	if err != nil {
		return err
	}
	switch {
	case at.state == stateRemoved && at.session == sessionID, at.state == stateNone:
	case at.state == stateStaged && at.session != sessionID, at.state == stateRemoved:
		return connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p+"; it waits for the release of another session")
	default:
		return connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p)
	}
	_, err = s.opts.Staging.Commit(ctx, sessionID, storage.Change{Message: "Create " + p, Author: author(c),
		Files: []storage.File{{Path: p, Data: []byte(content), Base: storage.NoFile}}})
	if storage.IsConflict(err) {
		return connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p)
	}
	if err != nil {
		return storeErr(err)
	}
	if at.state == stateRemoved {
		return nil // the stub is there
	}
	if err := s.writeStub(ctx, sessionID, p); err != nil {
		// Take the staged file back, and the area forgets the path. A push that put a file at this
		// path first keeps it, also at the release.
		if rerr := s.opts.Staging.TakeBack(ctx, sessionID, p); rerr != nil {
			s.opts.Logf("documents: take back the staged %s: %v", p, rerr)
		}
		if storage.IsConflict(err) {
			return connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p)
		}
		return connect.NewError(connect.CodeInternal, "the stub of the document could not be stored")
	}
	return nil
}

// edit changes a document, or removes a staged one (op "remove"). A staged document changes in
// the staging area of its session, for a participant of the session who names it; a released
// document changes in the artifact repository and is not held.
func (s *Service) edit(ctx context.Context, c tokencheck.Caller, op, p, content, commandID, sessionID, base string) (*documentv1.DocumentVersion, bool, error) {
	key := c.ParticipantID + "\x00" + commandID
	version := versionOf([]byte(content))
	if v, dup, err := s.previousLocked(key, op, p, version); err != nil || dup {
		if op == "remove" {
			return v, dup, err
		}
		return v, dup, s.tell(ctx, err, sessionID, c, commandID, false, v)
	}
	if op == "remove" {
		// A staged document that the release kept back because its path is taken goes on removal.
		if v, done, err := s.discardKept(ctx, c, key, p, sessionID, base); done || err != nil {
			return v, false, err
		}
	}
	at, err := s.locateLocked(ctx, p)
	if err != nil {
		return nil, false, err
	}
	// Ask the meeting session before the lock: it never waits for this part.
	switch at.state {
	case stateNone:
		return nil, false, connect.NewError(connect.CodeNotFound, "there is no document "+p)
	case stateStub:
		return nil, false, connect.NewError(connect.CodeFailedPrecondition, p+" is a stub, and no interface changes a stub")
	case stateReleased:
		if op == "remove" {
			return nil, false, connect.NewError(connect.CodeFailedPrecondition,
				"the document "+p+" is released; only a staged document can be removed here, remove a released one with Git")
		}
		if sessionID != "" {
			if err := s.tookPart(ctx, sessionID, c.ParticipantID); err != nil {
				return nil, false, err
			}
		}
	case stateStaged, stateRemoved:
		if err := s.tookPart(ctx, at.session, c.ParticipantID); err != nil {
			return nil, false, connect.NewError(connect.CodePermissionDenied,
				"only a participant of the session of the document "+p+" may change or remove it")
		}
		if sessionID != at.session {
			return nil, false, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Sprintf("the document %s waits for the release of session %s; a change or a removal must name that session", p, at.session))
		}
		if at.state == stateRemoved {
			if op == "remove" {
				return nil, true, nil
			}
			return nil, false, connect.NewError(connect.CodeNotFound, "the document "+p+" was removed")
		}
		switch err := s.releasing(ctx, at.session); {
		case errors.Is(err, errReleased):
			return nil, false, connect.NewError(connect.CodeAborted, "the session of the document was released meanwhile; repeat the command")
		case err != nil:
			return nil, false, err
		}
	}

	wctx := context.WithoutCancel(ctx)
	s.mu.Lock()
	v, dup, err := s.previous(key, op, p, version)
	if err == nil && !dup {
		v, err = s.commitEdit(wctx, c, op, p, content, base, at)
		if err == nil {
			s.done[key] = result{op: op, v: v}
		}
	}
	s.mu.Unlock()
	if op == "remove" {
		return v, dup, err
	}
	return v, dup, s.tell(ctx, err, sessionID, c, commandID, false, v)
}

// discardKept removes the staged document at p from the staging area of its session when the
// release of the session kept it back because the path is taken (storage.Staging.Discard), for a
// participant of the session who names it and read the staged version base. It reports false when
// p is not such a document.
func (s *Service) discardKept(ctx context.Context, c tokencheck.Caller, key, p, sessionID, base string) (*documentv1.DocumentVersion, bool, error) {
	areas, err := s.opts.Staging.Areas(ctx)
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, "read the staging areas: "+err.Error())
	}
	i := slices.IndexFunc(areas, func(a storage.Area) bool { return slices.Contains(a.Names, p) })
	if i < 0 || areas[i].Release == nil || !areas[i].Release.Done {
		return nil, false, nil
	}
	session := areas[i].Session
	if err := s.tookPart(ctx, session, c.ParticipantID); err != nil {
		return nil, false, connect.NewError(connect.CodePermissionDenied,
			"only a participant of the session of the document "+p+" may remove it")
	}
	if sessionID != session {
		return nil, false, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Sprintf("the document %s was not released with session %s; a removal must name that session", p, session))
	}
	wctx := context.WithoutCancel(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := s.opts.Staging.Read(wctx, session, p)
	switch {
	case errors.Is(err, storage.ErrNoArea) || err == nil && len(files) == 0:
		return nil, false, connect.NewError(connect.CodeAborted, "the document "+p+" changed meanwhile; repeat the command")
	case err != nil:
		return nil, false, connect.NewError(connect.CodeInternal, "read the staging area: "+err.Error())
	case files[0].Version != base:
		return nil, false, connect.NewError(connect.CodeAborted,
			fmt.Sprintf("the document has changed since you read it; its current version is %s", files[0].Version))
	}
	err = s.opts.Staging.Discard(wctx, session, p, author(c))
	switch {
	case errors.Is(err, storage.ErrNotKept), errors.Is(err, storage.ErrNoArea):
		return nil, false, connect.NewError(connect.CodeAborted, "the document "+p+" changed meanwhile; repeat the command")
	case err != nil:
		return nil, false, connect.NewError(connect.CodeInternal, "the document could not be discarded")
	}
	v := &documentv1.DocumentVersion{Path: p, Staged: true}
	s.done[key] = result{op: "remove", v: v}
	return v, true, nil
}

func (s *Service) locateLocked(ctx context.Context, p string) (place, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locate(ctx, p)
}

// commitEdit writes a change or a removal after the checks of edit. It reads the path again and
// refuses the command when it is no longer where edit found it. The caller holds s.mu.
func (s *Service) commitEdit(ctx context.Context, c tokencheck.Caller, op, p, content, base string, was place) (*documentv1.DocumentVersion, error) {
	at, err := s.locate(ctx, p)
	if err != nil {
		return nil, err
	}
	if at.state != was.state || at.session != was.session {
		return nil, connect.NewError(connect.CodeAborted, "the document "+p+" changed meanwhile; repeat the command")
	}
	if at.file.Version != base {
		return nil, connect.NewError(connect.CodeAborted,
			fmt.Sprintf("the document has changed since you read it; its current version is %s", at.file.Version))
	}
	file := storage.File{Path: p, Data: []byte(content), Base: base, Remove: op == "remove"}
	msg := "Change " + p
	if op == "remove" {
		msg = "Remove " + p
	}
	change := storage.Change{Message: msg, Author: author(c), Files: []storage.File{file}}
	if at.state == stateReleased {
		_, err = s.opts.Archive.Commit(ctx, change)
	} else {
		_, err = s.opts.Staging.Commit(ctx, at.session, change)
	}
	var ce *storage.ConflictError
	switch {
	case errors.As(err, &ce) && ce.Current == "":
		return nil, connect.NewError(connect.CodeNotFound, "there is no document "+p)
	case errors.As(err, &ce):
		return nil, connect.NewError(connect.CodeAborted,
			fmt.Sprintf("the document has changed since you read it; its current version is %s", ce.Current))
	case err != nil && at.state == stateStaged:
		return nil, storeErr(err)
	case err != nil:
		return nil, connect.NewError(connect.CodeInternal, "the document could not be stored")
	}
	if op == "remove" {
		return &documentv1.DocumentVersion{Path: p, Staged: true}, nil
	}
	return &documentv1.DocumentVersion{Path: p, Version: versionOf([]byte(content)), Staged: at.state == stateStaged}, nil
}

// tell tells the session that a call names about the write, after the write, and returns err when
// there was one. A repeated command tells the session again, so a failed notice can be repeated.
func (s *Service) tell(ctx context.Context, err error, sessionID string, c tokencheck.Caller, commandID string, created bool,
	v *documentv1.DocumentVersion) error {
	if err != nil || sessionID == "" || s.opts.Notify == nil || v == nil {
		return err
	}
	return s.opts.Notify.NoteArtifact(ctx, sessionID, c.ParticipantID, commandID, created, v.GetPath(), v.GetVersion())
}

// checkPath accepts "documents/<name>.md" with a clean path.
func checkPath(p string) error {
	if err := storage.CheckPath(p); err != nil {
		return invalid(err.Error())
	}
	if reason := check.PathProblem(p); reason != "" {
		return invalid(reason)
	}
	return nil
}

// versionOf is the Git object ID of a file with this content, which is also the version that the
// archive gives when it reads the file.
func versionOf(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, msg) }
