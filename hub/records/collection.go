package records

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// record is what every record type has.
type record interface {
	proto.Message
	GetId() uint32
	GetCommandId() string
	GetSessionId() string
}

// collection is the list of records of one type. It keeps all records in memory and writes every
// new record as one YAML file and one commit to the archive.
type collection[T record] struct {
	kind  string // for messages, for example "decision"
	dir   string // directory in the archive, for example "decisions"
	arch  storage.Archive
	newT  func() T
	setID func(T, uint32)

	mu    sync.Mutex // one write at a time; guards all and byCmd
	all   []T        // ordered by ID
	byCmd map[string]T
}

// openCollection reads the records of dir from the current commit of the archive.
func openCollection[T record](ctx context.Context, arch storage.Archive, kind, dir string, newT func() T, setID func(T, uint32)) (*collection[T], error) {
	c := &collection[T]{kind: kind, dir: dir, arch: arch, newT: newT, setID: setID, byCmd: map[string]T{}}
	files, err := arch.Read(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("records: read %s: %w", dir, err)
	}
	byID := map[uint32]string{}
	for _, f := range files {
		if path.Ext(f.Path) != ".yaml" {
			continue
		}
		r := newT()
		if err := unmarshalYAML(f.Data, r); err != nil {
			return nil, fmt.Errorf("records: %s: %w", f.Path, err)
		}
		switch prev, dup := byID[r.GetId()]; {
		case r.GetId() == 0:
			return nil, fmt.Errorf("records: %s: no id", f.Path)
		case dup:
			return nil, fmt.Errorf("records: %s and %s have the same id %d", prev, f.Path, r.GetId())
		case r.GetCommandId() == "":
			return nil, fmt.Errorf("records: %s: no command_id", f.Path)
		}
		if prev, dup := c.byCmd[r.GetCommandId()]; dup {
			return nil, fmt.Errorf("records: %s: command_id of %s %d", f.Path, kind, prev.GetId())
		}
		byID[r.GetId()] = f.Path
		c.all = append(c.all, r)
		c.byCmd[r.GetCommandId()] = r
	}
	// The paths sort by ID only up to six digits, so sort by ID.
	slices.SortFunc(c.all, func(a, b T) int { return cmp.Compare(a.GetId(), b.GetId()) })
	return c, nil
}

// path returns the file of the record with this ID.
func (c *collection[T]) path(id uint32) string { return fmt.Sprintf("%s/%06d.yaml", c.dir, id) }

// add stores r as the next record, or returns the stored record of the same command and true.
// same reports whether the stored record has the content of r. The commit message is title, the
// ID and the first line of text.
func (c *collection[T]) add(ctx context.Context, r T, same func(stored, r T) bool, title, text string) (T, bool, error) {
	var zero T
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.byCmd[r.GetCommandId()]; ok {
		if !same(prev, r) {
			return zero, false, connect.NewError(connect.CodeAlreadyExists,
				"command_id was already used for another "+c.kind)
		}
		return prev, true, nil
	}
	var id uint32 = 1
	if n := len(c.all); n > 0 {
		id = c.all[n-1].GetId() + 1
	}
	c.setID(r, id)
	data, err := marshalYAML(r)
	if err != nil {
		return zero, false, connect.NewError(connect.CodeInternal, "encode the "+c.kind+": "+err.Error())
	}
	// A cancelled commit could leave the repository locked, so the commit is not cancelled with
	// the call.
	_, err = c.arch.Commit(context.WithoutCancel(ctx), storage.Change{
		Message: fmt.Sprintf("%s %d: %s", title, id, summary(text)),
		Files:   []storage.File{{Path: c.path(id), Data: data}},
	})
	if err != nil {
		return zero, false, connect.NewError(connect.CodeInternal, "store the "+c.kind+" in the archive: "+err.Error())
	}
	c.all = append(c.all, r)
	c.byCmd[r.GetCommandId()] = r
	return r, false, nil
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
	for _, r := range c.all {
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
	return len(c.all)
}

// latest returns the newest record of a session, and false if there is none.
func (c *collection[T]) latest(sessionID string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := len(c.all) - 1; i >= 0; i-- {
		if c.all[i].GetSessionId() == sessionID {
			return c.all[i], true
		}
	}
	var zero T
	return zero, false
}

// marshalYAML returns the YAML form of a record: the fields of the contract in their order, in
// block style. Strings keep their quotes, so an ID such as "123" stays a string; a text with line
// breaks is a literal block if it reads back the same.
func marshalYAML(m proto.Message) ([]byte, error) {
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
