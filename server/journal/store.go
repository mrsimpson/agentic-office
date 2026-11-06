package journal

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/protobuf/encoding/protojson"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
)

// Filter selects entries for Store.List. Zero values select everything.
type Filter struct {
	After     uint32             // only entries with an ID greater than this
	Kind      officev1.EntryKind // only this kind; unspecified: all kinds
	SessionID string             // only entries of this session; empty: all
	Limit     int                // at most this many entries; 0: no limit
}

func (f Filter) match(e *officev1.JournalEntry) bool {
	return e.GetId() > f.After &&
		(f.Kind == officev1.EntryKind_ENTRY_KIND_UNSPECIFIED || e.GetKind() == f.Kind) &&
		(f.SessionID == "" || e.GetSessionId() == f.SessionID)
}

// Store keeps the journal: an append-only list of entries.
//
// The service calls Append only while it holds its append lock, so a store does not need to
// check entries. A store must allow List and ByCommand at any time, from many goroutines. Stored
// entries must never change.
type Store interface {
	// Append stores e as the next entry. It sets e.Id to the last ID plus one (the first entry
	// gets 1) and returns e.
	Append(e *officev1.JournalEntry) (*officev1.JournalEntry, error)
	// List returns the entries that match f, oldest first.
	List(f Filter) ([]*officev1.JournalEntry, error)
	// ByCommand returns the entry that the command with this ID created, or nil.
	ByCommand(commandID string) (*officev1.JournalEntry, error)
}

// MemoryStore is a Store in memory. All data is lost when the process stops.
type MemoryStore struct {
	mu      sync.Mutex
	entries []*officev1.JournalEntry // entries[i].Id == i+1
	byCmd   map[string]*officev1.JournalEntry
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byCmd: map[string]*officev1.JournalEntry{}}
}

func (m *MemoryStore) Append(e *officev1.JournalEntry) (*officev1.JournalEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.Id = uint32(len(m.entries) + 1)
	m.add(e)
	return e, nil
}

// add stores an entry that already has its ID. The caller holds m.mu.
func (m *MemoryStore) add(e *officev1.JournalEntry) {
	m.entries = append(m.entries, e)
	if e.GetCommandId() != "" {
		m.byCmd[e.GetCommandId()] = e
	}
}

func (m *MemoryStore) List(f Filter) ([]*officev1.JournalEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*officev1.JournalEntry
	start := min(int(f.After), len(m.entries))
	for _, e := range m.entries[start:] {
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
		if f.match(e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *MemoryStore) ByCommand(commandID string) (*officev1.JournalEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byCmd[commandID], nil
}

// FileStore is a Store that keeps all entries in memory and appends each new entry to a file.
// The file has one entry per line in the JSON form of the API (JSON Lines), so other tools can
// read it and a later store can import it.
type FileStore struct {
	mem  *MemoryStore
	mu   sync.Mutex
	file *os.File
	size int64 // length of the complete lines in the file
}

// OpenFileStore opens or creates the journal file at path and reads all entries. If the last line
// is incomplete (the server stopped while it wrote the line), it is removed.
func OpenFileStore(path string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	mem := NewMemoryStore()
	r := bufio.NewReader(f)
	var good int64
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(bytes.TrimSpace(line)) > 0 {
				// An incomplete last line: drop it, so the next entry starts on a new line.
				if err := f.Truncate(good); err != nil {
					f.Close()
					return nil, err
				}
			}
			break
		}
		if err != nil {
			f.Close()
			return nil, err
		}
		e := &officev1.JournalEntry{}
		if err := protojson.Unmarshal(line, e); err != nil {
			f.Close()
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		if e.GetId() != uint32(len(mem.entries)+1) {
			f.Close()
			return nil, fmt.Errorf("%s line %d: entry ID %d, want %d", path, n, e.GetId(), len(mem.entries)+1)
		}
		mem.add(e)
		good += int64(len(line))
	}
	if _, err := f.Seek(good, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	return &FileStore{mem: mem, file: f, size: good}, nil
}

func (s *FileStore) Append(e *officev1.JournalEntry) (*officev1.JournalEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem.mu.Lock()
	next := uint32(len(s.mem.entries) + 1)
	s.mem.mu.Unlock()
	e.Id = next
	b, err := protojson.Marshal(e)
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
	s.mem.add(e)
	s.mem.mu.Unlock()
	return e, nil
}

func (s *FileStore) List(f Filter) ([]*officev1.JournalEntry, error) { return s.mem.List(f) }

func (s *FileStore) ByCommand(commandID string) (*officev1.JournalEntry, error) {
	return s.mem.ByCommand(commandID)
}

// undo removes a partly written line after a failed write. The caller holds s.mu.
func (s *FileStore) undo() {
	_ = s.file.Truncate(s.size)
	_, _ = s.file.Seek(s.size, io.SeekStart)
}

// Close closes the file.
func (s *FileStore) Close() error { return s.file.Close() }
