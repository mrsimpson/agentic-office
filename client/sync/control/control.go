package control

import (
	"context"
	"errors"
	"io"

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

// Sync sets up a directory.
type Sync interface {
	// Init sets up dir (created if it does not exist) as the main directory of the artifacts of the
	// tenant: it clones branch main and writes the author, the way to get an access token and a
	// guard against commits in the main directory into the own configuration of the clone, never
	// into the global configuration of the user. It remembers the directory in that
	// configuration. It refuses a directory that holds files that are not part of a clone of this
	// tenant (ErrForeignDirectory). On a directory that is set up already, it fixes the
	// configuration and does not touch any file.
	Init(ctx context.Context, dir string) (Result, error)

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
