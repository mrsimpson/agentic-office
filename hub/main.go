// Command office-hub runs the office hub (bb-office-hub).
//
// All services are mounted, and the stage at /stage/<session>?token=<stage link token>. Every call
// needs an access token from the identity block, except GetChallenge and ExchangeChallenge. The server speaks only TLS. The meeting session keeps its
// logs in memory, signs their tree heads with the signing key and writes them to the archive (a local Git
// repository; the push is mocked). The records (decisions, minutes and notes), the tasks (local
// drafts) and the slide decks are files in the archive too.
//
// Files in the data directory (-data-dir):
//
//	admins       admin keys, written by hand (see identity.LoadAdmins)
//	identity.json registered participants and keys, written by the server
//	tls.crt      TLS certificate; created self-signed if neither it nor tls.key exists
//	tls.key      TLS private key
//	signing.key  Ed25519 key that signs the tree heads of session logs; created if it does not exist
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
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectinprocess"

	"github.com/mrsimpson/agentic-office/hub/archive"
	"github.com/mrsimpson/agentic-office/hub/identity"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
	"github.com/mrsimpson/agentic-office/hub/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/hub/records"
	"github.com/mrsimpson/agentic-office/hub/records/decision/api/decisionv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/minutes/api/minutesv1connect"
	"github.com/mrsimpson/agentic-office/hub/records/note/api/notev1connect"
	"github.com/mrsimpson/agentic-office/hub/session"
	"github.com/mrsimpson/agentic-office/hub/session/api/sessionv1connect"
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

func main() {
	addr := flag.String("addr", "127.0.0.1:8740", "listen address (port 0 picks a free port)")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for admin keys, registered keys, the archive and the TLS certificate")
	certFile := flag.String("tls-cert", "", "TLS certificate (PEM, may include the chain); default <data-dir>/tls.crt")
	keyFile := flag.String("tls-key", "", "TLS private key (PEM); default <data-dir>/tls.key")
	hostname, _ := os.Hostname()
	tlsHosts := flag.String("tls-hosts", "localhost,127.0.0.1,::1,"+hostname, "host names and IPs of a new self-signed certificate")
	signingKeyFile := flag.String("signing-key", "", "Ed25519 key that signs tree heads of session logs (PEM); default <data-dir>/signing.key, created if missing")
	adminsFile := flag.String("admins", "", "admin key file; default <data-dir>/admins")
	publicURL := flag.String("public-url", "", "base URL of the server in stage links; default https://<listen address>")
	tokenTTL := flag.Duration("token-ttl", 15*time.Minute, "lifetime of access tokens")
	stageTTL := flag.Duration("stage-link-ttl", 24*time.Hour, "lifetime of stage links")
	archiveDir := flag.String("archive-dir", "", "Git repository of the archive; default <data-dir>/archive, created if missing")
	archiveInterval := flag.Duration("archive-interval", 30*time.Second, "time between two writes of changed sessions to the archive; sessions are also written when they end")
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

	// The signing key must survive restarts, so old tree heads still verify (dec-merkle-log).
	signingKey, created, err := auth.LoadOrCreateKey(or(*signingKeyFile, "signing.key"))
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("created the signing key %s", or(*signingKeyFile, "signing.key"))
	}
	log.Printf("signing key of tree heads %s", auth.FormatPublicKey(auth.PublicKey(signingKey)))

	admins, err := identity.LoadAdmins(or(*adminsFile, "admins"))
	if err != nil {
		log.Fatal(err)
	}
	if len(admins) == 0 {
		log.Printf("no admin keys in %s: nobody can register keys", or(*adminsFile, "admins"))
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	if *publicURL == "" {
		*publicURL = "https://" + ln.Addr().String()
	}
	ids, err := identity.New(identity.Options{TokenTTL: *tokenTTL, StageLinkTTL: *stageTTL, PublicURL: *publicURL,
		StateFile: filepath.Join(*dataDir, "identity.json"), Admins: admins,
		StageProcedures: []string{sessionv1connect.SessionServiceSubscribeProcedure}})
	if err != nil {
		log.Fatal(err)
	}

	// The archive (dec-yaml-git-storage): a local Git repository; the push is mocked (#43).
	arch, err := archive.Open(context.Background(), archive.Options{Dir: or(*archiveDir, "archive"), Push: archive.NoPush{}})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("archive %s, sessions written every %s and when they end; push mocked", arch.Dir(), *archiveInterval)
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
	if err := sessions.UseArchive(session.ArchiveOptions{Archive: arch, Interval: *archiveInterval, Logf: log.Printf}); err != nil {
		log.Fatal(err)
	}
	// The writer runs until the server has stopped, then writes a last time.
	archCtx, archStop := context.WithCancel(context.Background())
	archDone := make(chan struct{})
	go func() { sessions.RunArchive(archCtx); close(archDone) }()

	cs := connect.NewServer(ids.Interceptor())
	sessionv1connect.RegisterSessionServiceHandler(cs, sessions)
	identityv1connect.RegisterIdentityServiceHandler(cs, ids)
	decisionv1connect.RegisterDecisionServiceHandler(cs, recs)
	minutesv1connect.RegisterMinutesServiceHandler(cs, recs)
	notev1connect.RegisterNoteServiceHandler(cs, recs)
	taskv1connect.RegisterTaskServiceHandler(cs, tks)
	slidesv1connect.RegisterSlidesServiceHandler(cs, decks)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)
	// The stage subscribes in-process through the token check, with the token of the link.
	mux.Handle(stage.PathPrefix, stage.New(stage.Options{
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
