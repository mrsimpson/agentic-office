// Command office-hub runs the office hub (bb-office-hub).
//
// All services are mounted, and the stage at /stage/<session>?token=<stage link token>. The
// artifact repository of a tenant is served over the Git smart protocol at /git/<tenant> (read
// access only; the password of HTTP Basic is an access token). Every call
// needs an access token from the identity block, except GetChallenge and ExchangeChallenge. The
// server speaks only TLS.
//
// The hub serves several tenants (dec-multi-tenancy). The users, tenants and memberships are files
// in the hub repository (repos/hub, directory system/). Each tenant has a repository of its own
// (repos/tenants/<id>), and one instance of the meeting session, the records, the tasks and the
// slides that read and write only this repository. The meeting session keeps its logs in memory,
// signs their tree heads with the signing key and writes them to the repository of the tenant
// (a local Git repository; the push is mocked). Every call of these services names the tenant in the
// header Office-Tenant; the identity interceptor checks the membership. At the first start the
// operator names the first system admin with -first-system-admin ID=PUBLIC_KEY.
//
// The operator gives the hub its signing key at start (dec-hub-signing-key): -signing-key FILE is
// required, and the hub does not start without a valid key. Create a key file once with
//
//	office-hub keygen FILE
//
// It refuses to overwrite an existing file. Replacing the key later is a deliberate act: heads
// signed with the old key then no longer verify against GetHubKey. How clients pin the public key
// of the hub is still open (#41).
//
// Files in the data directory (-data-dir):
//
//	tls.crt      TLS certificate; created self-signed if neither it nor tls.key exists
//	tls.key      TLS private key
//	repos/hub/   hub repository: users, tenants and memberships
//	repos/tenants/<id>/   repository of one tenant: its sessions (written at the interval
//	             -archive-interval and when a session ends), decisions, minutes, notes, tasks and
//	             slide decks, with the input and the rendered output

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mrsimpson/agentic-office/hub/stage/web"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/mrsimpson/agentic-office/hub/archive"
	"github.com/mrsimpson/agentic-office/hub/archive/api/archivev1connect"
	"github.com/mrsimpson/agentic-office/hub/archive/changes/api/changesv1connect"
	"github.com/mrsimpson/agentic-office/hub/documents/api/documentv1connect"
	"github.com/mrsimpson/agentic-office/hub/gitaccess"
	gitweb "github.com/mrsimpson/agentic-office/hub/gitaccess/web"
	"github.com/mrsimpson/agentic-office/hub/health"
	"github.com/mrsimpson/agentic-office/hub/identity"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
	"github.com/mrsimpson/agentic-office/hub/portal"
	portalweb "github.com/mrsimpson/agentic-office/hub/portal/web"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session/activityapi/sessionactivityv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/listapi/sessionlistv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	"github.com/mrsimpson/agentic-office/hub/stage"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
	"github.com/mrsimpson/agentic-office/hub/tenant"
	"github.com/mrsimpson/agentic-office/hub/tenant/api/tenantv1connect"
	"github.com/mrsimpson/agentic-office/hub/wiki/api/wikiv1connect"
)

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "agentic-office", "hub")
	}
	return "office-hub-data"
}

// keygenHint tells the operator how to create a signing key file.
const keygenHint = "create one with: office-hub keygen FILE"

// firstAdminHint tells the operator where the public key of the first system admin comes from.
const firstAdminHint = `The public key comes from "office key create" on your own computer; ` +
	`use a user ID such as root, because a system admin never works in a tenant.`

// flagSet tells whether the operator gave the flag, also with its default value.
func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// keygen writes a new signing key to the file in args and prints its public key. It refuses to
// overwrite an existing file.
func keygen(args []string) {
	if len(args) == 1 && isHelpFlag(args[0]) {
		fmt.Print(keygenHelp)
		return
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(os.Stderr, keygenHelp)
		os.Exit(2)
	}
	key, err := auth.NewKey()
	if err != nil {
		log.Fatal(err)
	}
	if err := auth.WriteKey(args[0], key); err != nil {
		if errors.Is(err, fs.ErrExist) {
			log.Fatalf("%s exists; it is not changed", args[0])
		}
		log.Fatal(err)
	}
	fmt.Printf("signing key: %s\npublic key:  %s\n", args[0], auth.FormatPublicKey(auth.PublicKey(key)))
}

