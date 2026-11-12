package archive

import (
	"context"
	"path"
	"strings"

	"connectrpc.com/connect/v2"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

// sessionLogs is the directory of the session logs. They hold the original of sentences that
// are off the record, so nothing under it can be read through the read interface.
const sessionLogs = "sessions"

// ReadService implements the read interface of the archive (if-archive-read): ArchiveReadService
// for clients and Load and List for other building blocks of the hub, for example the stage.
type ReadService struct {
	arch storage.Archive
}

var _ archivev1connect.ArchiveReadServiceHandler = (*ReadService)(nil)

// NewReadService returns the read interface of arch.
func NewReadService(arch storage.Archive) *ReadService { return &ReadService{arch: arch} }

// ReadFile implements the RPC. It needs the access token of a participant.
func (s *ReadService) ReadFile(ctx context.Context, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error) {
	if err := participant(ctx); err != nil {
		return nil, err
	}
	return s.Load(ctx, req)
}

// ListFiles implements the RPC. It needs the access token of a participant.
func (s *ReadService) ListFiles(ctx context.Context, req *archivev1.ListFilesRequest) (*archivev1.ListFilesResponse, error) {
	if err := participant(ctx); err != nil {
		return nil, err
	}
	return s.List(ctx, req)
}

// Load reads one file without a token check. It is for building blocks of the hub that decide
// themselves which paths a caller may see, such as the stage.
func (s *ReadService) Load(ctx context.Context, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error) {
	p := req.GetPath()
	if err := checkReadable(p); err != nil {
		return nil, err
	}
	dir := path.Dir(p)
	if dir == "." {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the path needs a directory")
	}
	files, err := s.arch.Read(ctx, dir)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the archive could not be read")
	}
	for _, f := range files {
		if f.Path == p {
			return &archivev1.ReadFileResponse{File: &archivev1.File{Path: f.Path, Content: f.Data, Version: f.Version}}, nil
		}
	}
	return nil, connect.NewError(connect.CodeNotFound, "there is no file "+p)
}

// List lists the readable files of a directory without a token check; see Load.
func (s *ReadService) List(ctx context.Context, req *archivev1.ListFilesRequest) (*archivev1.ListFilesResponse, error) {
	dir := req.GetDir()
	if err := storage.CheckPath(dir); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if underSessionLogs(dir) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the session logs cannot be read")
	}
	files, err := s.arch.Read(ctx, dir)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the archive could not be read")
	}
	resp := &archivev1.ListFilesResponse{}
	for _, f := range files {
		if isArtifactFile(f.Path) {
			resp.Files = append(resp.Files, &archivev1.FileInfo{Path: f.Path, Version: f.Version})
		}
	}
	return resp, nil
}

// participant lets only the token of a participant pass. A stage link is shown on the shared
// screen, so it must not read the archive.
func participant(ctx context.Context) error {
	c, ok := tokencheck.CallerFrom(ctx)
	switch {
	case !ok:
		return connect.NewError(connect.CodeUnauthenticated, "no access token")
	case c.ParticipantID == "":
		return connect.NewError(connect.CodePermissionDenied, "a stage link cannot read the archive")
	}
	return nil
}

func checkReadable(p string) error {
	if err := storage.CheckPath(p); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if underSessionLogs(p) {
		return connect.NewError(connect.CodeInvalidArgument, "the session logs cannot be read")
	}
	if !isArtifactFile(p) {
		return connect.NewError(connect.CodeInvalidArgument, "only Markdown and YAML files can be read")
	}
	return nil
}

func underSessionLogs(p string) bool {
	return p == sessionLogs || strings.HasPrefix(p, sessionLogs+"/")
}

// isArtifactFile reports whether p is a Markdown or YAML file outside the session logs.
func isArtifactFile(p string) bool {
	if underSessionLogs(p) {
		return false
	}
	switch path.Ext(p) {
	case ".md", ".yaml", ".yml":
		return true
	}
	return false
}
