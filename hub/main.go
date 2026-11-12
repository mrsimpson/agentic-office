// Command office-hub runs the office hub (bb-office-hub).
//
// All services are mounted, and the stage at /stage/<session>?token=<stage link token>. Every call
// needs an access token from the identity block, except GetChallenge and ExchangeChallenge. The server speaks only TLS. The meeting session keeps its
// logs in memory, signs their tree heads with the signing key and writes them to the archive (a local Git
// repository; the push is mocked). The records (decisions, minutes and notes), the tasks (local
// drafts) and the slide decks are files in the archive too.
// The users, keys and memberships are files in the archive too (system/); at the first start the
// operator names the first system admin with -first-system-admin ID=PUBLIC_KEY.
//
// The operator gives the hub its signing key at start (dec-hub-signing-key): -signing-key FILE is
// required, and the hub does not start without a valid key. Create a key file once with
//
//	office-hub keygen FILE
//
// It refuses to overwrite an existing file.
//
// Files in the data directory (-data-dir):
//
//	tls.crt      TLS certificate; created self-signed if neither it nor tls.key exists
//	tls.key      TLS private key
//	archive/     Git repository of the archive (-archive-dir); the meeting session writes its logs
//	             there at the interval -archive-interval and when a session ends; decisions,
//	             minutes and notes are committed there one by one (see package records), and so
//	             is every new or changed task (see package tasks) and every new slide deck, with
//	             its input and its rendered output (see package slides)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
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
	"github.com/mrsimpson/agentic-office/hub/identity"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/identity/userapi/userv1connect"
	"github.com/mrsimpson/agentic-office/hub/records"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session"
	"github.com/mrsimpson/agentic-office/hub/session/commandsapi/sessioncommandsv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/importapi/sessionimportv1connect"
	"github.com/mrsimpson/agentic-office/hub/session/streamapi/sessionstreamv1connect"
	"github.com/mrsimpson/agentic-office/hub/slides"
	"github.com/mrsimpson/agentic-office/hub/slides/api/slidesv1connect"
	"github.com/mrsimpson/agentic-office/hub/stage"
	"github.com/mrsimpson/agentic-office/hub/tasks"
	"github.com/mrsimpson/agentic-office/hub/tasks/api/taskv1connect"
)

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "agentic-office", "hub")
	}
	return "office-hub-data"
}

// keygenHint tells the operator how to create a signing key file.
const keygenHint = "create one with: office-hub keygen FILE"

