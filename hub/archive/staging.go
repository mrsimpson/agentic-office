package archive

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// areasDir holds the state of every area of the staging repository, one directory per session.
// Its name starts with a dot, so no artifact path can lie in it (storage.CheckPath), no read of a
// directory of artifacts returns it, and no watcher is told about it.
const areasDir = ".areas"

// areaStateFile is the name of the file that holds the state of an area.
const areaStateFile = "area.yaml"

// releaseTrailer is the trailer of a release commit that names the session.
const releaseTrailer = "Office-Release"

// Staging is the staging repository of a tenant together with its artifact repository. It
// implements storage.Staging.
type Staging struct {
	repo      *Repo // the staging repository
	artifacts *Repo // the artifact repository of the same tenant

	releases sync.Mutex // one release at a time
}

var _ storage.Staging = (*Staging)(nil)

// areaFile is the stored state of an area.
type areaFile struct {
	Session  string       `yaml:"session"`
	Readers  []string     `yaml:"readers,omitempty"`
	Deadline string       `yaml:"deadline,omitempty"` // RFC 3339, UTC
	Held     bool         `yaml:"held,omitempty"`
	Names    []string     `yaml:"names,omitempty"`
	Authors  []authorFile `yaml:"authors,omitempty"`
	Release  *releaseFile `yaml:"release,omitempty"`
}

type authorFile struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

type releaseFile struct {
	Author *authorFile `yaml:"author,omitempty"` // nil: the hub
	Commit string      `yaml:"commit,omitempty"`
}

// safeAreaName is the form of a session ID that is used as it is in the path of its area.
var safeAreaName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// areaName returns the name of the area of a session in paths and messages: the session ID when
// it is safe as a file name, otherwise "=" plus its base64url form without padding.
func areaName(session string) string {
	if safeAreaName.MatchString(session) {
		return session
	}
	return "=" + base64.RawURLEncoding.EncodeToString([]byte(session))
}

func areaStatePath(session string) string {
	return areasDir + "/" + areaName(session) + "/" + areaStateFile
}

func checkSession(session string) error {
	if session == "" {
		return errors.New("archive: a staging area needs a session ID")
	}
	return nil
}

func checkAuthor(a storage.Author) error {
	if a != (storage.Author{}) && (a.Name == "" || a.Email == "") {
		return errors.New("archive: an author needs a name and an email")
	}
	return nil
}

// load returns the state of the area of a session in the commit head, or nil if there is none.
func (s *Staging) load(ctx context.Context, head, session string) (*areaFile, error) {
	if head == "" {
		return nil, nil
	}
	files, err := s.repo.readAt(ctx, head, areaStatePath(session))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	return decodeArea(files[0])
}

func decodeArea(f storage.File) (*areaFile, error) {
	var a areaFile
	if err := yaml.Unmarshal(f.Data, &a); err != nil {
		return nil, fmt.Errorf("archive: state of a staging area %s: %w", f.Path, err)
	}
	return &a, nil
}

