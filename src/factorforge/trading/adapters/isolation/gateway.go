// Package isolation supplies a revocable Unix-socket TLS tunnel. A signing
// process in an empty network namespace has no alternate outbound path.
package isolation

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type pair struct {
	token         string
	local, remote net.Conn
}
type Egress struct {
	path, destination string
	listener          net.Listener
	Dial              func(context.Context, string, string) (net.Conn, error)
	mu                sync.Mutex
	tokens            map[string]bool
	active            map[net.Conn]pair
	accepted          map[net.Conn]bool
	closed            bool
	wg                sync.WaitGroup
	allowed           int
}

func fail(code string) error { return &d.Error{Code: code, Status: 503} }
func NewEgress(path, destination string, dial func(context.Context, string, string) (net.Conn, error)) (*Egress, error) {
	host, port, err := net.SplitHostPort(destination)
	if err != nil || host == "" || port != "443" || strings.ContainsAny(host, "/\\\r\n") {
		return nil, fail("GATEWAY_DESTINATION_FORBIDDEN")
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fail("GATEWAY_UNAVAILABLE")
	}
	if err = os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, fail("GATEWAY_UNAVAILABLE")
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	g := &Egress{path: path, destination: destination, listener: listener, Dial: dial, tokens: map[string]bool{}, active: map[net.Conn]pair{}, accepted: map[net.Conn]bool{}}
	g.wg.Add(1)
	go g.serve()
	return g, nil
}
func (g *Egress) Issue() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fail("EGRESS_PERMIT_UNAVAILABLE")
	}
	token := hex.EncodeToString(data)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return "", fail("GATEWAY_UNAVAILABLE")
	}
	g.tokens[token] = true
	return token, nil
}
func (g *Egress) Revoke(token string) {
	g.mu.Lock()
	delete(g.tokens, token)
	pairs := []pair{}
	for _, p := range g.active {
		if p.token == token {
			pairs = append(pairs, p)
		}
	}
	g.mu.Unlock()
	for _, p := range pairs {
		p.local.Close()
		p.remote.Close()
	}
}
func (g *Egress) AllowedConnections() int { g.mu.Lock(); defer g.mu.Unlock(); return g.allowed }
func (g *Egress) Close() {
	g.mu.Lock()
	g.closed = true
	tokens := []string{}
	for token := range g.tokens {
		tokens = append(tokens, token)
	}
	connections := make([]net.Conn, 0, len(g.accepted))
	for connection := range g.accepted {
		connections = append(connections, connection)
	}
	g.mu.Unlock()
	for _, token := range tokens {
		g.Revoke(token)
	}
	g.listener.Close()
	for _, connection := range connections {
		connection.Close()
	}
	g.wg.Wait()
	os.Remove(g.path)
}
func (g *Egress) serve() {
	defer g.wg.Done()
	for {
		connection, err := g.listener.Accept()
		if err != nil {
			return
		}
		g.mu.Lock()
		if g.closed {
			g.mu.Unlock()
			connection.Close()
			return
		}
		g.accepted[connection] = true
		g.wg.Add(1)
		g.mu.Unlock()
		go g.handle(connection)
	}
}
func (g *Egress) handle(local net.Conn) {
	defer g.wg.Done()
	defer local.Close()
	defer func() { g.mu.Lock(); delete(g.accepted, local); g.mu.Unlock() }()
	local.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReaderSize(io.LimitReader(local, 8193), 8193)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	g.mu.Lock()
	token := ""
	for candidate := range g.tokens {
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("executor:"+candidate))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(request.Header.Get("Proxy-Authorization"))) == 1 {
			token = candidate
			break
		}
	}
	g.mu.Unlock()
	if request.Method != "CONNECT" || request.Host != g.destination || request.URL.Host != g.destination || token == "" || reader.Buffered() != 0 {
		io.WriteString(local, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	remote, err := g.Dial(ctx, "tcp", g.destination)
	cancel()
	if err != nil {
		return
	}
	defer remote.Close()
	g.mu.Lock()
	if !g.tokens[token] || g.closed {
		g.mu.Unlock()
		return
	}
	g.active[local] = pair{token, local, remote}
	g.allowed++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); delete(g.active, local); g.mu.Unlock() }()
	local.SetDeadline(time.Time{})
	if _, err = io.WriteString(local, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { io.Copy(remote, local); remote.Close(); close(done) }()
	io.Copy(local, remote)
	local.Close()
	<-done
}
func TunnelClient(path, token, destination string, timeout time.Duration, roots *x509.CertPool) (*http.Client, error) {
	host, port, err := net.SplitHostPort(destination)
	if err != nil || host == "" || port != "443" || token == "" || timeout <= 0 {
		return nil, fail("GATEWAY_DESTINATION_FORBIDDEN")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fail("GATEWAY_DESTINATION_FORBIDDEN")
	}
	transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != destination {
			return nil, fail("GATEWAY_DESTINATION_FORBIDDEN")
		}
		connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fail("GATEWAY_UNAVAILABLE")
		}
		okay := false
		defer func() {
			if !okay {
				connection.Close()
			}
		}()
		deadline := time.Now().Add(timeout)
		if at, ok := ctx.Deadline(); ok && at.Before(deadline) {
			deadline = at
		}
		connection.SetDeadline(deadline)
		authorization := base64.StdEncoding.EncodeToString([]byte("executor:" + token))
		if _, err = fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", destination, destination, authorization); err != nil {
			return nil, fail("GATEWAY_UNAVAILABLE")
		}
		reader := bufio.NewReader(connection)
		response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
		if err != nil || response.StatusCode != 200 || reader.Buffered() != 0 {
			return nil, fail("EGRESS_REVOKED_OR_FORBIDDEN")
		}
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, RootCAs: roots, MinVersion: tls.VersionTLS12})
		if err = tlsConnection.HandshakeContext(ctx); err != nil {
			return nil, fail("GATEWAY_TLS_UNAVAILABLE")
		}
		connection.SetDeadline(time.Time{})
		okay = true
		return tlsConnection, nil
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
