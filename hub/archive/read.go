package archive

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"

	archivev1 "github.com/mrsimpson/agentic-office/hub/archive/api"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

// ReadService implements the read interface of the archive (if-archive-read): ArchiveReadService
// for clients and Load for other building blocks of the hub, for example the stage.
//
// A path that the staging area of a session names is staged (concept-session-release). A reader
// of that session (Area.Readers) or a tenant admin by role reads the staged file, with staged
// set; everyone else reads what the artifact repository holds at the path, the stub. A staged
// file that its session removed is not found for a reader. The archive reads no content to
// decide this: it follows the names of the areas.
type ReadService struct {
	arch    storage.Archive
	staging storage.Staging
}

var _ archivev1connect.ArchiveReadServiceHandler = (*ReadService)(nil)

// NewReadService returns the read interface of the artifact repository arch and the staging
// repository staging of a tenant.
func NewReadService(arch storage.Archive, staging storage.Staging) *ReadService {
	return &ReadService{arch: arch, staging: staging}
}

// mayRead tells whether a caller may read the staged files of a session.
type mayRead func(area storage.Area) bool

// readerOf returns the reader check of a caller: a tenant admin reads every area, everyone else
// the areas that list them as a reader.
func readerOf(c tokencheck.Caller) mayRead {
	if c.TenantRole == tokencheck.RoleTenantAdmin {
		return func(storage.Area) bool { return true }
	}
	return func(a storage.Area) bool { return slices.Contains(a.Readers, c.ParticipantID) }
}

// ReadFile implements the RPC. It needs the access token of a participant.
func (s *ReadService) ReadFile(ctx context.Context, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error) {
	c, err := participant(ctx)
	if err != nil {
		return nil, err
	}
	return s.read(ctx, req.GetPath(), readerOf(c))
}

// ListFiles implements the RPC. It needs the access token of a participant.
func (s *ReadService) ListFiles(ctx context.Context, req *archivev1.ListFilesRequest) (*archivev1.ListFilesResponse, error) {
	c, err := participant(ctx)
	if err != nil {
		return nil, err
	}
	return s.list(ctx, req.GetDir(), readerOf(c))
}

// Load reads one file without a token check, as the stage of a session sees it: the staged files
// of that session, and the artifact repository elsewhere. It is for building blocks of the hub
// that decide themselves which paths a caller may see, such as the stage.
func (s *ReadService) Load(ctx context.Context, session string, req *archivev1.ReadFileRequest) (*archivev1.ReadFileResponse, error) {
	return s.read(ctx, req.GetPath(), func(a storage.Area) bool { return session != "" && a.Session == session })
}

// Staged lists the files that the area of a session holds, with their versions, ordered by path:
// what the stage of that session may show next to the artifacts that its log names. It reads only
// the area of the session it is given. A session without an area, for example one that is
// released, has no staged files. It is for building blocks of the hub that take the session from
// a token that they checked themselves, such as the stage.
func (s *ReadService) Staged(ctx context.Context, session string) ([]*archivev1.FileInfo, error) {
	if session == "" || s.staging == nil {
		return nil, nil
	}
	names, err := s.staging.List(ctx, session)
	if errors.Is(err, storage.ErrNoArea) {
		return nil, nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the staging area could not be read")
	}
	var dirs []string
	for _, n := range names {
		if d := path.Dir(n); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	seen := map[string]bool{}
	var out []*archivev1.FileInfo
	for _, d := range dirs {
		files, err := s.staging.Read(ctx, session, d)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "the staging area could not be read")
		}
		for _, f := range files {
			if !seen[f.Path] && isArtifactFile(f.Path) {
				seen[f.Path] = true
				out = append(out, &archivev1.FileInfo{Path: f.Path, Version: f.Version, Staged: true})
			}
		}
	}
	slices.SortFunc(out, func(a, b *archivev1.FileInfo) int { return strings.Compare(a.GetPath(), b.GetPath()) })
	return out, nil
}

