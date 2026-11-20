package records

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
	"github.com/mrsimpson/agentic-office/hub/records/check"
)

// record is what every record type has.
type record interface {
	proto.Message
	GetId() uint32
	GetCommandId() string
	GetSessionId() string
}

// maxCreateAttempts is how often a create takes the next ID again because a file of that ID was
// already in the archive. Every attempt means that a push put a file there in the meantime.
const maxCreateAttempts = 50

// StubMessage is the commit message of every stub. It says nothing about the record.
const StubMessage = "Stub: an artifact waits for the release of its session"

// released is a file of the artifact repository: a record, or the stub of a staged record.
type released[T record] struct {
	rec     T // zero for a stub
	stub    bool
	version string // "" when the hub wrote it and has not read it back
}

// staged is a file of the staging repository: a record that waits for the release of its
// session.
type staged[T record] struct {
	rec     T
	session string
	version string
}

// entry is one path of the collection as the clients see it, before the reader check.
type entry[T record] struct {
	id      uint32
	rec     T      // the released or the staged record; zero for a stub
	session string // the session of a staged record or of a removed staged record
	state   entryState
}

type entryState int

const (
	entryReleased entryState = iota + 1
	entryStaged              // a staged record; readers of its session see it
	entryRemoved             // a staged record that was removed; its stub stays until the release
	entryStub                // a stub whose staged record the part does not know
)

// changeCmd is what a change or a removal did, for its command_id.
type changeCmd struct {
	id     uint32
	digest string
}

// collection is the list of records of one type. It keeps all records in memory: the released
// ones from the artifact repository and the staged ones from the staging repository
// (concept-session-release). It writes every new record as one file and one commit: a YAML file,
// or a Markdown file with front matter for a note. A staged record goes into the staging area of
// its session, and its stub into the artifact repository. It reloads the files that a commit or a
// push changed (see reloadPaths).
type collection[T record] struct {
	kind    string // for messages, for example "decision"
	title   string // for commit messages, for example "Decision"
	dir     string // directory in the archive, for example "decisions"
	schema  string // the schema that every file of the collection names, for example "office.decision.v1"
	ext     string // the ending of the files, for example ".yaml"
	encode  func(schema string, m proto.Message) ([]byte, error)
	arch    storage.Archive
	staging storage.Staging
	newT    func() T
	logf    func(string, ...any)

	mu      sync.Mutex             // one write at a time; guards everything below
	art     map[string]released[T] // the files of the artifact repository by path
	stage   map[string]staged[T]   // the files of the staging repository by path
	names   map[string]string      // path -> session of the area that names it
	cmds    map[string]string      // command_id to the path of its record
	changes map[string]changeCmd   // command_id of a change or a removal
	bad     map[string]string      // path (with a prefix for the repository) to the version that was ignored and logged
	high    uint32                 // the highest number of any file ever seen, valid or not
	view    []entry[T]             // the entries ordered by ID; nil while stale. Never changed in place.
}

// newCollection returns an empty collection. encode writes the file of a record (marshalYAML
// or marshalMarkdown).
func newCollection[T record](arch storage.Archive, staging storage.Staging, kind, title, dir, schema string,
	encode func(string, proto.Message) ([]byte, error), newT func() T, logf func(string, ...any)) *collection[T] {
	return &collection[T]{kind: kind, title: title, dir: dir, schema: schema, ext: check.Ext(schema), encode: encode,
		arch: arch, staging: staging, newT: newT, logf: logf,
		art: map[string]released[T]{}, stage: map[string]staged[T]{}, names: map[string]string{},
		cmds: map[string]string{}, changes: map[string]changeCmd{}, bad: map[string]string{}}
}

// The fields of the contract that the hub sets in a response and never stores.
const (
	fieldStaged = "staged"
	fieldStub   = "stub"
)

// setFlag returns a copy of r with the bool field name set.
func setFlag[T record](r T, name string) T {
	c := proto.Clone(r).(T)
	m := c.ProtoReflect()
	m.Set(m.Descriptor().Fields().ByName(protoreflect.Name(name)), protoreflect.ValueOfBool(true))
	return c
}

