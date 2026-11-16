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

// collection is the list of records of one type. It keeps all records in memory and writes every
// new record as one YAML file and one commit to the archive. It reloads the files that a push
// changed (see reloadPaths).
type collection[T record] struct {
	kind   string // for messages, for example "decision"
	dir    string // directory in the archive, for example "decisions"
	schema string // the schema that every file of the collection names, for example "office.decision.v1"
	arch   storage.Archive
	newT   func() T
	setID  func(T, uint32)
	logf   func(string, ...any)

	mu       sync.Mutex        // one write at a time; guards everything below
	files    map[string]T      // the valid records by path
	versions map[string]string // the version of the file that each record was read from
	cmds     map[string]string // command_id to the path of its record
	bad      map[string]string // path to the version of a file that was ignored and logged
	high     uint32            // the highest number of any file ever seen, valid or not
	all      []T               // the records ordered by ID; stale while dirty
	dirty    bool
}

func newCollection[T record](arch storage.Archive, kind, dir, schema string, newT func() T, setID func(T, uint32), logf func(string, ...any)) *collection[T] {
	return &collection[T]{kind: kind, dir: dir, schema: schema, arch: arch, newT: newT, setID: setID, logf: logf,
		files: map[string]T{}, versions: map[string]string{}, cmds: map[string]string{}, bad: map[string]string{}}
}

// numberOf returns the number in the name of a record file such as "decisions/000042.yaml".
func numberOf(p string) (uint32, bool) {
	n, err := strconv.ParseUint(strings.TrimSuffix(path.Base(p), ".yaml"), 10, 32)
	return uint32(n), err == nil
}

// load reads one file into the collection. It returns an error and changes nothing if the file is
// not valid. The caller holds c.mu. A file that is not valid still counts for the next ID, so
// the part never takes its number.
func (c *collection[T]) load(f storage.File) error {
	if path.Ext(f.Path) != ".yaml" {
		return nil
	}
	if n, ok := numberOf(f.Path); ok && n > c.high {
		c.high = n
	}
	if v, ok := c.versions[f.Path]; ok && v == f.Version {
		return nil
	}
	// The check of the part: the same one that decides about a push.
	if reasons := check.Validate(c.schema, f.Path, f.Data); len(reasons) > 0 {
		return fmt.Errorf("%s: %s", f.Path, strings.Join(reasons, "; "))
	}
	r := c.newT()
	if err := unmarshalYAML(f.Data, r); err != nil {
		return fmt.Errorf("%s: %w", f.Path, err)
	}
	if p, dup := c.cmds[r.GetCommandId()]; dup && p != f.Path {
		return fmt.Errorf("%s: command_id of %s %s", f.Path, c.kind, p)
	}
	c.forget(f.Path)
	c.files[f.Path] = r
	c.versions[f.Path] = f.Version
	c.cmds[r.GetCommandId()] = f.Path
	delete(c.bad, f.Path)
	c.dirty = true
	return nil
}

// loadLenient is load for a file that a push brought: a file that is not valid is logged once
// for its version and ignored. The record that the path had before stays.
func (c *collection[T]) loadLenient(f storage.File) {
	if err := c.load(f); err != nil && c.bad[f.Path] != f.Version {
		c.bad[f.Path] = f.Version
		c.logf("records: ignore the pushed file %v", err)
	}
}

// forget removes the record of a path. The caller holds c.mu.
func (c *collection[T]) forget(p string) {
	if r, ok := c.files[p]; ok {
		if c.cmds[r.GetCommandId()] == p {
			delete(c.cmds, r.GetCommandId())
		}
		delete(c.files, p)
		c.dirty = true
	}
	delete(c.versions, p)
	delete(c.bad, p)
}

// loadAll reads every file of the directory. With strict, the first file that is not valid fails
// it (the start of the hub). Otherwise such a file is logged and ignored, and a record whose file
// is gone is removed.
func (c *collection[T]) loadAll(ctx context.Context, strict bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
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
		if n, ok := numberOf(p); ok {
			c.high = max(c.high, n)
		}
	}
	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
		if strict {
			if err := c.load(f); err != nil {
				return fmt.Errorf("records: %w", err)
			}
		} else {
			c.loadLenient(f)
		}
	}
	for p := range c.files {
		if !present[p] {
			c.forget(p)
		}
	}
	return nil
}

// reloadPaths reads the files at the paths again from the current commit: a file that is there is
// loaded, a file that is gone is removed. The caller holds c.mu.
func (c *collection[T]) reloadPaths(ctx context.Context, paths []string) error {
	for _, p := range paths {
		if path.Ext(p) != ".yaml" {
			continue
		}
		files, err := c.arch.Read(ctx, p)
		if err != nil {
			return fmt.Errorf("records: read %s: %w", p, err)
		}
		if len(files) == 0 {
			c.forget(p)
			continue
		}
		for _, f := range files {
			c.loadLenient(f)
		}
	}
	return nil
}

