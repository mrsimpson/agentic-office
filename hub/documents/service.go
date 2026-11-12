package documents

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"connectrpc.com/connect/v2"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	documentv1 "github.com/mrsimpson/agentic-office/hub/documents/api"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
	"github.com/mrsimpson/agentic-office/hub/session/notice"
)

// Dir is the directory of the text documents in the archive.
const Dir = "documents"

// maxContent is the largest content of a document in bytes.
const maxContent = 1 << 20

// Service implements the documents interface (if-document-api).
type Service struct {
	arch   storage.Archive
	notify notice.Notifier // may be nil: no session is told

	mu   sync.Mutex // one write at a time
	done map[string]*documentv1.DocumentVersion
}

var _ documentv1connect.DocumentServiceHandler = (*Service)(nil)

// New returns a Service that stores documents in arch and tells sessions through notify. notify
// may be nil.
func New(arch storage.Archive, notify notice.Notifier) *Service {
	return &Service{arch: arch, notify: notify, done: map[string]*documentv1.DocumentVersion{}}
}

// CreateDocument implements the RPC.
func (s *Service) CreateDocument(ctx context.Context, req *documentv1.CreateDocumentRequest) (*documentv1.CreateDocumentResponse, error) {
	v, dup, err := s.write(ctx, true, req.GetPath(), req.GetContent(), req.GetCommandId(), req.GetSessionId(), storage.NoFile)
	if err != nil {
		return nil, err
	}
	return &documentv1.CreateDocumentResponse{Document: v, Duplicate: dup}, nil
}

// ChangeDocument implements the RPC.
func (s *Service) ChangeDocument(ctx context.Context, req *documentv1.ChangeDocumentRequest) (*documentv1.ChangeDocumentResponse, error) {
	if req.GetBaseVersion() == "" || req.GetBaseVersion() == storage.NoFile {
		return nil, invalid("base_version is required")
	}
	v, dup, err := s.write(ctx, false, req.GetPath(), req.GetContent(), req.GetCommandId(), req.GetSessionId(), req.GetBaseVersion())
	if err != nil {
		return nil, err
	}
	return &documentv1.ChangeDocumentResponse{Document: v, Duplicate: dup}, nil
}

func (s *Service) write(ctx context.Context, created bool, p, content, commandID, sessionID, base string) (*documentv1.DocumentVersion, bool, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	switch {
	case !ok:
		return nil, false, connect.NewError(connect.CodeUnauthenticated, "no access token")
	case c.ParticipantID == "":
		return nil, false, connect.NewError(connect.CodePermissionDenied, "a stage link cannot write documents")
	}
	if commandID == "" {
		return nil, false, invalid("command_id is required")
	}
	if err := checkPath(p); err != nil {
		return nil, false, err
	}
	if content == "" || len(content) > maxContent || !utf8.ValidString(content) {
		return nil, false, invalid("content must be valid UTF-8 text, not empty, at most 1 MiB")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := c.ParticipantID + "\x00" + commandID
	v, dup := s.done[key]
	if dup && (v.GetPath() != p || v.GetVersion() != versionOf([]byte(content))) {
		return nil, false, connect.NewError(connect.CodeAlreadyExists, "command_id is used by another command")
	}
	if !dup {
		name := c.DisplayName
		if name == "" {
			name = c.ParticipantID
		}
		msg := "Change " + p
		if created {
			msg = "Create " + p
		}
		_, err := s.arch.Commit(ctx, storage.Change{
			Message: msg,
			Author:  storage.Author{Name: name, Email: c.ParticipantID + "@office.invalid"},
			Files:   []storage.File{{Path: p, Data: []byte(content), Base: base}},
		})
		var ce *storage.ConflictError
		switch {
		case created && storage.IsConflict(err):
			return nil, false, connect.NewError(connect.CodeAlreadyExists, "there is already a document "+p)
		case errors.As(err, &ce) && ce.Current == "":
			return nil, false, connect.NewError(connect.CodeNotFound, "there is no document "+p)
		case errors.As(err, &ce):
			return nil, false, connect.NewError(connect.CodeAborted,
				fmt.Sprintf("the document has changed since you read it; its current version is %s", ce.Current))
		case err != nil:
			return nil, false, connect.NewError(connect.CodeInternal, "the document could not be stored")
		}
		v = &documentv1.DocumentVersion{Path: p, Version: versionOf([]byte(content))}
		s.done[key] = v
	}
	if sessionID != "" && s.notify != nil {
		if err := s.notify.NoteArtifact(ctx, sessionID, c.ParticipantID, commandID, created, p, v.GetVersion()); err != nil {
			return nil, false, err
		}
	}
	return v, dup, nil
}

// checkPath accepts "documents/<name>.md" with a clean path.
func checkPath(p string) error {
	if err := storage.CheckPath(p); err != nil {
		return invalid(err.Error())
	}
	if !strings.HasPrefix(p, Dir+"/") || path.Ext(p) != ".md" {
		return invalid("the path of a document is " + Dir + "/<name>.md")
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