// plain clears the fields that are never stored.
func plain[T record](r T) T {
	m := r.ProtoReflect()
	for _, name := range []string{fieldStaged, fieldStub} {
		m.Clear(m.Descriptor().Fields().ByName(protoreflect.Name(name)))
	}
	return r
}

func (c *collection[T]) setID(r T, id uint32) {
	m := r.ProtoReflect()
	m.Set(m.Descriptor().Fields().ByName("id"), protoreflect.ValueOfUint32(id))
}

// stubOf returns what a client that may not read a staged record sees: its ID and stub.
func (c *collection[T]) stubOf(id uint32) T {
	r := c.newT()
	c.setID(r, id)
	return setFlag(r, fieldStub)
}

// numberOf returns the number in the name of a record file such as "decisions/000042.yaml", and
// false if the name is not a number with the ending of the collection.
func (c *collection[T]) numberOf(p string) (uint32, bool) {
	if path.Dir(p) != c.dir {
		return 0, false
	}
	base, ok := strings.CutSuffix(path.Base(p), c.ext)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(base, 10, 32)
	return uint32(n), err == nil
}

// recordAt returns the record that a path holds: the released one, else the staged one.
func (c *collection[T]) recordAt(p string) (T, bool) {
	if a, ok := c.art[p]; ok && !a.stub {
		return a.rec, true
	}
	if s, ok := c.stage[p]; ok {
		return s.rec, true
	}
	var zero T
	return zero, false
}

// unindex removes the command_id of a record that left path p, unless p still holds a record
// with it. The caller holds c.mu.
func (c *collection[T]) unindex(p, cid string) {
	if c.cmds[cid] != p {
		return
	}
	if r, ok := c.recordAt(p); ok && r.GetCommandId() == cid {
		return
	}
	delete(c.cmds, cid)
}

// setArt sets the file of the artifact repository at p. The caller holds c.mu.
func (c *collection[T]) setArt(p string, f released[T]) {
	old, had := c.art[p]
	c.art[p] = f
	if had && !old.stub {
		c.unindex(p, old.rec.GetCommandId())
	}
	if !f.stub {
		c.cmds[f.rec.GetCommandId()] = p
	}
	delete(c.bad, "art:"+p)
	c.view = nil
}

// dropArt forgets the file of the artifact repository at p. The caller holds c.mu.
func (c *collection[T]) dropArt(p string) {
	old, had := c.art[p]
	delete(c.art, p)
	delete(c.bad, "art:"+p)
	if had && !old.stub {
		c.unindex(p, old.rec.GetCommandId())
	}
	c.view = nil
}

// setStaged sets the staged file at p. The caller holds c.mu.
func (c *collection[T]) setStaged(p string, f staged[T]) {
	old, had := c.stage[p]
	c.stage[p] = f
	c.names[p] = f.session
	if had {
		c.unindex(p, old.rec.GetCommandId())
	}
	c.cmds[f.rec.GetCommandId()] = p
	delete(c.bad, "stage:"+p)
	c.view = nil
}

// dropStaged forgets the staged file at p. The caller holds c.mu.
func (c *collection[T]) dropStaged(p string) {
	old, had := c.stage[p]
	delete(c.stage, p)
	delete(c.bad, "stage:"+p)
	if had {
		c.unindex(p, old.rec.GetCommandId())
	}
	c.view = nil
}

// parse checks a record file with the check of the part and reads it. The caller holds c.mu.
func (c *collection[T]) parse(f storage.File) (T, error) {
	var zero T
	if reasons := check.Validate(c.schema, f.Path, f.Data); len(reasons) > 0 {
		return zero, fmt.Errorf("%s: %s", f.Path, strings.Join(reasons, "; "))
	}
	r := c.newT()
	if err := decode(c.schema, f.Data, r); err != nil {
		return zero, fmt.Errorf("%s: %w", f.Path, err)
	}
	if p, dup := c.cmds[r.GetCommandId()]; dup && p != f.Path {
		return zero, fmt.Errorf("%s: command_id of %s %s", f.Path, c.kind, p)
	}
	return plain(r), nil
}