func (s *ReadService) read(ctx context.Context, p string, may mayRead) (*archivev1.ReadFileResponse, error) {
	if err := checkReadable(p); err != nil {
		return nil, err
	}
	dir := path.Dir(p)
	if dir == "." {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the path needs a directory")
	}
	areas, err := s.areas(ctx)
	if err != nil {
		return nil, err
	}
	if a, ok := areaNaming(areas, p); ok && may(a) {
		files, err := s.staging.Read(ctx, a.Session, p)
		switch {
		case errors.Is(err, storage.ErrNoArea):
			// Released in the meantime: the file lies in the artifact repository now.
		case err != nil:
			return nil, connect.NewError(connect.CodeInternal, "the staging area could not be read")
		case len(files) == 0:
			return nil, connect.NewError(connect.CodeNotFound, "the file "+p+" was removed before the release of its session")
		default:
			f := files[0]
			return &archivev1.ReadFileResponse{File: &archivev1.File{Path: f.Path, Content: f.Data, Version: f.Version, Staged: true}}, nil
		}
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

// list lists the readable files of a directory as the caller sees them.
func (s *ReadService) list(ctx context.Context, dir string, may mayRead) (*archivev1.ListFilesResponse, error) {
	if err := storage.CheckPath(dir); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	files, err := s.arch.Read(ctx, dir)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the archive could not be read")
	}
	areas, err := s.areas(ctx)
	if err != nil {
		return nil, err
	}
	// The staged files under dir of every area that the caller may read, by path. A name of such
	// an area without a file was removed before the release.
	staged := map[string]storage.File{}
	hidden := map[string]bool{}
	for _, a := range areas {
		if !may(a) || !namesUnder(a.Names, dir) {
			continue
		}
		fs, err := s.staging.Read(ctx, a.Session, dir)
		if errors.Is(err, storage.ErrNoArea) {
			continue
		}
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, "the staging area could not be read")
		}
		for _, n := range a.Names {
			hidden[n] = true
		}
		for _, f := range fs {
			staged[f.Path] = f
		}
	}
	resp := &archivev1.ListFilesResponse{}
	for _, f := range files {
		if !isArtifactFile(f.Path) {
			continue
		}
		if sf, ok := staged[f.Path]; ok {
			resp.Files = append(resp.Files, &archivev1.FileInfo{Path: f.Path, Version: sf.Version, Staged: true})
			delete(staged, f.Path)
			continue
		}
		if hidden[f.Path] {
			continue
		}
		resp.Files = append(resp.Files, &archivev1.FileInfo{Path: f.Path, Version: f.Version})
	}
	// A staged file whose stub is not written yet.
	for _, f := range staged {
		if isArtifactFile(f.Path) {
			resp.Files = append(resp.Files, &archivev1.FileInfo{Path: f.Path, Version: f.Version, Staged: true})
		}
	}
	slices.SortFunc(resp.Files, func(a, b *archivev1.FileInfo) int { return strings.Compare(a.GetPath(), b.GetPath()) })
	return resp, nil
}

// areas returns the areas of the staging repository; none when the tenant has no staging
// repository.
func (s *ReadService) areas(ctx context.Context) ([]storage.Area, error) {
	if s.staging == nil {
		return nil, nil
	}
	areas, err := s.staging.Areas(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, "the staging repository could not be read")
	}
	return areas, nil
}

// areaNaming returns the area that names path p. A path belongs to one area only.
func areaNaming(areas []storage.Area, p string) (storage.Area, bool) {
	for _, a := range areas {
		if slices.Contains(a.Names, p) {
			return a, true
		}
	}
	return storage.Area{}, false
}

// namesUnder reports whether a name lies under the directory dir.
func namesUnder(names []string, dir string) bool {
	for _, n := range names {
		if n == dir || strings.HasPrefix(n, dir+"/") {
			return true
		}
	}
	return false
}

// participant lets only the token of a participant pass. A stage link is shown on the shared
// screen, so it must not read the archive.
func participant(ctx context.Context) (tokencheck.Caller, error) {
	c, ok := tokencheck.CallerFrom(ctx)
	switch {
	case !ok:
		return c, connect.NewError(connect.CodeUnauthenticated, "no access token")
	case c.ParticipantID == "":
		return c, connect.NewError(connect.CodePermissionDenied, "a stage link cannot read the archive")
	}
	return c, nil
}

func checkReadable(p string) error {
	if err := storage.CheckPath(p); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	if !isArtifactFile(p) {
		return connect.NewError(connect.CodeInvalidArgument, "only Markdown and YAML files can be read")
	}
	return nil
}

// isArtifactFile reports whether p is a Markdown or YAML file.
func isArtifactFile(p string) bool {
	switch path.Ext(p) {
	case ".md", ".yaml", ".yml":
		return true
	}
	return false
}
