// Command office-server runs the spike of the office server (meeting session only).
package main

import (
	"flag"
	"log"
	"net"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/mrsimpson/agentic-office/gen/office/v1/officev1connect"
	"github.com/mrsimpson/agentic-office/internal/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8740", "listen address")
	flag.Parse()

	cs := connect.NewServer()
	officev1connect.RegisterSessionServiceHandler(cs, server.New())
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs)

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	// The e2e test reads this line to learn the port.
	log.Printf("office-server listening on http://%s", ln.Addr())
	log.Fatal((&http.Server{Handler: mux, Protocols: p}).Serve(ln))
}