// load reads one file of the artifact repository into the collection: a record or a stub. It
// returns an error and changes nothing if the file is not valid. The caller holds c.mu. A file
// that is not valid still counts for the next ID, so the part never takes its number. A file whose
// name is not a number with the ending of the collection is not a record, for example a page in
// the directory, and is ignored.
func (c *collection[T]) load(f storage.File) error {
	n, ok := c.numberOf(f.Path)
	if !ok {
		return nil
	}
	c.high = max(c.high, n)
	if a, ok := c.art[f.Path]; ok && a.version == f.Version {
		return nil
	}
	if check.IsStub(c.schema, f.Data) {
		c.setArt(f.Path, released[T]{stub: true, version: f.Version})
		return nil
	}
	r, err := c.parse(f)
	if err != nil {
		return err
	}
	c.setArt(f.Path, released[T]{rec: r, version: f.Version})
	return nil
}

// loadStaged reads one staged file of a session into the collection. A stub is never staged.
func (c *collection[T]) loadStaged(session string, f storage.File) error {
	n, ok := c.numberOf(f.Path)
	if !ok {
		return nil
	}
	c.high = max(c.high, n)
	c.names[f.Path] = session
	if s, ok := c.stage[f.Path]; ok && s.version == f.Version && s.session == session {
		return nil
	}
	if check.IsStub(c.schema, f.Data) {
		return fmt.Errorf("%s: a stub lies in the staging area of session %s", f.Path, session)
	}
	r, err := c.parse(f)
	if err != nil {
		return err
	}
	c.setStaged(f.Path, staged[T]{rec: r, session: session, version: f.Version})
	return nil
}

// lenient logs an error of a file once for its version. key starts with "art:" for a file of the
// artifact repository, which a push brought, and with "stage:" for a staged file.
func (c *collection[T]) lenient(key, version string, err error) {
	if err != nil && c.bad[key] != version {
		c.bad[key] = version
		if strings.HasPrefix(key, "art:") {
			c.logf("records: ignore the pushed file %v", err)
		} else {
			c.logf("records: ignore the staged file %v", err)
		}
	}
}

// loadAll reads every file of the directory, in the artifact repository and in every staging
// area. With strict, the first file that is not valid fails it (the start of the hub). Otherwise
// such a file is logged and ignored, and a record whose file is gone is removed. A staged record
// without a stub, which a crash between the two commits leaves, gets its stub.
func (c *collection[T]) loadAll(ctx context.Context, strict bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.loadArt(ctx, strict); err != nil {
		return err
	}
	if err := c.loadStage(ctx, strict); err != nil {
		return err
	}
	for p := range c.stage {
		if _, ok := c.art[p]; ok {
			continue
		}
		if _, err := c.arch.Commit(context.WithoutCancel(ctx), storage.Change{Message: StubMessage,
			Files: []storage.File{{Path: p, Data: check.Stub(c.schema), Base: storage.NoFile}}}); err != nil {
			c.logf("records: write the missing stub of %s: %v", p, err)
			continue
		}
		c.setArt(p, released[T]{stub: true})
	}
	return nil
}

// loadArt reads the directory of the artifact repository. The caller holds c.mu.
func (c *collection[T]) loadArt(ctx context.Context, strict bool) error {
	files, err := c.arch.Read(ctx, c.dir)
	if err != nil {
		return fmt.Errorf("records: read %s: %w", c.dir, err)
	}
	// The next ID never goes below a number that ever lay in the directory, also not below the
	// number of a file that a push removed.
	names, err := c.arch.Names(ctx, c.dir)
	if err != nil {
		return fmt.Errorf("records: list the names in %s: %w", c.dir, err)
	}
	for _, p := range names {
		if n, ok := c.numberOf(p); ok {
			c.high = max(c.high, n)
		}
	}
	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
		err := c.load(f)
		if strict && err != nil {
			return fmt.Errorf("records: %w", err)
		}
		c.lenient("art:"+f.Path, f.Version, err)
	}
	for p := range c.art {
		if !present[p] {
			c.dropArt(p)
		}
	}
	return nil
}