// loadAll returns the state of every area in the commit head.
func (s *Staging) loadAll(ctx context.Context, head string) ([]*areaFile, error) {
	if head == "" {
		return nil, nil
	}
	files, err := s.repo.readAt(ctx, head, areasDir)
	if err != nil {
		return nil, err
	}
	var out []*areaFile
	for _, f := range files {
		if path.Base(f.Path) != areaStateFile {
			continue
		}
		a, err := decodeArea(f)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	slices.SortFunc(out, func(x, y *areaFile) int { return strings.Compare(x.Session, y.Session) })
	return out, nil
}

// stateFile returns the file that stores the state a.
func stateFile(a *areaFile) (storage.File, error) {
	data, err := yaml.Marshal(a)
	if err != nil {
		return storage.File{}, err
	}
	return storage.File{Path: areaStatePath(a.Session), Data: data}, nil
}

// writeState commits the state of an area alone. The caller holds s.repo.turn.
func (s *Staging) writeState(ctx context.Context, a *areaFile, message string) error {
	f, err := stateFile(a)
	if err != nil {
		return err
	}
	_, err = s.repo.commitLocked(ctx, storage.Change{Message: message, Files: []storage.File{f}}, nil)
	return err
}

func (a *areaFile) public() storage.Area {
	out := storage.Area{
		Session: a.Session,
		AreaState: storage.AreaState{
			Readers: slices.Clone(a.Readers),
			Held:    a.Held,
		},
		Names: slices.Clone(a.Names),
	}
	if a.Deadline != "" {
		if t, err := time.Parse(time.RFC3339Nano, a.Deadline); err == nil {
			out.Deadline = t
		}
	}
	for _, au := range a.Authors {
		out.Authors = append(out.Authors, storage.Author{Name: au.Name, Email: au.Email})
	}
	if a.Release != nil {
		out.Release = &storage.ReleaseState{Commit: a.Release.Commit}
		if a.Release.Author != nil {
			out.Release.Author = storage.Author{Name: a.Release.Author.Name, Email: a.Release.Author.Email}
		}
	}
	return out
}

// OpenArea implements storage.Staging.
func (s *Staging) OpenArea(ctx context.Context, session string, st storage.AreaState) error {
	if err := checkSession(session); err != nil {
		return err
	}
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	head, err := s.repo.head(ctx)
	if err != nil {
		return err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return err
	}
	message := "Update the staging area of session " + areaName(session)
	if a == nil {
		a = &areaFile{Session: session}
		message = "Open the staging area of session " + areaName(session)
	} else if a.Release != nil {
		return storage.ErrAreaClosed
	}
	a.Readers = slices.Clone(st.Readers)
	a.Deadline = ""
	if !st.Deadline.IsZero() {
		a.Deadline = st.Deadline.UTC().Format(time.RFC3339Nano)
	}
	a.Held = st.Held
	return s.writeState(ctx, a, message)
}

// Area implements storage.Staging.
func (s *Staging) Area(ctx context.Context, session string) (storage.Area, error) {
	if err := checkSession(session); err != nil {
		return storage.Area{}, err
	}
	head, err := s.repo.head(ctx)
	if err != nil {
		return storage.Area{}, err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return storage.Area{}, err
	}
	if a == nil {
		return storage.Area{}, storage.ErrNoArea
	}
	return a.public(), nil
}

// Areas implements storage.Staging.
func (s *Staging) Areas(ctx context.Context) ([]storage.Area, error) {
	head, err := s.repo.head(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.loadAll(ctx, head)
	if err != nil {
		return nil, err
	}
	out := make([]storage.Area, 0, len(all))
	for _, a := range all {
		out = append(out, a.public())
	}
	return out, nil
}

// Commit implements storage.Staging.
func (s *Staging) Commit(ctx context.Context, session string, c storage.Change) (string, error) {
	if err := checkSession(session); err != nil {
		return "", err
	}
	if err := checkChange(c); err != nil {
		return "", err
	}
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	head, err := s.repo.head(ctx)
	if err != nil {
		return "", err
	}
	all, err := s.loadAll(ctx, head)
	if err != nil {
		return "", err
	}
	var a *areaFile
	owner := map[string]string{} // path -> session of the area that names it
	for _, x := range all {
		if x.Session == session {
			a = x
		}
		for _, n := range x.Names {
			owner[n] = x.Session
		}
	}
	if a == nil {
		return "", storage.ErrNoArea
	}
	if a.Release != nil {
		return "", storage.ErrAreaClosed
	}
	for _, f := range c.Files {
		if o, ok := owner[f.Path]; ok && o != session {
			cur, err := s.repo.rev(ctx, head+":"+f.Path)
			if err != nil {
				return "", err
			}
			return "", &storage.ConflictError{Path: f.Path, Current: cur}
		}
		if f.Remove && owner[f.Path] != session {
			return "", fmt.Errorf("archive: %s is not in the staging area of session %s", f.Path, areaName(session))
		}
		if !f.Remove && owner[f.Path] == "" {
			// A file outside every area cannot be in the staging repository, but check anyway.
			if cur, err := s.repo.rev(ctx, head+":"+f.Path); err != nil {
				return "", err
			} else if cur != "" {
				return "", &storage.ConflictError{Path: f.Path, Current: cur}
			}
			a.Names = append(a.Names, f.Path)
		}
	}
	slices.Sort(a.Names)
	if au := c.Author; au != (storage.Author{}) && !slices.Contains(a.Authors, authorFile(au)) {
		a.Authors = append(a.Authors, authorFile(au))
	}
	state, err := stateFile(a)
	if err != nil {
		return "", err
	}
	change := c
	change.Files = append(slices.Clone(c.Files), state)
	return s.repo.commitLocked(ctx, change, nil)
}

// TakeBack implements storage.Staging.
func (s *Staging) TakeBack(ctx context.Context, session, p string) error {
	if err := checkSession(session); err != nil {
		return err
	}
	if err := storage.CheckPath(p); err != nil {
		return err
	}
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	head, err := s.repo.head(ctx)
	if err != nil {
		return err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return err
	}
	if a == nil {
		return storage.ErrNoArea
	}
	if a.Release != nil {
		return storage.ErrAreaClosed
	}
	i, ok := slices.BinarySearch(a.Names, p)
	if !ok {
		return nil
	}
	a.Names = slices.Delete(a.Names, i, i+1)
	state, err := stateFile(a)
	if err != nil {
		return err
	}
	_, err = s.repo.commitLocked(ctx, storage.Change{
		Message: "Take " + p + " back from the staging area of session " + areaName(session),
		Files:   []storage.File{{Path: p, Remove: true}, state},
	}, nil)
	return err
}

// Read implements storage.Staging.
func (s *Staging) Read(ctx context.Context, session, dir string) ([]storage.File, error) {
	if err := checkSession(session); err != nil {
		return nil, err
	}
	if err := storage.CheckPath(dir); err != nil {
		return nil, err
	}
	head, err := s.repo.head(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, storage.ErrNoArea
	}
	files, err := s.repo.readAt(ctx, head, dir)
	if err != nil {
		return nil, err
	}
	out := files[:0]
	for _, f := range files {
		if _, ok := slices.BinarySearch(a.Names, f.Path); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// List implements storage.Staging.
func (s *Staging) List(ctx context.Context, session string) ([]string, error) {
	if err := checkSession(session); err != nil {
		return nil, err
	}
	head, err := s.repo.head(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, storage.ErrNoArea
	}
	return s.present(ctx, head, a.Names)
}

// present returns the names that have a file in the commit head of the staging repository.
func (s *Staging) present(ctx context.Context, head string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	list, err := s.repo.run(ctx, append([]string{"ls-tree", "-r", "-z", "--name-only", "--full-tree", head, "--"}, names...)...)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range strings.Split(list, "\x00") {
		if p != "" && slices.Contains(names, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Release implements storage.Staging.
func (s *Staging) Release(ctx context.Context, session string, author storage.Author) (string, error) {
	if err := checkSession(session); err != nil {
		return "", err
	}
	if err := checkAuthor(author); err != nil {
		return "", err
	}
	s.releases.Lock()
	defer s.releases.Unlock()
	a, err := s.close(ctx, session, author)
	if err != nil {
		return "", err
	}
	id, err := s.move(ctx, a)
	if err != nil {
		return "", err
	}
	if err := s.cleanUp(ctx, session); err != nil {
		return "", err
	}
	return id, nil
}

// close is step 1 of a release: it closes the area for writes and records the author. An area
// that is closed already keeps its author.
func (s *Staging) close(ctx context.Context, session string, author storage.Author) (*areaFile, error) {
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	head, err := s.repo.head(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.load(ctx, head, session)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, storage.ErrNoArea
	}
	if a.Release != nil {
		return a, nil
	}
	a.Release = &releaseFile{}
	if author != (storage.Author{}) {
		a.Release.Author = &authorFile{Name: author.Name, Email: author.Email}
	}
	if err := s.writeState(ctx, a, "Close the staging area of session "+areaName(session)+" for its release"); err != nil {
		return nil, err
	}
	return a, nil
}

// move is step 2 of a release: it writes the files of the area into the artifact repository in
// one commit and returns its ID, or "" when there was nothing to move. A release commit that is
// recorded and already lies in the history of main is not made again.
func (s *Staging) move(ctx context.Context, a *areaFile) (string, error) {
	if a.Release.Commit != "" {
		landed, err := s.artifacts.onMain(ctx, a.Release.Commit)
		if err != nil || landed {
			return a.Release.Commit, err
		}
	}
	head, err := s.repo.head(ctx)
	if err != nil {
		return "", err
	}
	// The area is closed, so its files do not change any more. Read them as they are.
	var files []storage.File
	if len(a.Names) > 0 {
		if files, err = s.repo.readAt(ctx, head, a.Names...); err != nil {
			return "", err
		}
	}
	var change storage.Change
	have := map[string]bool{}
	for _, f := range files {
		if _, ok := slices.BinarySearch(a.Names, f.Path); !ok {
			continue
		}
		have[f.Path] = true
		change.Files = append(change.Files, storage.File{Path: f.Path, Data: f.Data})
	}
	// The stub of a name whose file was removed from the area goes away.
	for _, n := range a.Names {
		if !have[n] {
			change.Files = append(change.Files, storage.File{Path: n, Remove: true})
		}
	}
	slices.SortFunc(change.Files, func(x, y storage.File) int { return strings.Compare(x.Path, y.Path) })
	var by *storage.Author
	if ra := a.Release.Author; ra != nil {
		change.Author = storage.Author{Name: ra.Name, Email: ra.Email}
		by = &change.Author
	}
	var msg strings.Builder
	fmt.Fprintf(&msg, "Release session %s\n\n", areaName(a.Session))
	for _, au := range a.Authors {
		if by != nil && au.Name == by.Name && au.Email == by.Email {
			continue
		}
		fmt.Fprintf(&msg, "Co-authored-by: %s <%s>\n", au.Name, au.Email)
	}
	fmt.Fprintf(&msg, "%s: %s\n", releaseTrailer, areaName(a.Session))
	change.Message = msg.String()

	s.artifacts.turn.Lock()
	defer s.artifacts.turn.Unlock()
	built := ""
	id, err := s.artifacts.commitLocked(ctx, change, func(id string) error {
		// Record the commit before main moves to it: after a restart, the release looks for it
		// in the history of main and never makes it twice.
		built = id
		return s.record(ctx, a, id)
	})
	if err != nil {
		return "", err
	}
	if id != built {
		return "", nil // the area changed nothing in the artifact repository
	}
	return id, nil
}

// record writes the ID of the release commit into the state of the area.
func (s *Staging) record(ctx context.Context, a *areaFile, id string) error {
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	a.Release.Commit = id
	return s.writeState(ctx, a, "Record the release commit of session "+areaName(a.Session))
}

// cleanUp is step 3 of a release: it removes the files and the state of the area from the
// staging repository in one commit.
func (s *Staging) cleanUp(ctx context.Context, session string) error {
	s.repo.turn.Lock()
	defer s.repo.turn.Unlock()
	head, err := s.repo.head(ctx)
	if err != nil {
		return err
	}
	a, err := s.load(ctx, head, session)
	if err != nil || a == nil {
		return err
	}
	files := []storage.File{{Path: areaStatePath(session), Remove: true}}
	for _, n := range a.Names {
		files = append(files, storage.File{Path: n, Remove: true})
	}
	_, err = s.repo.commitLocked(ctx, storage.Change{
		Message: "Clean up the staging area of session " + areaName(session) + " after its release",
		Files:   files,
	}, nil)
	return err
}

// onMain reports whether the commit id lies in the history of main.
func (r *Repo) onMain(ctx context.Context, id string) (bool, error) {
	_, err := r.run(ctx, "merge-base", "--is-ancestor", id, "refs/heads/main")
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	// A commit that main never reached may be missing; that is the same answer.
	if exitCode(err) == 128 {
		if ok, _ := r.rev(ctx, id+"^{commit}"); ok == "" {
			return false, nil
		}
	}
	return false, err
}

// hubEmail is the e-mail of the hub as the author of a commit (see Repo.runEnv).
const hubEmail = "office-hub@localhost"

// coAuthorLine is a co-author in the message of a release commit.
var coAuthorLine = regexp.MustCompile(`^Co-authored-by: (.+) <([^<>]*)>$`)

// Released implements storage.Staging. It reads the newest commit on main of the artifact
// repository whose trailer names the session.
func (s *Staging) Released(ctx context.Context, session string) (storage.ReleaseCommit, error) {
	if err := checkSession(session); err != nil {
		return storage.ReleaseCommit{}, err
	}
	trailer := releaseTrailer + ": " + areaName(session)
	out, err := s.artifacts.run(ctx, "log", "refs/heads/main", "-F", "--grep="+trailer,
		"--format=%H%x00%an%x00%ae%x00%aI%x00%B%x1e")
	if err != nil {
		return storage.ReleaseCommit{}, err
	}
	for _, rec := range strings.Split(out, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 5)
		if len(fields) != 5 {
			continue
		}
		lines := strings.Split(strings.TrimSpace(fields[4]), "\n")
		if !slices.Contains(lines, trailer) {
			continue // the trailer names another session that starts the same way
		}
		rc := storage.ReleaseCommit{ID: fields[0]}
		if fields[2] != hubEmail {
			rc.Author = storage.Author{Name: fields[1], Email: fields[2]}
		}
		if rc.Time, err = time.Parse(time.RFC3339, fields[3]); err != nil {
			return storage.ReleaseCommit{}, fmt.Errorf("archive: the time of the release commit %s: %w", rc.ID, err)
		}
		for _, l := range lines {
			if m := coAuthorLine.FindStringSubmatch(l); m != nil {
				rc.CoAuthors = append(rc.CoAuthors, storage.Author{Name: m[1], Email: m[2]})
			}
		}
		paths, err := s.artifacts.run(ctx, "diff-tree", "--no-commit-id", "-r", "--name-only", "-z", "--diff-filter=AMT", rc.ID)
		if err != nil {
			return storage.ReleaseCommit{}, err
		}
		for _, p := range strings.Split(paths, "\x00") {
			if p != "" {
				rc.Paths = append(rc.Paths, p)
			}
		}
		slices.Sort(rc.Paths)
		return rc, nil
	}
	return storage.ReleaseCommit{}, storage.ErrNotReleased
}
