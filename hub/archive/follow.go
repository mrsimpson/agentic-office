package archive

import (
	"context"
	"slices"
	"strings"
	"sync"

	"connectrpc.com/connect/v2"

	changesv1 "github.com/mrsimpson/agentic-office/hub/archive/changes/api"
	"github.com/mrsimpson/agentic-office/hub/archive/changes/api/changesv1connect"
	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	"github.com/mrsimpson/agentic-office/hub/identity/tokencheck"
)

// followerBuffer is how many events the archive keeps for a follower that does not read. A
// follower with a full buffer is dropped.
const followerBuffer = 16

// move is one change of main that no follower has been told about yet.
type move struct{ from, to string }

// changeLog tracks the moves of main of one repository and tells the followers. It never blocks
// the caller: a move is queued, and a goroutine that runs only while the queue is not empty
// compares the two trees and delivers the event.
type changeLog struct {
	mu        sync.Mutex
	last      string // the state of main that the followers know
	queue     []move
	running   bool
	followers map[*follower]struct{}
}

// follower is a call of ArchiveChangesService.Follow.
type follower struct {
	prefixes []string
	events   chan []*changesv1.ArtifactChange
}

// reset sets the state that the followers know, when the repository opens.
func (l *changeLog) reset(head string) {
	l.mu.Lock()
	l.last = head
	l.mu.Unlock()
}

// moved reports that main moved from old to now. A state in between that nobody reported (a
// push that has not been announced yet) is reported first, so no change is lost.
func (r *Repo) moved(old, now string) {
	l := &r.changes
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last != old {
		l.queue = append(l.queue, move{l.last, old})
	}
	l.queue = append(l.queue, move{old, now})
	l.last = now
	r.startDelivery()
}

// MainMoved tells the archive that something other than the archive moved main: it compares
// main with the last state it knows. It returns at once.
func (r *Repo) MainMoved(ctx context.Context) error {
	now, err := r.head(ctx)
	if err != nil {
		return err
	}
	l := &r.changes
	l.mu.Lock()
	defer l.mu.Unlock()
	if now == l.last {
		return nil
	}
	l.queue = append(l.queue, move{l.last, now})
	l.last = now
	r.startDelivery()
	return nil
}

// startDelivery starts the delivery goroutine if none runs. The caller holds l.mu.
func (r *Repo) startDelivery() {
	l := &r.changes
	if l.running {
		return
	}
	l.running = true
	go r.deliver()
}

// deliver sends the queued moves in order, then ends.
func (r *Repo) deliver() {
	l := &r.changes
	for {
		l.mu.Lock()
		if len(l.queue) == 0 {
			l.running = false
			l.mu.Unlock()
			return
		}
		m := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()
		changes, err := r.diff(context.Background(), m.from, m.to)
		if err != nil {
			r.logf("archive: the changes between %s and %s of %s cannot be told to the followers: %v", m.from, m.to, r.dir, err)
			continue
		}
		if len(changes) > 0 {
			r.publish(changes)
		}
	}
}

// diff lists the artifacts that differ between the commits from and to, ordered by path. An
// empty ID stands for the empty tree.
func (r *Repo) diff(ctx context.Context, from, to string) ([]*changesv1.ArtifactChange, error) {
	empty := ""
	tree := func(c string) (string, error) {
		if c != "" {
			return c, nil
		}
		if empty == "" {
			out, err := r.run(ctx, "hash-object", "-t", "tree", "--stdin")
			if err != nil {
				return "", err
			}
			empty = strings.TrimSpace(out)
		}
		return empty, nil
	}
	a, err := tree(from)
	if err != nil {
		return nil, err
	}
	b, err := tree(to)
	if err != nil {
		return nil, err
	}
	// Per file: ":<old mode> <new mode> <old object> <new object> <status>", NUL, path, NUL.
	out, err := r.run(ctx, "diff-tree", "-r", "-z", "--raw", "--no-renames", "--no-abbrev", a, b)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(out, "\x00")
	var changes []*changesv1.ArtifactChange
	for i := 0; i+1 < len(fields); i += 2 {
		f := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		p := fields[i+1]
		if len(f) < 5 || !isArtifactFile(p) || storage.CheckPath(p) != nil {
			continue
		}
		hadOld, hasNew := isFileMode(f[0]), isFileMode(f[1])
		switch {
		case hadOld && hasNew && f[2] != f[3]:
			changes = append(changes, &changesv1.ArtifactChange{Path: p, Kind: changesv1.ChangeKind_CHANGE_KIND_CHANGED, Version: f[3]})
		case hasNew && !hadOld:
			changes = append(changes, &changesv1.ArtifactChange{Path: p, Kind: changesv1.ChangeKind_CHANGE_KIND_CREATED, Version: f[3]})
		case hadOld && !hasNew:
			changes = append(changes, &changesv1.ArtifactChange{Path: p, Kind: changesv1.ChangeKind_CHANGE_KIND_REMOVED})
		}
	}
	slices.SortFunc(changes, func(x, y *changesv1.ArtifactChange) int { return strings.Compare(x.Path, y.Path) })
	return changes, nil
}