// loadStage reads the directory in every staging area. The caller holds c.mu.
func (c *collection[T]) loadStage(ctx context.Context, strict bool) error {
	areas, err := c.staging.Areas(ctx)
	if err != nil {
		return fmt.Errorf("records: read the staging areas: %w", err)
	}
	present := map[string]bool{}
	c.names = map[string]string{}
	for _, a := range areas {
		for _, p := range a.Names {
			if n, ok := c.numberOf(p); ok {
				c.high = max(c.high, n)
				c.names[p] = a.Session
			}
		}
		files, err := c.staging.Read(ctx, a.Session, c.dir)
		if errors.Is(err, storage.ErrNoArea) {
			continue // released in the meantime
		}
		if err != nil {
			return fmt.Errorf("records: read %s in the staging area of %s: %w", c.dir, a.Session, err)
		}
		for _, f := range files {
			present[f.Path] = true
			err := c.loadStaged(a.Session, f)
			if strict && err != nil {
				return fmt.Errorf("records: %w", err)
			}
			c.lenient("stage:"+f.Path, f.Version, err)
		}
	}
	for p := range c.stage {
		if !present[p] {
			c.dropStaged(p)
		}
	}
	c.view = nil
	return nil
}

// reloadArt reads the files at the paths of the artifact repository again from the current
// commit: a file that is there is loaded, a file that is gone is removed. The caller holds c.mu.
func (c *collection[T]) reloadArt(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if _, ok := c.numberOf(p); !ok {
			continue
		}
		files, err := c.arch.Read(ctx, p)
		if err != nil {
			return fmt.Errorf("records: read %s: %w", p, err)
		}
		if len(files) == 0 {
			c.dropArt(p)
			continue
		}
		for _, f := range files {
			c.lenient("art:"+f.Path, f.Version, c.load(f))
		}
	}
	return nil
}

// reloadStaged reads the staged files at the paths again. A path that no area names any more
// was released or never staged. The caller holds c.mu.
func (c *collection[T]) reloadStaged(ctx context.Context, paths []string) error {
	refreshed := false
	for _, p := range paths {
		if _, ok := c.numberOf(p); !ok {
			continue
		}
		session, ok := c.names[p]
		if !ok && !refreshed {
			// A path of another writer or of an area the part has not seen yet.
			areas, err := c.staging.Areas(ctx)
			if err != nil {
				return fmt.Errorf("records: read the staging areas: %w", err)
			}
			refreshed = true
			for _, a := range areas {
				for _, n := range a.Names {
					if _, ok := c.numberOf(n); ok {
						c.names[n] = a.Session
					}
				}
			}
			session, ok = c.names[p]
		}
		if !ok {
			c.dropStaged(p)
			continue
		}
		files, err := c.staging.Read(ctx, session, p)
		if errors.Is(err, storage.ErrNoArea) {
			delete(c.names, p)
			c.dropStaged(p)
			continue
		}
		if err != nil {
			return fmt.Errorf("records: read %s in the staging area of %s: %w", p, session, err)
		}
		if len(files) == 0 {
			c.dropStaged(p)
			continue
		}
		for _, f := range files {
			c.lenient("stage:"+f.Path, f.Version, c.loadStaged(session, f))
		}
	}
	return nil
}

// paths returns the paths of the changes under the directory of the collection.
func (c *collection[T]) paths(changes []watch.Change) []string {
	var out []string
	for _, ch := range changes {
		if strings.HasPrefix(ch.Path, c.dir+"/") {
			out = append(out, ch.Path)
		}
	}
	return out
}

// reload is reloadArt for the changes of a push or a commit in the artifact repository.
func (c *collection[T]) reload(ctx context.Context, changes []watch.Change) error {
	paths := c.paths(changes)
	if len(paths) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadArt(ctx, paths)
}

