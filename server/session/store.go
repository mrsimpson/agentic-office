package session

import (
	"sync"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
)

// Store keeps the append-only event logs of all sessions (concept-event-log).
//
// The service calls Append for one session only while it holds that session's command lock, so a
// store does not need to check commands or state. A store must allow Read at any time, from many
// goroutines, also while Append runs. Stored events must never change.
type Store interface {
	// Append stores ev as the next event of the session. It sets ev.Id to the last ID plus one
	// (the first event gets 1) and returns ev.
	Append(sessionID string, ev *officev1.Event) (*officev1.Event, error)
	// Read returns all events with an ID greater than after, in order. It also returns a channel
	// that is closed when the next event is appended after this call.
	Read(sessionID string, after uint32) ([]*officev1.Event, <-chan struct{}, error)
	// ByCommand returns the event that the command with this ID created, or nil.
	ByCommand(sessionID, commandID string) (*officev1.Event, error)
}

// MemoryStore is a Store in memory. All data is lost when the process stops.
type MemoryStore struct {
	mu   sync.Mutex
	logs map[string]*memoryLog
}

type memoryLog struct {
	events []*officev1.Event          // events[i].Id == i+1
	byCmd  map[string]*officev1.Event // command ID -> event
	notify chan struct{}              // closed and replaced on every append
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore { return &MemoryStore{logs: map[string]*memoryLog{}} }

// log returns the log of a session and creates it if needed. The caller holds m.mu.
func (m *MemoryStore) log(sessionID string) *memoryLog {
	l, ok := m.logs[sessionID]
	if !ok {
		l = &memoryLog{byCmd: map[string]*officev1.Event{}, notify: make(chan struct{})}
		m.logs[sessionID] = l
	}
	return l
}

func (m *MemoryStore) Append(sessionID string, ev *officev1.Event) (*officev1.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log(sessionID)
	ev.Id = uint32(len(l.events) + 1)
	l.events = append(l.events, ev)
	if ev.GetCommandId() != "" {
		l.byCmd[ev.GetCommandId()] = ev
	}
	close(l.notify)
	l.notify = make(chan struct{})
	return ev, nil
}

func (m *MemoryStore) Read(sessionID string, after uint32) ([]*officev1.Event, <-chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.log(sessionID)
	if int(after) >= len(l.events) {
		return nil, l.notify, nil
	}
	// The slice is never written again below its current length, so sharing it is safe.
	return l.events[after:len(l.events):len(l.events)], l.notify, nil
}

func (m *MemoryStore) ByCommand(sessionID, commandID string) (*officev1.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.log(sessionID).byCmd[commandID], nil
}