// reload is reloadPaths for the changes of a push or a commit under the directory of the
// collection.
func (c *collection[T]) reload(ctx context.Context, changes []watch.Change) error {
	var paths []string
	for _, ch := range changes {
		if strings.HasPrefix(ch.Path, c.dir+"/") {
			paths = append(paths, ch.Path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reloadPaths(ctx, paths)
}

// sorted returns the records ordered by ID. The caller holds c.mu.
func (c *collection[T]) sorted() []T {
	if c.dirty {
		c.all = c.all[:0]
		for _, r := range c.files {
			c.all = append(c.all, r)
		}
		// The paths sort by ID only up to six digits, so sort by ID.
		slices.SortFunc(c.all, func(a, b T) int { return cmp.Compare(a.GetId(), b.GetId()) })
		c.dirty = false
	}
	return c.all
}

// path returns the file of the record with this ID.
func (c *collection[T]) path(id uint32) string { return check.FilePath(c.dir, id) }

// add stores r as the next record, or returns the stored record of the same command and true.
// same reports whether the stored record has the content of r. The commit message is title, the
// ID and the first line of text.
//
// The file is created with "must not exist yet". If a push put a file with this ID there first,
// add reads the file, takes the next free ID and tries again: the caller sees no error.
func (c *collection[T]) add(ctx context.Context, r T, same func(stored, r T) bool, title, text string) (T, bool, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		if p, ok := c.cmds[r.GetCommandId()]; ok {
			prev := c.files[p]
			if !same(prev, r) {
				return zero, false, connect.NewError(connect.CodeAlreadyExists,
					"command_id was already used for another "+c.kind)
			}
			return prev, true, nil
		}
		id := c.high + 1
		c.setID(r, id)
		data, err := marshalYAML(c.schema, r)
		if err != nil {
			return zero, false, connect.NewError(connect.CodeInternal, "encode the "+c.kind+": "+err.Error())
		}
		if reasons := check.Validate(c.schema, c.path(id), data); len(reasons) > 0 {
			return zero, false, connect.NewError(connect.CodeInternal, "the "+c.kind+" is not valid: "+strings.Join(reasons, "; "))
		}
		// A cancelled commit could leave the repository locked, so the commit is not cancelled with
		// the call.
		_, err = c.arch.Commit(context.WithoutCancel(ctx), storage.Change{
			Message: fmt.Sprintf("%s %d: %s", title, id, summary(text)),
			Files:   []storage.File{{Path: c.path(id), Data: data, Base: storage.NoFile}},
		})
		if storage.IsConflict(err) {
			// A push came first. Read what is there; its number is taken from now on.
			if err := c.reloadPaths(context.WithoutCancel(ctx), []string{c.path(id)}); err != nil {
				return zero, false, connect.NewError(connect.CodeInternal, err.Error())
			}
			c.high = max(c.high, id)
			continue
		}
		if err != nil {
			return zero, false, connect.NewError(connect.CodeInternal, "store the "+c.kind+" in the archive: "+err.Error())
		}
		c.high = id
		c.forget(c.path(id))
		c.files[c.path(id)] = r
		c.cmds[r.GetCommandId()] = c.path(id)
		c.dirty = true
		return r, false, nil
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

// list returns the records after the cursor that match the session (empty: all), oldest first.
func (c *collection[T]) list(after uint32, sessionID string, limit uint32) []T {
	n := int(limit)
	if n == 0 {
		n = DefaultLimit
	}
	n = min(n, MaxLimit)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []T
	for _, r := range c.sorted() {
		if len(out) >= n {
			break
		}
		if r.GetId() > after && (sessionID == "" || r.GetSessionId() == sessionID) {
			out = append(out, r)
		}
	}
	return out
}

// count returns the number of records.
func (c *collection[T]) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.files)
}

// latest returns the newest record of a session, and false if there is none.
func (c *collection[T]) latest(sessionID string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	all := c.sorted()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].GetSessionId() == sessionID {
			return all[i], true
		}
	}
	var zero T
	return zero, false
}

// marshalYAML returns the YAML form of a record: the name of its schema, then the fields of the
// contract in their order, in block style. Strings keep their quotes, so an ID such as "123" stays a string; a text with line
// breaks is a literal block if it reads back the same.
func marshalYAML(schema string, m proto.Message) ([]byte, error) {
	raw, err := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	for _, literal := range []bool{true, false} {
		// JSON is YAML: parse it into a node, which keeps the order of the fields.
		var doc yaml.Node
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			return nil, errors.New("the record is not a mapping")
		}
		mapping := doc.Content[0]
		mapping.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: check.SchemaKey},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: schema},
		}, mapping.Content...)
		style(&doc, literal)
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return nil, err
		}
		back := m.ProtoReflect().New().Interface()
		if err := unmarshalYAML(out, back); err == nil && proto.Equal(back, m) {
			return out, nil
		}
	}
	return nil, fmt.Errorf("the YAML form does not read back the same")
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

// unmarshalYAML parses the YAML form of a record. Unknown fields are ignored, so a newer hub
// can add fields.
func unmarshalYAML(data []byte, m proto.Message) error {
	var v map[string]any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, m)
}