// reloadStage is reloadStaged for the changes of a commit in the staging repository.
func (c *collection[T]) reloadStage(ctx context.Context, changes []watch.Change) error {
	paths := c.paths(changes)
	if len(paths) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadStaged(ctx, paths)
}

// loadAllStaged reads every staged file again, after the staging watcher was dropped.
func (c *collection[T]) loadAllStaged(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadStage(ctx, false)
}

// entries returns the entries ordered by ID. The slice is never changed afterwards, so the
// caller may use it without c.mu. The caller holds c.mu.
func (c *collection[T]) entries() []entry[T] {
	if c.view != nil {
		return c.view
	}
	view := make([]entry[T], 0, len(c.art)+len(c.stage))
	add := func(p string) {
		n, _ := c.numberOf(p)
		a, inArt := c.art[p]
		s, inStage := c.stage[p]
		switch {
		case inArt && !a.stub:
			view = append(view, entry[T]{id: a.rec.GetId(), rec: a.rec, state: entryReleased})
		case inStage:
			view = append(view, entry[T]{id: n, rec: s.rec, session: s.session, state: entryStaged})
		case c.names[p] != "":
			view = append(view, entry[T]{id: n, session: c.names[p], state: entryRemoved})
		default:
			view = append(view, entry[T]{id: n, state: entryStub})
		}
	}
	for p := range c.art {
		add(p)
	}
	for p := range c.stage {
		if _, ok := c.art[p]; !ok {
			add(p)
		}
	}
	// The paths sort by ID only up to six digits, so sort by ID.
	slices.SortFunc(view, func(a, b entry[T]) int { return cmp.Compare(a.id, b.id) })
	c.view = view
	return view
}

// snapshot returns the entries for a read.
func (c *collection[T]) snapshot() []entry[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries()
}

// path returns the file of the record with this ID.
func (c *collection[T]) path(id uint32) string { return check.FilePath(c.schema, id) }

// target is where a write goes (concept-session-release).
type target struct {
	// session is the staging area of the write; empty means the artifact repository.
	session string
	// author is the author of the change in the staging area.
	author storage.Author
}

// found returns the stored record of a command, or false.
func (c *collection[T]) found(cid string) (T, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byCommand(cid)
}

// byCommand returns the record of a command and whether it is staged. The caller holds c.mu.
func (c *collection[T]) byCommand(cid string) (T, bool, bool) {
	var zero T
	p, ok := c.cmds[cid]
	if !ok {
		return zero, false, false
	}
	if a, ok := c.art[p]; ok && !a.stub {
		return a.rec, false, true
	}
	if s, ok := c.stage[p]; ok {
		return s.rec, true, true
	}
	return zero, false, false
}

// shown returns a record as a client sees it: a staged record has staged set.
func shown[T record](r T, isStaged bool) T {
	if isStaged {
		return setFlag(r, fieldStaged)
	}
	return r
}

// storeErr maps an error of the archive to the error of a write.
func (c *collection[T]) storeErr(err error) error {
	switch {
	case errors.Is(err, storage.ErrNoArea):
		return connect.NewError(connect.CodeFailedPrecondition,
			"the release status of the session and its staging area disagree: the session has no staging area")
	case errors.Is(err, storage.ErrAreaClosed):
		return connect.NewError(connect.CodeUnavailable, "the session is being released; repeat the command")
	}
	return connect.NewError(connect.CodeInternal, "store the "+c.kind+" in the archive: "+err.Error())
}