// The flags are package variables so that the help can list them before the hub starts.
var (
	addr            = flag.String("addr", "127.0.0.1:8740", "listen address (port 0 picks a free port)")
	dataDir         = flag.String("data-dir", defaultDataDir(), "data directory: the TLS certificate and the Git repositories of the hub and the tenants; <config>/agentic-office/hub by default, where <config> is ~/.config or $XDG_CONFIG_HOME on Linux and ~/Library/Application Support on macOS")
	certFile        = flag.String("tls-cert", "", "TLS certificate (PEM, may include the chain); default <data-dir>/tls.crt")
	keyFile         = flag.String("tls-key", "", "TLS private key (PEM); default <data-dir>/tls.key")
	tlsHosts        = flag.String("tls-hosts", "localhost,127.0.0.1,::1,"+osHostname(), "host names and IPs of a new self-signed certificate; participants must reach the hub by one of them. A start with -tls-hosts fails if the existing self-signed certificate misses one")
	signingKeyFile  = flag.String("signing-key", "", "Ed25519 key file that signs tree heads of session logs (PEM); required, "+keygenHint)
	firstAdmin      = flag.String("first-system-admin", "", "ID=PUBLIC_KEY of the first system admin; needed at the first start, ignored later")
	publicURL       = flag.String("public-url", "", "base URL of the hub in stage links; default https://<listen address>")
	tokenTTL        = flag.Duration("token-ttl", 15*time.Minute, "lifetime of access tokens; clients sign in again before they expire")
	stageTTL        = flag.Duration("stage-link-ttl", 24*time.Hour, "lifetime of stage links (read-only links to the stage of one session)")
	archiveInterval = flag.Duration("archive-interval", 30*time.Second, "time between two writes of changed sessions to the archive; sessions are also written when they end. Keep it above the usual commit time")
	gitMaxPushMiB   = flag.Int64("git-max-push-mib", gitaccess.DefaultMaxPushBytes>>20, "largest push to the artifact repository, in MiB; a larger push is refused")
	gitMaxFileKiB   = flag.Int64("git-max-file-kib", gitaccess.DefaultMaxFileBytes>>10, "largest artifact file in a push, in KiB; a larger file refuses the push")
	gitMaxFiles     = flag.Int("git-max-files", gitaccess.DefaultMaxFiles, "most files that one push may change; a push that changes more is refused")
	gracePeriod     = flag.Duration("release-grace-period", 24*time.Hour, "time between the end of a session with humans and its release by the hub, for every tenant; a human who took part can release earlier or object")
	restoreWait     = flag.Duration("restore-wait", 10*time.Second, "after a restart, how long a restored session that has not ended waits for a peer to fill in the events after the last write to the archive")
)

func osHostname() string {
	h, _ := os.Hostname()
	return h
}

func isHelpFlag(a string) bool { return a == "-h" || a == "--help" || a == "-help" || a == "help" }

// keygenHelp is the help of "office-hub keygen".
const keygenHelp = `office-hub keygen: create the signing key of the hub

usage: office-hub keygen FILE

The hub signs the tree heads of the session logs with an Ed25519 key that you, the operator,
give it at every start with -signing-key FILE. The hub never creates a signing key by itself.
keygen writes the private key as PEM (PKCS #8) with mode 0600 and prints the public key. It
refuses to overwrite an existing file. The hub logs the public key at start ("signing key of tree
heads ..."). Keep the file with your other secrets and back it up: without it, old tree heads
cannot be checked against the hub key, and replacing it is a deliberate act.

Example:
  office-hub keygen ~/office-hub/signing.key
`

