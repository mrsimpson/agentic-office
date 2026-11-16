package control

import (
	"context"
	"errors"
	"io"
	"time"

	coreapi "github.com/mrsimpson/agentic-office/client/core/api"
)

// Options configure a Sync.
type Options struct {
	// Client is the authenticated client of the hub (if-client-core). Its configuration gives the
	// hub address, the tenant and the certificate. Its key proves the participant to the hub.
	Client coreapi.Client
	// CredentialHelper is the command that Git runs to get a fresh access token: the program and
	// its arguments, without the Git operation ("get") that Git appends. The command must call
	// Sync.Credential with the same hub and tenant as Client. It is written into the own
	// configuration of the clone.
	CredentialHelper []string
	// Git is the path of the git command line tool. Empty means: find "git" in PATH.
	Git string
	// Logf receives status messages. Nil means no messages.
	Logf func(format string, args ...any)
}

// Result tells what Init did.
type Result struct {
	// Dir is the absolute path of the main directory.
	Dir string
	// Repository is the address of the repository of the tenant.
	Repository string
	// Cloned is true if Init cloned the repository, false if it only fixed the configuration of an
	// existing clone.
	Cloned bool
	// Author is the name and the address that commits in this clone get, as "Name <address>".
	Author string
}

// UpdateResult tells what Update did.
type UpdateResult struct {
	// Dir is the absolute path of the main directory.
	Dir string
	// Updated is true if main moved forward, false if it was up to date.
	Updated bool
	// From and To are the commits of main before and after the update (the same when not Updated).
	From, To string
	// Rewritten is true if the hub had rewritten the history of main and main moved to the new
	// state (Updated is true then, too).
	Rewritten bool
}

// FollowOptions configure Follow.
type FollowOptions struct {
	// PathPrefixes limit the events that trigger an update to artifacts under these paths (see
	// if-artifact-changes). An update always brings everything. Empty means all artifacts.
	PathPrefixes []string
	// Updated is called after every update that Follow runs: the first one, one for each event
	// (or a group of events that arrived while an update ran) and one after each reconnect. err is
	// nil on success. An update that fails for a reason that the participant must fix (own commits,
	// files that would be overwritten) does not stop Follow; the next event
	// tries again. Nil means: no call.
	Updated func(res UpdateResult, err error)
	// FirstPause is the pause before the first new connection after a failure. The pause doubles
	// with each failure up to MaxPause and starts again after a connection that worked. Zero means
	// one second.
	FirstPause time.Duration
	// MaxPause is the longest pause between two connection attempts. Zero means 30 seconds.
	MaxPause time.Duration
}

// Sync sets up a directory and keeps it up to date.
type Sync interface {
	// Init sets up dir (created if it does not exist) as the main directory of the artifacts of the
	// tenant: it clones branch main and writes the author, the way to get an access token and a
	// guard against commits in the main directory into the own configuration of the clone, never
	// into the global configuration of the user. It remembers the directory in that
	// configuration. It refuses a directory that holds files that are not part of a clone of this
	// tenant (ErrForeignDirectory). On a directory that is set up already, it fixes the
	// configuration and does not touch any file.
	Init(ctx context.Context, dir string) (Result, error)

	// Update fetches the hub and moves main of the main directory dir forward without a merge
	// (fast-forward only). When the hub rewrote the history and main has no commits of its own,
	// nothing of the participant is on main and main moves to the new state of the hub
	// (UpdateResult.Rewritten). Update never touches a file that the participant changed and did not
	// commit. It runs no other Git command than fetch, the fast-forward merge or the move to the
	// rewritten state, besides read-only queries about commits. Errors: ErrNotSetUp (no set-up
	// directory), ErrLinkedWorktree, ErrOwnCommits (main has commits of its own), ErrLocalChanges
	// (the update would overwrite uncommitted files; nothing changed), ErrFetch (the hub could not
	// be reached).
	Update(ctx context.Context, dir string) (UpdateResult, error)

	// Follow updates dir once and then follows the changes of the hub (if-artifact-changes)
	// until ctx ends. It connects first and updates only after the hub has confirmed the
	// registration, so no change falls between the update and the first event. After each event
	// it updates again; events that arrive during an update lead to one more update. When the
	// stream ends or fails, Follow connects again after a pause and updates before it follows,
	// because the hub keeps no events for a client that is away. It returns nil when ctx ends, and
	// an error when the directory is not set up (ErrNotSetUp, ErrLinkedWorktree) or when the hub
	// refuses the participant for good (no member of the tenant).
	Follow(ctx context.Context, dir string, opts FollowOptions) error

	// Credential answers Git as a credential helper (the protocol of "git help credential"): op is
	// the operation that Git passes, in holds the attributes that Git sends. For "get" it prints
	// the user name and a fresh access token of the participant to out, if the attributes name the
	// hub of this sync. It ignores every other operation and every other host.
	Credential(ctx context.Context, op string, in io.Reader, out io.Writer) error
}

// New returns a Sync for the options.
type New func(Options) Sync

// ErrForeignDirectory means that the directory holds files that are not part of a clone of this
// tenant. The error text names the directory and what is wrong.
var ErrForeignDirectory = errors.New("the directory holds files that are not part of a clone of this tenant")

// ErrNoGit means that the git command line tool was not found.
var ErrNoGit = errors.New("the git command line tool was not found")

// ErrNotSetUp means that the directory was not set up with Init.
var ErrNotSetUp = errors.New("the directory is not set up with \"office sync init\"")

// ErrLinkedWorktree means that the directory is a linked worktree. Update and Follow work on the
// main directory only.
var ErrLinkedWorktree = errors.New("the directory is a linked worktree")

// ErrOwnCommits means that main of the main directory has commits that the hub does not have.
var ErrOwnCommits = errors.New("the main directory has commits of its own")

// ErrLocalChanges means that a file with changes that are not committed would be overwritten by
// the update. Nothing was changed.
var ErrLocalChanges = errors.New("uncommitted files would be overwritten")

// ErrFetch means that the hub could not be reached or did not give its data. Trying again later
// may work.
var ErrFetch = errors.New("could not fetch from the hub")