// add stores r as the next record, or returns the stored record of the same command and true.
// same reports whether the stored record has the content of r. The commit message of a released
// record is the title, the ID and the first line of text. A record for a staging area goes into
// the area, and then its stub into the artifact repository (concept-session-release).
//
// A file is created with "must not exist yet". If a push put a file with this ID there first,
// add reads the file, takes the next free ID and tries again: the caller sees no error.
func (c *collection[T]) add(ctx context.Context, r T, same func(stored, r T) bool, text string, to target) (T, bool, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	// A cancelled commit could leave the repository locked, so the commits are not cancelled
	// with the call.
	ctx = context.WithoutCancel(ctx)
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		if prev, isStaged, ok := c.byCommand(r.GetCommandId()); ok {
			if !same(prev, r) {
				return zero, false, connect.NewError(connect.CodeAlreadyExists,
					"command_id was already used for another "+c.kind)
			}
			return shown(prev, isStaged), true, nil
		}
		id := c.high + 1
		c.setID(r, id)
		p := c.path(id)
		data, err := c.encode(c.schema, r)
		if err != nil {
			return zero, false, connect.NewError(connect.CodeInternal, "encode the "+c.kind+": "+err.Error())
		}
		if reasons := check.Validate(c.schema, p, data); len(reasons) > 0 {
			return zero, false, connect.NewError(connect.CodeInternal, "the "+c.kind+" is not valid: "+strings.Join(reasons, "; "))
		}
		file := []storage.File{{Path: p, Data: data, Base: storage.NoFile}}
		message := fmt.Sprintf("%s %d: %s", c.title, id, summary(text))
		if to.session == "" {
			_, err = c.arch.Commit(ctx, storage.Change{Message: message, Files: file})
			if storage.IsConflict(err) {
				// A push came first. Read what is there; its number is taken from now on.
				if err := c.reloadArt(ctx, []string{p}); err != nil {
					return zero, false, connect.NewError(connect.CodeInternal, err.Error())
				}
				c.high = max(c.high, id)
				continue
			}
			if err != nil {
				return zero, false, c.storeErr(err)
			}
			c.high = id
			c.setArt(p, released[T]{rec: r})
			return r, false, nil
		}
		// The file goes into the area first, so that the area knows the name, then the stub.
		_, err = c.staging.Commit(ctx, to.session, storage.Change{Message: message, Author: to.author, Files: file})
		if storage.IsConflict(err) {
			if err := c.reloadArt(ctx, []string{p}); err != nil {
				return zero, false, connect.NewError(connect.CodeInternal, err.Error())
			}
			c.high = max(c.high, id)
			continue
		}
		if err != nil {
			return zero, false, c.storeErr(err)
		}
		c.high = id
		c.names[p] = to.session
		_, err = c.arch.Commit(ctx, storage.Change{Message: StubMessage,
			Files: []storage.File{{Path: p, Data: check.Stub(c.schema), Base: storage.NoFile}}})
		if err != nil {
			// Take the staged file back. A push that put a file at this path first keeps it.
			if _, rerr := c.staging.Commit(ctx, to.session, storage.Change{Message: fmt.Sprintf("%s %d: taken back", c.title, id),
				Files: []storage.File{{Path: p, Remove: true}}}); rerr != nil {
				c.logf("records: take back the staged %s: %v", p, rerr)
			}
			if storage.IsConflict(err) {
				if err := c.reloadArt(ctx, []string{p}); err != nil {
					return zero, false, connect.NewError(connect.CodeInternal, err.Error())
				}
				continue
			}
			return zero, false, c.storeErr(err)
		}
		c.setStaged(p, staged[T]{rec: r, session: to.session})
		c.setArt(p, released[T]{stub: true})
		return shown(r, true), false, nil
	}
	return zero, false, connect.NewError(connect.CodeInternal, "store the "+c.kind+" in the archive: too many files were pushed meanwhile; repeat the command")
}

// summary returns the first line of a text, short enough for a commit message.
func summary(text string) string {
	t, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if rs := []rune(t); len(rs) > 60 {
		t = string(rs[:60]) + "…"
	}
	return t
}

// where tells what a record is, for a change or a removal.
type where struct {
	state   entryState // 0: no such record
	session string     // of a staged or removed record
}

// locate returns what the record with this ID is.
func (c *collection[T]) locate(id uint32) where {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.path(id)
	if a, ok := c.art[p]; ok && !a.stub {
		return where{state: entryReleased}
	}
	if s, ok := c.stage[p]; ok {
		return where{state: entryStaged, session: s.session}
	}
	if sid := c.names[p]; sid != "" {
		return where{state: entryRemoved, session: sid}
	}
	if _, ok := c.art[p]; ok {
		return where{state: entryStub}
	}
	return where{}
}