// hubHelp is the text before the flags in the help of "office-hub".
const hubHelp = `office-hub: the hub of the agentic office

usage:
  office-hub -signing-key FILE [flags]     run the hub
  office-hub keygen FILE                   create the signing key file (once)
  office-hub help                          print this help

The hub orders the meetings, keeps the archive and serves the stage. It speaks only TLS and needs
an access token for every call. Run it once per team, on a computer that all participants can
reach on one port (default 8740). The user guide shows the whole setup.

First start: the hub needs a signing key and the first system admin, who runs the hub.
  office-hub keygen ~/office-hub/signing.key
  office key create                                  # on your own computer: prints the public key
  office-hub -signing-key ~/office-hub/signing.key -addr 0.0.0.0:8740 \
    -first-system-admin root=<public key> -tls-hosts hub.example.org \
    -public-url https://hub.example.org:8740

- -first-system-admin creates the user (here root) as a human and system admin when the hub
  repository has no users yet. Without the flag, the first start fails. Later starts ignore it.
  A system admin is never a member of a tenant: use a second user for your work in a tenant.
- -signing-key is required. The hub does not start if the file is missing, broken or readable by
  others. Start the hub with the same file every time, so tree heads signed before a restart
  still verify.
- TLS: if neither tls.crt nor tls.key exists, the hub creates a self-signed certificate for the
  names of -tls-hosts (valid for 820 days, because macOS and browsers reject longer ones), and
  logs its SHA-256 fingerprint. Participants compare that fingerprint with "office hub add". If a
  later start names a host in -tls-hosts that the self-signed certificate misses, the hub does not
  start: delete tls.crt and tls.key to create a new one. To
  use your own certificate, pass -tls-cert and -tls-key (PEM, the certificate may hold the whole
  chain; the key is PKCS #8, PKCS #1 or SEC 1), or put both files into the data directory. The
  hub never overwrites existing files, and reads the certificate at start: restart it after you
  renew the certificate. With a certificate from a public authority, give clients no
  certificate file.
- The hub needs the "git" command line tool in PATH; its archive is Git repositories. It runs git
  without your system and global git configuration.
- The artifact repository of a tenant can be fetched with Git at https://<hub>/git/<tenant>. The
  password is an access token; the user name is ignored. Pushing is not allowed yet.
- GET /healthz answers 200 and "ok" without a login, for a container probe.

Files in the data directory (-data-dir):
  tls.crt, tls.key       TLS certificate and key
  repos/hub/             the hub repository: users, tenants and memberships (the hub is the only writer)
  repos/tenants/<id>/    the Git repository of one tenant: sessions, decisions, minutes, notes,
                         tasks and slide decks; it stays when the tenant is removed

The hub writes the sessions that changed every -archive-interval and when a session ends, and
once more when it stops (SIGINT or SIGTERM). After a restart it restores the sessions that have
not ended from the archive; the participants fill in the events after the last write, and until
then, for at most -restore-wait, commands of such a session fail with "unavailable".

Flags:
`

// hubExample is the text after the flags in the help of "office-hub".
const hubExample = `
Example, the same hub as a container (the image has the default command
-data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key; pass all flags or none):
  docker run -d --name office-hub -p 8740:8740 -v office-hub:/data \
    ghcr.io/mrsimpson/agentic-office/office-hub \
    -data-dir /data -addr 0.0.0.0:8740 -signing-key /data/signing.key \
    -first-system-admin root=<public key>
`

