package participant

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

// Environment variables that ConfigFromEnv reads.
const (
	EnvServer     = "OFFICE_SERVER"
	EnvCACert     = "OFFICE_CA_CERT"
	EnvKey        = "OFFICE_KEY"
	EnvRuntimeDir = "OFFICE_RUNTIME_DIR"
)

// DefaultServer is the server URL if OFFICE_SERVER is not set.
const DefaultServer = "https://127.0.0.1:8740"

// Config tells a client where the server is and where its local files live.
type Config struct {
	// Server is the base URL of the office server, for example "https://127.0.0.1:8740".
	Server string
	// CACert is a PEM file with the certificate of the server (its tls.crt if it is
	// self-signed). Empty means: trust the system roots only.
	CACert string
	// KeyFile is the private key of this actor.
	KeyFile string
	// RuntimeDir is the private directory for local logs and state.
	RuntimeDir string
}

// ConfigFromEnv returns the configuration from the OFFICE_* environment variables and the
// defaults:
//
//   - Server: OFFICE_SERVER, else DefaultServer.
//   - CACert: OFFICE_CA_CERT, else <config dir>/agentic-office/client/server.crt if it exists,
//     else empty (system roots).
//   - KeyFile: OFFICE_KEY, else <config dir>/agentic-office/client/key.pem.
//   - RuntimeDir: OFFICE_RUNTIME_DIR, else $XDG_RUNTIME_DIR/agentic-office, else
//     <temp dir>/agentic-office-<uid>.
func ConfigFromEnv() Config {
	c := Config{Server: os.Getenv(EnvServer), CACert: os.Getenv(EnvCACert), KeyFile: os.Getenv(EnvKey),
		RuntimeDir: os.Getenv(EnvRuntimeDir)}
	if c.Server == "" {
		c.Server = DefaultServer
	}
	cfgDir := clientConfigDir()
	if c.CACert == "" {
		if p := filepath.Join(cfgDir, "server.crt"); fileExists(p) {
			c.CACert = p
		}
	}
	if c.KeyFile == "" {
		c.KeyFile = filepath.Join(cfgDir, "key.pem")
	}
	if c.RuntimeDir == "" {
		if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
			c.RuntimeDir = filepath.Join(d, "agentic-office")
		} else {
			c.RuntimeDir = filepath.Join(os.TempDir(), "agentic-office-"+strconv.Itoa(os.Getuid()))
		}
	}
	return c
}

// Env returns the configuration as OFFICE_* environment variables, for a child process.
func (c Config) Env() []string {
	return []string{EnvServer + "=" + c.Server, EnvCACert + "=" + c.CACert, EnvKey + "=" + c.KeyFile,
		EnvRuntimeDir + "=" + c.RuntimeDir}
}

func clientConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "agentic-office", "client")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,127}$`)

// FileName turns a session or participant ID into a file name. Safe IDs (letters, digits, ".",
// "_" and "-", not starting with ".") stay as they are. Other IDs become "=" followed by the
// unpadded base64url form of the ID, so they can never leave the directory.
func FileName(id string) string {
	if safeName.MatchString(id) {
		return id
	}
	return "=" + base64.RawURLEncoding.EncodeToString([]byte(id))
}

// Paths are the local files of one participant in one session.
type Paths struct {
	// Dir is the directory of the session.
	Dir string
	// Log is the local session log (if-local-log).
	Log string
	// Join holds the join state (session ID and join command ID).
	Join string
	// PID holds the process ID of the running replicator.
	PID string
	// ReplicatorLog is the output of a detached replicator.
	ReplicatorLog string
}

// Paths returns the local files of participantID in sessionID and creates the directories
// with mode 0700. It fails if the runtime directory belongs to another user.
func (c Config) Paths(sessionID, participantID string) (Paths, error) {
	if sessionID == "" || participantID == "" {
		return Paths{}, errors.New("session and participant are required")
	}
	if err := privateDir(c.RuntimeDir); err != nil {
		return Paths{}, err
	}
	dir := filepath.Join(c.RuntimeDir, FileName(sessionID))
	if err := privateDir(dir); err != nil {
		return Paths{}, err
	}
	base := filepath.Join(dir, FileName(participantID))
	return Paths{Dir: dir, Log: base + ".jsonl", Join: base + ".join", PID: base + ".pid",
		ReplicatorLog: base + ".replicator.log"}, nil
}

// privateDir creates dir with mode 0700 and checks that it belongs to this user and that
// nobody else can read it.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user; set %s", dir, EnvRuntimeDir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
