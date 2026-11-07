package contracttest

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mrsimpson/agentic-office/server/identity/api/auth"
)

// Proxy is a TCP proxy between clients and a server. Tests point a client at Proxy.URL instead
// of the server to cut its network (Cut, Restore) and to count or record what it sends.
//
// A plain proxy (StartProxy) forwards the TLS bytes as they are and only counts them. A
// decrypting proxy (StartDecryptingProxy) ends TLS with the server's own certificate and key,
// opens its own TLS connection to the server, and records the plaintext that the client sends.
// Clients do not notice: they trust the server certificate, and the proxy presents exactly that
// certificate. It is the test version of a TLS-intercepting proxy.
type Proxy struct {
	// URL is the base URL for clients, with the host and port of the proxy.
	URL string

	t       testing.TB
	ln      net.Listener
	target  string // host:port of the server
	srvTLS  *tls.Config
	cliTLS  *tls.Config
	up      atomic.Int64
	down    atomic.Int64
	refused atomic.Int64

	mu       sync.Mutex
	cut      bool
	conns    map[net.Conn]struct{}
	captured []*bytes.Buffer
}

// StartProxy starts a plain TCP proxy to the server at serverURL on a free local port.
func StartProxy(t testing.TB, serverURL string) *Proxy {
	t.Helper()
	return startProxy(t, serverURL, nil, nil)
}

// StartDecryptingProxy starts a proxy that ends TLS with certFile and keyFile (the server's own
// pair) and records the plaintext from the client (Captured).
func StartDecryptingProxy(t testing.TB, serverURL, certFile, keyFile string) *Proxy {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := auth.TLSConfig(certFile)
	if err != nil {
		t.Fatal(err)
	}
	srv := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}}
	return startProxy(t, serverURL, srv, cli)
}

func startProxy(t testing.TB, serverURL string, srvTLS, cliTLS *tls.Config) *Proxy {
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Proxy{t: t, ln: ln, target: u.Host, srvTLS: srvTLS, cliTLS: cliTLS, conns: map[net.Conn]struct{}{}}
	p.URL = u.Scheme + "://" + ln.Addr().String()
	go p.accept()
	t.Cleanup(func() {
		ln.Close()
		p.Cut()
	})
	return p
}

func (p *Proxy) accept() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		if p.cut {
			p.mu.Unlock()
			p.refused.Add(1)
			c.Close()
			continue
		}
		p.conns[c] = struct{}{}
		p.mu.Unlock()
		go p.serve(c)
	}
}

// countConn counts the bytes on the wire of a client connection.
type countConn struct {
	net.Conn
	p *Proxy
}

func (c countConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.p.up.Add(int64(n))
	return n, err
}

func (c countConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.p.down.Add(int64(n))
	return n, err
}

func (p *Proxy) serve(raw net.Conn) {
	defer p.forget(raw)
	c := countConn{Conn: raw, p: p}
	var client io.ReadWriteCloser = c
	var rec *bytes.Buffer
	var server net.Conn
	var err error
	if p.srvTLS == nil {
		server, err = net.Dial("tcp", p.target)
	} else {
		tc := tls.Server(c, p.srvTLS)
		if tc.Handshake() != nil {
			return
		}
		client = tc
		cfg := p.cliTLS.Clone()
		if proto := tc.ConnectionState().NegotiatedProtocol; proto != "" {
			cfg.NextProtos = []string{proto}
		}
		server, err = tls.Dial("tcp", p.target, cfg)
		rec = &bytes.Buffer{}
		p.mu.Lock()
		p.captured = append(p.captured, rec)
		p.mu.Unlock()
	}
	if err != nil {
		return
	}
	p.mu.Lock()
	if p.cut {
		p.mu.Unlock()
		server.Close()
		return
	}
	p.conns[server] = struct{}{}
	p.mu.Unlock()
	defer p.forget(server)

	done := make(chan struct{}, 2)
	go func() {
		p.copy(server, client, rec)
		done <- struct{}{}
	}()
	go func() {
		p.copy(client, server, nil)
		done <- struct{}{}
	}()
	<-done
	raw.Close()
	server.Close()
	<-done
}

// copy forwards from src to dst and records what it forwards in rec (if not nil).
func (p *Proxy) copy(dst io.Writer, src io.Reader, rec *bytes.Buffer) {
	buf := make([]byte, 32*1024)
	for {
		k, err := src.Read(buf)
		if k > 0 {
			if rec != nil {
				p.mu.Lock()
				rec.Write(buf[:k])
				p.mu.Unlock()
			}
			if _, werr := dst.Write(buf[:k]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *Proxy) forget(c net.Conn) {
	p.mu.Lock()
	delete(p.conns, c)
	p.mu.Unlock()
	c.Close()
}

// Cut drops the network: it closes all open connections and refuses new ones until Restore.
func (p *Proxy) Cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cut = true
	for c := range p.conns {
		c.Close()
	}
}

// Restore accepts connections again after Cut.
func (p *Proxy) Restore() {
	p.mu.Lock()
	p.cut = false
	p.mu.Unlock()
}

// UpstreamBytes is the number of bytes that the clients sent on the wire (TLS records included).
func (p *Proxy) UpstreamBytes() int64 { return p.up.Load() }

// DownstreamBytes is the number of bytes that the clients received on the wire.
func (p *Proxy) DownstreamBytes() int64 { return p.down.Load() }

// Refused is the number of connections that were refused while the proxy was cut.
func (p *Proxy) Refused() int64 { return p.refused.Load() }

// Captured returns the plaintext that the clients sent, one byte slice per connection. Only a
// decrypting proxy records it.
func (p *Proxy) Captured() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]byte, len(p.captured))
	for i, b := range p.captured {
		out[i] = bytes.Clone(b.Bytes())
	}
	return out
}