// keygen writes a new signing key to the file in args and prints its public key. It refuses to
// overwrite an existing file.
func keygen(args []string) {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(os.Stderr, "usage: office-hub keygen FILE")
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

func main() {
	if len(os.Args) > 1 && os.Args[1] == "keygen" {
		keygen(os.Args[2:])
		return
	}
	addr := flag.String("addr", "127.0.0.1:8740", "listen address (port 0 picks a free port)")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for admin keys, registered keys, the archive and the TLS certificate")
	certFile := flag.String("tls-cert", "", "TLS certificate (PEM, may include the chain); default <data-dir>/tls.crt")
	keyFile := flag.String("tls-key", "", "TLS private key (PEM); default <data-dir>/tls.key")
	hostname, _ := os.Hostname()
	tlsHosts := flag.String("tls-hosts", "localhost,127.0.0.1,::1,"+hostname, "host names and IPs of a new self-signed certificate")
	signingKeyFile := flag.String("signing-key", "", "Ed25519 key file that signs tree heads of session logs (PEM); required, "+keygenHint)
	firstAdmin := flag.String("first-system-admin", "", "ID=PUBLIC_KEY of the first system admin; needed at the first start, ignored later")
	publicURL := flag.String("public-url", "", "base URL of the server in stage links; default https://<listen address>")
	tokenTTL := flag.Duration("token-ttl", 15*time.Minute, "lifetime of access tokens")
	stageTTL := flag.Duration("stage-link-ttl", 24*time.Hour, "lifetime of stage links")
	archiveDir := flag.String("archive-dir", "", "Git repository of the archive; default <data-dir>/archive, created if missing")
	archiveInterval := flag.Duration("archive-interval", 30*time.Second, "time between two writes of changed sessions to the archive; sessions are also written when they end")
	restoreWait := flag.Duration("restore-wait", 10*time.Second, "after a restart, how long a restored session that has not ended waits for a peer to fill in the events after the last write to the archive")
	flag.Parse()
	or := func(v, def string) string {
		if v != "" {
			return v
		}
		return filepath.Join(*dataDir, def)
	}

	cert, created, err := loadOrCreateCert(or(*certFile, "tls.crt"), or(*keyFile, "tls.key"), strings.Split(*tlsHosts, ","))
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("created a self-signed TLS certificate %s for %s", or(*certFile, "tls.crt"), *tlsHosts)
	}
	log.Printf("TLS certificate %s sha256 %s", or(*certFile, "tls.crt"), fingerprint(cert))
	if warn := validityWarning(cert); warn != "" {
		log.Printf("warning: TLS certificate %s: %s", or(*certFile, "tls.crt"), warn)
	}

	// The operator gives the signing key (dec-hub-signing-key). It must stay the same across
	// restarts, so old tree heads still verify (dec-merkle-log).
	if *signingKeyFile == "" {
		log.Fatal("no signing key: pass -signing-key FILE; " + keygenHint)
	}
	signingKey, err := auth.ReadKey(*signingKeyFile)
	if err != nil {
		log.Fatalf("signing key: %v; %s", err, keygenHint)
	}
	log.Printf("signing key of tree heads %s", auth.FormatPublicKey(auth.PublicKey(signingKey)))

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	if *publicURL == "" {
		*publicURL = "https://" + ln.Addr().String()
	}
	// The archive (dec-yaml-git-storage): a local Git repository; the push is mocked (#43).
	arch, err := archive.Open(context.Background(), archive.Options{Dir: or(*archiveDir, "archive"), Push: archive.NoPush{}})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("archive %s, sessions written every %s and when they end; push mocked", arch.Dir(), *archiveInterval)
	var bootstrap *identity.Bootstrap
	if *firstAdmin != "" {
		if bootstrap, err = identity.ParseBootstrap(*firstAdmin); err != nil {
			log.Fatalf("-first-system-admin: %v", err)
		}
	}
	// Users, keys and memberships live in the hub repository (dec-multi-tenancy); the hub does
	// not start when the repository breaks a rule.
	ids, err := identity.New(context.Background(), identity.Options{TokenTTL: *tokenTTL, StageLinkTTL: *stageTTL,
		PublicURL: *publicURL, Archive: arch, Bootstrap: bootstrap, Logf: log.Printf,
		StageProcedures: []string{sessionstreamv1connect.SessionStreamServiceSubscribeProcedure}})
	if err != nil {
		log.Fatal(err)
	}

	// Decisions, minutes and notes: YAML files in the archive, read once at the start.
	recs, err := records.Open(context.Background(), arch)
	if err != nil {
		log.Fatal(err)
	}
	nd, nm, nn := recs.Counts()
	log.Printf("records: %d decisions, %d minutes, %d notes", nd, nm, nn)
	// Tasks: local drafts as YAML files in the archive, read once at the start (#40 syncs them later).
	tks, err := tasks.Open(context.Background(), arch)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("tasks: %d local drafts", tks.Count())
	// Slide decks: input as YAML and output as HTML in the archive, read once at the start.
	decks, err := slides.Open(context.Background(), arch)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("slides: %d decks", decks.Count())
	sessions := session.New(session.NewMemoryStore(), session.TokenCheck(), recs, signingKey)
	if err := sessions.UseArchive(context.Background(), session.ArchiveOptions{Archive: arch, Interval: *archiveInterval,
		RestoreWait: *restoreWait, Logf: log.Printf}); err != nil {
		log.Fatal(err)
	}
	// The writer runs until the server has stopped, then writes a last time.
	archCtx, archStop := context.WithCancel(context.Background())
	archDone := make(chan struct{})
	go func() { sessions.RunArchive(archCtx); close(archDone) }()

	cs := connect.NewServer(ids.Interceptor())
	sessioncommandsv1connect.RegisterSessionCommandsServiceHandler(cs, sessions)
	sessionstreamv1connect.RegisterSessionStreamServiceHandler(cs, sessions)
	sessionimportv1connect.RegisterSessionImportServiceHandler(cs, sessions)
	identityv1connect.RegisterIdentityServiceHandler(cs, ids)
	userv1connect.RegisterUserServiceHandler(cs, ids)
	decisionv1connect.RegisterDecisionServiceHandler(cs, recs)
	minutesv1connect.RegisterMinutesServiceHandler(cs, recs)
	notev1connect.RegisterNoteServiceHandler(cs, recs)
	taskv1connect.RegisterTaskServiceHandler(cs, tks)
	slidesv1connect.RegisterSlidesServiceHandler(cs, decks)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)
	// The stage subscribes in-process through the token check, with the token of the link.
	mux.Handle(web.PathPrefix, stage.New(stage.Options{
		Session: connectinprocess.New(cs),
		StageSession: func(token string) (string, error) {
			c, err := ids.CheckToken(token)
			return c.StageSessionID, err
		},
		Slides: decks,
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
	archStop()
	<-archDone
	log.Printf("office-hub stopped")
}
