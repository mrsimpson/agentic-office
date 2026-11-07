package artifacts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"

	artifactv1 "github.com/mrsimpson/agentic-office/server/artifacts/api"
)

// Record is a stored artifact together with the command that created it. The service needs the
// input to tell a repeated command from a reused command ID.
type Record struct {
	CommandID string
	// Input is the request without command_id.
	Input    *artifactv1.RenderSlidesRequest
	Artifact *artifactv1.Artifact
}

// Store keeps the artifacts. Artifacts never change once stored.
//
// The service calls Add only while it holds its write lock, so a store does not need to check
// records. A store must allow Get and ByCommand at any time, from many goroutines.
type Store interface {
	// Add stores r as the next artifact. It sets r.Artifact.Id to "<kind>/<n>", where n is the
	// number of stored artifacts plus one (the first artifact gets 1), and returns r.
	Add(kind string, r *Record) (*Record, error)
	// Get returns the record of the artifact with this ID, or nil.
	Get(id string) (*Record, error)
	// ByCommand returns the record that the command with this ID created, or nil.
	ByCommand(commandID string) (*Record, error)
}

// MemoryStore is a Store in memory. All data is lost when the process stops.
type MemoryStore struct {
	mu    sync.Mutex
	n     int
	byID  map[string]*Record
	byCmd map[string]*Record
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]*Record{}, byCmd: map[string]*Record{}}
}

func (m *MemoryStore) Add(kind string, r *Record) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Artifact.Id = kind + "/" + strconv.Itoa(m.n+1)
	m.add(r)
	return r, nil
}

// add stores a record that already has its ID. The caller holds m.mu.
func (m *MemoryStore) add(r *Record) {
	m.n++
	m.byID[r.Artifact.GetId()] = r
	m.byCmd[r.CommandID] = r
}

// next returns the ID that the next artifact of this kind gets.
func (m *MemoryStore) next(kind string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return kind + "/" + strconv.Itoa(m.n+1)
}

func (m *MemoryStore) Get(id string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byID[id], nil
}

func (m *MemoryStore) ByCommand(commandID string) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byCmd[commandID], nil
}

// line is one line of the file of a FileStore. Input and artifact are in the JSON form of the
// API, so the content is base64.
type line struct {
	CommandID string          `json:"commandId"`
	Input     json.RawMessage `json:"input"`
	Artifact  json.RawMessage `json:"artifact"`
}

// FileStore is a Store that keeps all artifacts in memory and appends each new artifact to a
// file: one JSON object per line (JSON Lines) with the command ID, the input and the artifact in
// the JSON form of the API.
type FileStore struct {
	mem  *MemoryStore
	mu   sync.Mutex
	file *os.File
	size int64 // length of the complete lines in the file
}

// OpenFileStore opens or creates the artifact file at path and reads all artifacts. If the last
// line is incomplete (the server stopped while it wrote the line), it is removed.
func OpenFileStore(path string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*FileStore, error) {
		f.Close()
		return nil, err
	}
	mem := NewMemoryStore()
	r := bufio.NewReader(f)
	var good int64
	for n := 1; ; n++ {
		b, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(bytes.TrimSpace(b)) > 0 {
				// An incomplete last line: drop it, so the next artifact starts on a new line.
				if err := f.Truncate(good); err != nil {
					return fail(err)
				}
			}
			break
		}
		if err != nil {
			return fail(err)
		}
		var l line
		rec := &Record{Input: &artifactv1.RenderSlidesRequest{}, Artifact: &artifactv1.Artifact{}}
		if err := json.Unmarshal(b, &l); err != nil {
			return fail(fmt.Errorf("%s line %d: %w", path, n, err))
		}
		rec.CommandID = l.CommandID
		if err := protojson.Unmarshal(l.Input, rec.Input); err != nil {
			return fail(fmt.Errorf("%s line %d: input: %w", path, n, err))
		}
		if err := protojson.Unmarshal(l.Artifact, rec.Artifact); err != nil {
			return fail(fmt.Errorf("%s line %d: artifact: %w", path, n, err))
		}
		if want := "/" + strconv.Itoa(n); !strings.HasSuffix(rec.Artifact.GetId(), want) {
			return fail(fmt.Errorf("%s line %d: artifact ID %q, want one that ends with %q", path, n, rec.Artifact.GetId(), want))
		}
		mem.add(rec)
		good += int64(len(b))
	}
	if _, err := f.Seek(good, io.SeekStart); err != nil {
		return fail(err)
	}
	return &FileStore{mem: mem, file: f, size: good}, nil
}

func (s *FileStore) Add(kind string, r *Record) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Artifact.Id = s.mem.next(kind)
	in, err := protojson.Marshal(r.Input)
	if err != nil {
		return nil, err
	}
	a, err := protojson.Marshal(r.Artifact)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(line{CommandID: r.CommandID, Input: in, Artifact: a})
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if _, err := s.file.Write(b); err != nil {
		s.undo()
		return nil, err
	}
	if err := s.file.Sync(); err != nil {
		s.undo()
		return nil, err
	}
	s.size += int64(len(b))
	s.mem.mu.Lock()
	s.mem.add(r)
	s.mem.mu.Unlock()
	return r, nil
}

func (s *FileStore) Get(id string) (*Record, error) { return s.mem.Get(id) }

func (s *FileStore) ByCommand(commandID string) (*Record, error) { return s.mem.ByCommand(commandID) }

// undo removes a partly written line after a failed write. The caller holds s.mu.
func (s *FileStore) undo() {
	_ = s.file.Truncate(s.size)
	_, _ = s.file.Seek(s.size, io.SeekStart)
}

// Close closes the file.
func (s *FileStore) Close() error { return s.file.Close() }
