// Command office-server runs the office server (bb-office-server).
//
// All services are mounted, and the stage at /stage/<session>?token=<stage link token>. Every call
// needs an access token from the identity block, except GetChallenge and ExchangeChallenge. The server speaks only TLS. The meeting session keeps its
// logs in memory; the journal and the artifact tools keep their data in files.
//
// Files in the data directory (-data-dir):
//
//	admins       admin keys, written by hand (see identity.LoadAdmins)
//	identity.json registered participants and keys, written by the server
//	journal.jsonl journal entries, one JSON object per line, appended by the server
//	artifacts.jsonl artifacts (rendered slides), one JSON object per line, appended by the server
//	tls.crt      TLS certificate; created self-signed if neither it nor tls.key exists
//	tls.key      TLS private key
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

	"github.com/mrsimpson/agentic-office/server/artifacts"
	"github.com/mrsimpson/agentic-office/server/artifacts/api/artifactv1connect"
	"github.com/mrsimpson/agentic-office/server/identity"
	"github.com/mrsimpson/agentic-office/server/identity/api/identityv1connect"
	"github.com/mrsimpson/agentic-office/server/journal"
	"github.com/mrsimpson/agentic-office/server/journal/api/journalv1connect"
	"github.com/mrsimpson/agentic-office/server/session"
	"github.com/mrsimpson/agentic-office/server/session/api/sessionv1connect"
	"github.com/mrsimpson/agentic-office/server/stage"
)

func defaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "agentic-office", "server")
	}
	return "office-server-data"
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8740", "listen address (port 0 picks a free port)")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for admin keys, registered keys, the journal, the artifacts and the TLS certificate")
	certFile := flag.String("tls-cert", "", "TLS certificate (PEM, may include the chain); default <data-dir>/tls.crt")
	keyFile := flag.String("tls-key", "", "TLS private key (PEM); default <data-dir>/tls.key")
	hostname, _ := os.Hostname()
	tlsHosts := flag.String("tls-hosts", "localhost,127.0.0.1,::1,"+hostname, "host names and IPs of a new self-signed certificate")
	adminsFile := flag.String("admins", "", "admin key file; default <data-dir>/admins")
	publicURL := flag.String("public-url", "", "base URL of the server in stage links; default https://<listen address>")
	tokenTTL := flag.Duration("token-ttl", 15*time.Minute, "lifetime of access tokens")
	stageTTL := flag.Duration("stage-link-ttl", 24*time.Hour, "lifetime of stage links")
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

	journalStore, err := journal.OpenFileStore(filepath.Join(*dataDir, "journal.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	defer journalStore.Close()
	jrn := journal.New(journalStore, journal.TokenCheck())

	artifactStore, err := artifacts.OpenFileStore(filepath.Join(*dataDir, "artifacts.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	defer artifactStore.Close()
	arts := artifacts.New(artifactStore, artifacts.TokenCheck())

	cs := connect.NewServer(ids.Interceptor())
	sessionv1connect.RegisterSessionServiceHandler(cs, session.New(session.NewMemoryStore(), session.TokenCheck(), jrn))
	identityv1connect.RegisterIdentityServiceHandler(cs, ids)
	journalv1connect.RegisterJournalServiceHandler(cs, jrn)
	artifactv1connect.RegisterArtifactServiceHandler(cs, arts)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)
	// The stage subscribes in-process through the token check, with the token of the link.
	mux.Handle(stage.PathPrefix, stage.New(stage.Options{
		Session: connectinprocess.New(cs),
		StageSession: func(token string) (string, error) {
			c, err := ids.CheckToken(token)
			return c.StageSessionID, err
		},
		Artifacts: arts,
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
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	// Tests and scripts read this line to learn the address.
	log.Printf("office-server listening on https://%s", ln.Addr())
	if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Printf("office-server stopped")
}
