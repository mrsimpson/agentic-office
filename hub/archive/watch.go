package archive

import (
	"sync"

	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/archive/watch"
)

var _ watch.Watcher = (*Repo)(nil)

// Watch implements watch.Watcher (if-archive-watch). A watcher is a follower of the changes of
// the repository, the same kind as the call of ArchiveChangesService.Follow: the delivery code
// in follow.go serves both, with the same buffer and the same rule for a slow reader. A small
// goroutine turns the events of the follower into the types of package watch.
func (r *Repo) Watch(dirs ...string) (watch.Subscription, error) {
	for _, d := range dirs {
		if err := storage.CheckPath(d); err != nil {
			return nil, err
		}
	}
	s := &subscription{repo: r, f: r.follow(dirs), out: make(chan []watch.Change), done: make(chan struct{})}
	go s.run()
	return s, nil
}

type subscription struct {
	repo *Repo
	f    *follower
	out  chan []watch.Change
	done chan struct{}
	once sync.Once

	mu  sync.Mutex
	err error
}

// run moves the events of the follower to the channel of the subscription, in order. It never
// holds up the delivery of the archive: when it waits for the part, the buffer of the follower
// fills and the archive drops the follower.
func (s *subscription) run() {
	defer close(s.out)
	for {
		select {
		case <-s.done:
			return
		case changes, ok := <-s.f.events:
			if !ok {
				select {
				case <-s.done: // Close ended the follower
				default:
					s.mu.Lock()
					s.err = watch.ErrDropped
					s.mu.Unlock()
				}
				return
			}
			select {
			case s.out <- convert(changes):
			case <-s.done:
				return
			}
		}
	}
}

func convert(in []*changesv1.ArtifactChange) []watch.Change {
	out := make([]watch.Change, 0, len(in))
	for _, c := range in {
		k := watch.Changed
		switch c.GetKind() {
		case changesv1.ChangeKind_CHANGE_KIND_CREATED:
			k = watch.Created
		case changesv1.ChangeKind_CHANGE_KIND_REMOVED:
			k = watch.Removed
		}
		out = append(out, watch.Change{Path: c.GetPath(), Kind: k, Version: c.GetVersion()})
	}
	return out
}

func (s *subscription) Changes() <-chan []watch.Change { return s.out }

func (s *subscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *subscription) Close() {
	s.once.Do(func() {
		close(s.done)
		s.repo.unfollow(s.f)
	})
}
