// Command office-server runs the office server (bb-office-server).
//
// All services are mounted. Services that are not implemented yet answer every call with the
// Connect code "unimplemented".
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/core/api/office/v1/officev1connect"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8740", "listen address (port 0 picks a free port)")
	flag.Parse()

	cs := connect.NewServer()
	officev1connect.RegisterSessionServiceHandler(cs, officev1connect.UnimplementedSessionServiceHandler{})
	officev1connect.RegisterIdentityServiceHandler(cs, officev1connect.UnimplementedIdentityServiceHandler{})
	officev1connect.RegisterJournalServiceHandler(cs, officev1connect.UnimplementedJournalServiceHandler{})
	officev1connect.RegisterArtifactServiceHandler(cs, officev1connect.UnimplementedArtifactServiceHandler{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: mux, Protocols: p, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	// Tests and scripts read this line to learn the address.
	log.Printf("office-server listening on http://%s", ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Printf("office-server stopped")
}