// changed returns the result of an earlier change or removal of the command, and false if there
// was none. A command_id that was used with other content is AlreadyExists.
func (c *collection[T]) changed(cid, digest string) (T, bool, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.changes[cid]
	if !ok {
		return zero, false, nil
	}
	if prev.digest != digest {
		return zero, false, connect.NewError(connect.CodeAlreadyExists, "command_id was already used for another change")
	}
	p := c.path(prev.id)
	if a, ok := c.art[p]; ok && !a.stub {
		return a.rec, true, nil
	}
	if s, ok := c.stage[p]; ok {
		return shown(s.rec, true), true, nil
	}
	return zero, true, nil
}

// change writes a new version of a staged record of a session, or removes it when mutate is nil.
// The caller has checked the session; change checks again that the record is staged there.
func (c *collection[T]) change(ctx context.Context, id uint32, session, cid, digest string, author storage.Author,
	mutate func(old T) T) (T, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	p := c.path(id)
	s, ok := c.stage[p]
	if !ok || s.session != session {
		return zero, connect.NewError(connect.CodeAborted, "the "+c.kind+" changed meanwhile; repeat the command")
	}
	base := s.version
	if base == "" {
		files, err := c.staging.Read(ctx, session, p)
		if err != nil {
			return zero, c.storeErr(err)
		}
		if len(files) != 1 {
			return zero, connect.NewError(connect.CodeAborted, "the "+c.kind+" changed meanwhile; repeat the command")
		}
		base = files[0].Version
	}
	file := storage.File{Path: p, Base: base}
	var r T
	message := fmt.Sprintf("%s %d removed", c.title, id)
	if mutate == nil {
		file.Remove = true
	} else {
		r = mutate(proto.Clone(s.rec).(T))
		data, err := c.encode(c.schema, r)
		if err != nil {
			return zero, connect.NewError(connect.CodeInternal, "encode the "+c.kind+": "+err.Error())
		}
		if reasons := check.Validate(c.schema, p, data); len(reasons) > 0 {
			return zero, connect.NewError(connect.CodeInternal, "the "+c.kind+" is not valid: "+strings.Join(reasons, "; "))
		}
		file.Data = data
		message = fmt.Sprintf("%s %d changed", c.title, id)
	}
	_, err := c.staging.Commit(ctx, session, storage.Change{Message: message, Author: author, Files: []storage.File{file}})
	if storage.IsConflict(err) {
		c.lenient("stage:"+p, "", c.reloadStaged(ctx, []string{p}))
		return zero, connect.NewError(connect.CodeAborted, "the "+c.kind+" changed meanwhile; repeat the command")
	}
	if err != nil {
		return zero, c.storeErr(err)
	}
	c.changes[cid] = changeCmd{id: id, digest: digest}
	if mutate == nil {
		c.dropStaged(p)
		return zero, nil
	}
	c.setStaged(p, staged[T]{rec: r, session: session})
	return shown(r, true), nil
}

// reader tells whether the caller may read the staged records of a session.
type reader func(session string) bool

// visible returns an entry as the caller sees it, and false if the caller does not see it.
func (c *collection[T]) visible(e entry[T], may reader) (T, bool) {
	var zero T
	switch e.state {
	case entryReleased:
		return e.rec, true
	case entryStaged:
		if may(e.session) {
			return shown(e.rec, true), true
		}
	case entryRemoved:
		if may(e.session) {
			return zero, false
		}
	}
	return c.stubOf(e.id), true
}