// isFileMode reports whether a Git mode is a regular file. Symbolic links, submodules and the
// absent file (mode 000000) are not artifacts.
func isFileMode(mode string) bool { return mode == "100644" || mode == "100755" }

// publish gives the change to every follower without waiting. A follower whose buffer is full
// is dropped.
func (r *Repo) publish(changes []*changesv1.ArtifactChange) {
	l := &r.changes
	l.mu.Lock()
	defer l.mu.Unlock()
	for f := range l.followers {
		matching := changes
		if len(f.prefixes) > 0 {
			matching = nil
			for _, c := range changes {
				if hasPrefix(f.prefixes, c.Path) {
					matching = append(matching, c)
				}
			}
		}
		if len(matching) == 0 {
			continue
		}
		select {
		case f.events <- matching:
		default:
			delete(l.followers, f)
			close(f.events)
		}
	}
}

// hasPrefix reports whether p is one of the paths or lies below one of them.
func hasPrefix(prefixes []string, p string) bool {
	for _, x := range prefixes {
		if p == x || strings.HasPrefix(p, x+"/") {
			return true
		}
	}
	return false
}

// follow registers a follower.
func (r *Repo) follow(prefixes []string) *follower {
	f := &follower{prefixes: prefixes, events: make(chan []*changesv1.ArtifactChange, followerBuffer)}
	l := &r.changes
	l.mu.Lock()
	if l.followers == nil {
		l.followers = map[*follower]struct{}{}
	}
	l.followers[f] = struct{}{}
	l.mu.Unlock()
	return f
}

// unfollow removes a follower that is still registered.
func (r *Repo) unfollow(f *follower) {
	l := &r.changes
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.followers[f]; ok {
		delete(l.followers, f)
		close(f.events)
	}
}

// ChangesService implements the changes interface of the archive (if-artifact-changes) for the
// repository of one tenant.
type ChangesService struct {
	repo *Repo
}

var _ changesv1connect.ArchiveChangesServiceHandler = (*ChangesService)(nil)

// NewChangesService returns the changes interface of the repository of one tenant.
func NewChangesService(r *Repo) *ChangesService { return &ChangesService{repo: r} }

// Follow implements the RPC. It needs the access token of a participant of the tenant.
func (s *ChangesService) Follow(ctx context.Context, req *changesv1.FollowRequest, stream changesv1connect.ArchiveChangesServiceFollowServerStream) error {
	if err := participant(ctx); err != nil {
		return err
	}
	var prefixes []string
	for _, p := range req.GetPathPrefixes() {
		if err := storage.CheckPath(p); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err.Error())
		}
		prefixes = append(prefixes, p)
	}
	caller, _ := tokencheck.CallerFrom(ctx)
	f := s.repo.follow(prefixes)
	defer s.repo.unfollow(f)
	// The first message is the signal that the follower is registered (see the contract).
	if err := stream.Send(&changesv1.FollowResponse{Message: &changesv1.FollowResponse_Registered{Registered: &changesv1.Registered{}}}); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-caller.Done:
			return connect.NewError(connect.CodeUnauthenticated, "access token expired or revoked")
		case changes, ok := <-f.events:
			if !ok {
				// Only publish closes the channel of a call that is still running.
				return connect.NewError(connect.CodeResourceExhausted, "the follower did not read fast enough and was dropped; connect again and update the clone")
			}
			if err := stream.Send(&changesv1.FollowResponse{Message: &changesv1.FollowResponse_Changes{Changes: &changesv1.ArtifactChanges{Changes: changes}}}); err != nil {
				return err
			}
		}
	}
}