func printHubHelp(w io.Writer) {
	fmt.Fprint(w, hubHelp)
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()
	fmt.Fprint(w, hubExample)
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == gitaccess.SubcommandPreReceive {
		// Git started the hub program as the pre-receive hook of the artifact repository.
		os.Exit(gitaccess.RunPreReceive(os.Stdin, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		keygen(os.Args[2:])
		return
	}
	if len(os.Args) == 2 && isHelpFlag(os.Args[1]) {
		printHubHelp(os.Stdout)
		return
	}
	flag.Usage = func() { printHubHelp(os.Stderr) }
	flag.Parse()
	or := func(v, def string) string {
		if v != "" {
			return v
		}
		return filepath.Join(*dataDir, def)
	}

	// Check everything that can fail before the hub writes to the data directory: a failed start
	// leaves nothing behind, such as a self-signed certificate for the wrong names, which a later
	// start would keep.

	// The operator gives the signing key (dec-hub-signing-key). It must stay the same across
	// restarts, so old tree heads still verify (dec-merkle-log).
	if *signingKeyFile == "" {
		log.Fatal("no signing key: pass -signing-key FILE; " + keygenHint)
	}
	signingKey, err := auth.ReadKey(*signingKeyFile)
	if err != nil {
		log.Fatalf("signing key: %v; %s", err, keygenHint)
	}
	// At the first start, the hub repository does not exist yet and the hub needs the first
	// system admin. Later starts ignore the flag.
	if _, err := os.Stat(filepath.Join(*dataDir, "repos", "hub")); errors.Is(err, fs.ErrNotExist) {
		if *firstAdmin == "" {
			log.Fatal("first start: name the first system admin with -first-system-admin ID=PUBLIC_KEY. " + firstAdminHint)
		}
		if _, err := identity.ParseBootstrap(*firstAdmin); err != nil {
			log.Fatalf("-first-system-admin: %v. %s", err, firstAdminHint)
		}
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}

	hosts := strings.Split(*tlsHosts, ",")
	cert, created, err := loadOrCreateCert(or(*certFile, "tls.crt"), or(*keyFile, "tls.key"), hosts)
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("created a self-signed TLS certificate %s for %s", or(*certFile, "tls.crt"), *tlsHosts)
	} else if flagSet("tls-hosts") {
		// -tls-hosts only shapes a new certificate. An old self-signed one that misses a name
		// would make every participant fail at that name, so the hub does not start with it.
		if missing := missingHosts(cert, hosts); len(missing) > 0 {
			log.Fatalf("the self-signed TLS certificate %s does not cover %s of -tls-hosts; "+
				"to create a new one for -tls-hosts, delete %s and %s and start again "+
				"(participants then check the new fingerprint with \"office hub add\")",
				or(*certFile, "tls.crt"), strings.Join(missing, ", "), or(*certFile, "tls.crt"), or(*keyFile, "tls.key"))
		}
	}
	log.Printf("TLS certificate %s sha256 %s", or(*certFile, "tls.crt"), fingerprint(cert))
	if warn := validityWarning(cert); warn != "" {
		log.Printf("warning: TLS certificate %s: %s", or(*certFile, "tls.crt"), warn)
	}
	log.Printf("signing key of tree heads %s", auth.FormatPublicKey(auth.PublicKey(signingKey)))

	if *publicURL == "" {
		*publicURL = "https://" + ln.Addr().String()
	}
	// The repositories of the hub (dec-multi-tenancy): the hub repository for the users, tenants and
	// memberships, and one for each tenant. The layout is fixed.
	repos, err := archive.OpenRepos(context.Background(), filepath.Join(*dataDir, "repos"), archive.Options{Push: archive.NoPush{}})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("hub repository %s; artifact repository of each tenant below %s, session-log repository below %s; push mocked", repos.HubDir(),
		filepath.Join(*dataDir, "repos", "tenants"), filepath.Join(*dataDir, "repos", "session-logs"))
	var bootstrap *identity.Bootstrap
	var bootstrapErr error
	if *firstAdmin != "" {
		bootstrap, bootstrapErr = identity.ParseBootstrap(*firstAdmin)
	}
	// Users, keys and memberships live in the hub repository (dec-multi-tenancy); the hub does
	// not start when the repository breaks a rule.
	ids, err := identity.New(context.Background(), identity.Options{TokenTTL: *tokenTTL, StageLinkTTL: *stageTTL,
		PublicURL: *publicURL, Archive: repos.Hub(), Bootstrap: bootstrap, BootstrapErr: bootstrapErr, Logf: log.Printf,
		TenantFreeServices: []string{tenantv1connect.TenantServiceName},
		StageProcedures: []string{sessionstreamv1connect.SessionStreamServiceSubscribeProcedure,
			sessionactivityv1connect.SessionActivityServiceWatchActivityProcedure}})
	if err != nil {
		log.Fatal(err)
	}

	// The content of each tenant (dec-multi-tenancy): sessions, decisions, minutes, notes, tasks and
	// slide decks, in the repository of the tenant. A tenant is opened on its first call; the
	// tenants that exist are opened now, so their sessions are restored.
	if *gracePeriod <= 0 {
		log.Fatal("-release-grace-period must be positive")
	}
	rts := newRuntimes(repos, signingKey, *archiveInterval, *restoreWait, *gracePeriod)
	tenants, err := ids.Tenants(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, t := range tenants {
		if !t.Active() {
			continue
		}
		if _, err := rts.get(context.Background(), t.ID); err != nil {
			log.Fatal(err)
		}
	}

	cs := connect.NewServer(ids.Interceptor())
	sessioncommandsv1connect.RegisterSessionCommandsServiceHandler(cs, commandsMux{rts})
	sessionstreamv1connect.RegisterSessionStreamServiceHandler(cs, streamMux{rts})
	sessionimportv1connect.RegisterSessionImportServiceHandler(cs, importMux{rts})
	sessionactivityv1connect.RegisterSessionActivityServiceHandler(cs, activityMux{rts})
	sessionlistv1connect.RegisterSessionListServiceHandler(cs, listMux{rts})
	identityv1connect.RegisterIdentityServiceHandler(cs, ids)
	userv1connect.RegisterUserServiceHandler(cs, ids)
	tenantv1connect.RegisterTenantServiceHandler(cs, tenant.New(ids, repos))
	decisionv1connect.RegisterDecisionServiceHandler(cs, decisionMux{rts})
	minutesv1connect.RegisterMinutesServiceHandler(cs, minutesMux{rts})
	notev1connect.RegisterNoteServiceHandler(cs, noteMux{rts})
	taskv1connect.RegisterTaskServiceHandler(cs, taskMux{rts})
	slidesv1connect.RegisterSlidesServiceHandler(cs, slidesMux{rts})
	documentv1connect.RegisterDocumentServiceHandler(cs, documentMux{rts})
	archivev1connect.RegisterArchiveReadServiceHandler(cs, archiveReadMux{rts})
	changesv1connect.RegisterArchiveChangesServiceHandler(cs, archiveChangesMux{rts})
	wikiv1connect.RegisterWikiServiceHandler(cs, wikiMux{rts})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)
	// The health check needs no token and reads no state, so a container probe can use it.
	mux.Handle("GET "+health.Path, health.Handler())
	// Git access serves the artifact repository of each tenant at /git/<tenant>. It checks the
	// token (the password of Basic) and the membership itself, then hands the request to Git.
	gitHandler, err := gitaccess.New(gitaccess.Options{
		Tokens: ids, Repositories: repos, Logf: log.Printf,
		HooksDir:     filepath.Join(*dataDir, "git-hooks"),
		MaxPushBytes: *gitMaxPushMiB << 20,
		MaxFileBytes: *gitMaxFileKiB << 10,
		MaxFiles:     *gitMaxFiles,
	})
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle(gitweb.PathPrefix, gitHandler)
	// The portal asks the interfaces in-process, with the token of the user (dec-web-portal).
	mux.Handle(portalweb.PathPrefix, portal.New(portal.Options{Hub: connectinprocess.New(cs)}))
	// The stage subscribes in-process through the token check, with the token of the link.
	mux.Handle(web.PathPrefix, stage.New(stage.Options{
		Session: connectinprocess.New(cs),
		StageSession: func(token string) (stage.StageLink, error) {
			c, err := ids.CheckToken(token)
			if err != nil || c.StageSessionID == "" {
				return stage.StageLink{}, err
			}
			// The link names its tenant: the stage shows the decks of this tenant.
			rt, err := rts.get(context.Background(), c.TenantID)
			if err != nil {
				return stage.StageLink{}, err
			}
			return stage.StageLink{SessionID: c.StageSessionID, Slides: rt.slides, Artifacts: rt.archiveRead}, nil
		},
	}))

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Handler: mux, Protocols: p, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		// Open session streams end when the server stops.
		BaseContext: func(net.Listener) context.Context { return ctx }}
	shutDown := make(chan struct{})
	go func() {
		defer close(shutDown)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	// Tests and scripts read this line to learn the address.
	log.Printf("office-hub listening on https://%s", ln.Addr())
	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutDown
	rts.stopAll()
	log.Printf("office-hub stopped")
}