// list returns the records after the cursor that match the session (empty: all), oldest first,
// as the caller sees them. A stub names no session, so a filter by session never returns one.
func (c *collection[T]) list(after uint32, sessionID string, limit uint32, may reader) []T {
	n := int(limit)
	if n == 0 {
		n = DefaultLimit
	}
	n = min(n, MaxLimit)
	var out []T
	for _, e := range c.snapshot() {
		if len(out) >= n {
			break
		}
		if e.id <= after {
			continue
		}
		r, ok := c.visible(e, may)
		if !ok || (sessionID != "" && r.GetSessionId() != sessionID) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// count returns the number of records, released and staged.
func (c *collection[T]) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.entries() {
		if e.state == entryReleased || e.state == entryStaged {
			n++
		}
	}
	return n
}

// latest returns the newest record of a session that the caller sees, and false if there is none.
func (c *collection[T]) latest(sessionID string, may reader) (T, bool) {
	all := c.snapshot()
	for i := len(all) - 1; i >= 0; i-- {
		if r, ok := c.visible(all[i], may); ok && r.GetSessionId() == sessionID {
			return r, true
		}
	}
	var zero T
	return zero, false
}

// marshalYAML returns the YAML form of a record: the name of its schema, then the fields of the
// contract in their order, in block style. Strings keep their quotes, so an ID such as "123" stays
// a string; a text with line breaks is a literal block if it reads back the same.
func marshalYAML(schema string, m proto.Message) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	for _, literal := range []bool{true, false} {
		out, err := yamlOf(raw, schema, nil, func(n *yaml.Node) { style(n, literal) })
		if err != nil {
			return nil, err
		}
		back := m.ProtoReflect().New().Interface()
		if err := decode(schema, out, back); err == nil && proto.Equal(back, m) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("the YAML form does not read back the same")
}

// marshalMarkdown returns the Markdown form of a record (dec-wiki): front matter with the name of
// its schema and the fields of the contract that are set, in their order, then the text as the
// body, ended by one line break. Strings are plain where that reads back the same.
func marshalMarkdown(schema string, m proto.Message) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	text, _ := fields[check.TextKey].(string)
	for _, plain := range []bool{true, false} {
		front, err := yamlOf(raw, schema, []string{check.TextKey}, func(n *yaml.Node) { style(n, false); unquote(n, plain) })
		if err != nil {
			return nil, err
		}
		out := slices.Concat([]byte("---\n"), front, []byte("---\n"), []byte(text), []byte("\n"))
		back := m.ProtoReflect().New().Interface()
		if err := decode(schema, out, back); err == nil && proto.Equal(back, m) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("the Markdown form does not read back the same")
}

// yamlOf returns the YAML of the JSON form of a record: the field schema first, then the fields
// in their order without the fields in skip and without the fields that are never stored
// (staged, stub). format changes the style of the nodes.
func yamlOf(raw []byte, schema string, skip []string, format func(*yaml.Node)) ([]byte, error) {
	// JSON is YAML: parse it into a node, which keeps the order of the fields.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the record is not a mapping")
	}
	skip = append(slices.Clone(skip), check.StagedKey, check.StubKey)
	mapping := doc.Content[0]
	content := []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: check.SchemaKey},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: schema},
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if !slices.Contains(skip, mapping.Content[i].Value) {
			content = append(content, mapping.Content[i], mapping.Content[i+1])
		}
	}
	mapping.Content = content
	format(&doc)
	return yaml.Marshal(&doc)
}

func style(n *yaml.Node, literal bool) {
	n.Style &^= yaml.FlowStyle
	for i, c := range n.Content {
		switch {
		case n.Kind == yaml.MappingNode && i%2 == 0:
			c.Style = 0
		case literal && c.Kind == yaml.ScalarNode && c.Tag == "!!str" && strings.Contains(c.Value, "\n"):
			c.Style = yaml.LiteralStyle
		}
		style(c, literal)
	}
}

// unquote writes the strings without quotes where YAML allows it. The YAML library still quotes
// a string that would read back as another type, such as "123".
func unquote(n *yaml.Node, plain bool) {
	if plain && n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		n.Style = 0
	}
	for _, c := range n.Content {
		unquote(c, plain)
	}
}

// decode parses the file of a record of the schema, through the check of the part (Fields), so
// the part reads a file as the check sees it. Unknown fields are ignored, so a newer hub can add
// fields.
func decode(schema string, data []byte, m proto.Message) error {
	v, reason := check.Fields(schema, data)
	if reason != "" {
		return errors.New(reason)
	}
	delete(v, check.SchemaKey)
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, m)
}
