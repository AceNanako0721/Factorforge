package trading_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func buildNative(t *testing.T, packagePath, name string) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", output, packagePath)
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOTOOLCHAIN=local")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native build failed: %s", data)
	}
	return output
}

func requireNetworkNamespace(t *testing.T) {
	t.Helper()
	probe := exec.Command("/usr/bin/unshare", "--user", "--map-root-user", "--net", "/bin/true")
	data, err := probe.CombinedOutput()
	if err == nil {
		return
	}
	if strings.Contains(string(data), "Operation not permitted") || strings.Contains(string(data), "Permission denied") {
		t.Skip("host denies network namespaces; runtime still fails closed; deployment-host isolation is verified separately")
	}
	t.Fatalf("namespace capability probe failed: %s", data)
}
func TestRevocableTLSGateway(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Write([]byte("verified"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "gateway.sock")
	g, err := isolation.NewEgress(path, "example.com:443", func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	token, err := g.Issue()
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client, err := isolation.TunnelClient(path, token, "example.com:443", 3*time.Second, roots)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "verified" || g.AllowedConnections() != 1 {
		t.Fatal("TLS tunnel failed", err)
	}
	response, err = client.Get("https://example.com/slow")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	g.Revoke(token)
	_, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err == nil {
		t.Fatal("revoked established tunnel remained readable")
	}
	if _, err = client.Get("https://example.com/"); err == nil {
		t.Fatal("revoked token reconnected")
	}
	if _, err = client.Get("https://other.example/"); err == nil {
		t.Fatal("destination escaped")
	}
	fresh, _ := g.Issue()
	next, _ := isolation.TunnelClient(path, fresh, "example.com:443", time.Second, roots)
	response, err = next.Get("https://example.com/")
	if err != nil {
		t.Fatal("unrelated permit revoked", err)
	}
	response.Body.Close()
	next.CloseIdleConnections()
	client.CloseIdleConnections()
}
func TestNativeKernelIsolation(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatal("Linux process boundary required")
	}
	probe := buildNative(t, "./tests/trading/processprobe", "probe")
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	private := filepath.Join(t.TempDir(), "private.toml")
	os.WriteFile(private, []byte("synthetic-secret-content"), 0600)
	signer := exec.Command(probe, "--mode", "idle")
	signer.Env = []string{"PATH=/nonexistent", "SYNTHETIC_SECRET=synthetic"}
	pipe, err := signer.StdoutPipe()
	if err != nil || signer.Start() != nil {
		t.Fatal("signer probe start failed")
	}
	defer func() { signer.Process.Kill(); signer.Wait() }()
	ready := make([]byte, 6)
	if _, err = io.ReadFull(pipe, ready); err != nil {
		t.Fatal(err)
	}
	proc := "/proc/" + strconv.Itoa(signer.Process.Pid) + "/environ"
	if _, err = os.ReadFile(proc); err != nil {
		t.Fatal("process fixture inaccessible")
	}
	profile := filepath.Join(t.TempDir(), "profile.json")
	settings, _ := json.Marshal(map[string]string{"DeniedFile": private, "DeniedProc": proc, "DSN": server.SIMDSN})
	os.WriteFile(profile, settings, 0600)
	child := exec.Command(probe, "--mode", "filesystem", "--profile", profile)
	child.Env = []string{"PATH=/nonexistent"}
	data, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("kernel isolation failed: %s / %v", data, err)
	}
	var proof map[string]bool
	if json.Unmarshal(data, &proof) != nil || len(proof) != 3 {
		t.Fatal("isolation receipt missing")
	}
	for field, ok := range proof {
		if !ok {
			t.Fatal(field)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requireNetworkNamespace(t)
	unshare := "/usr/bin/unshare"
	if _, err = os.Stat(unshare); err != nil {
		t.Fatal("native unshare required")
	}
	child = exec.Command(unshare, "--user", "--map-root-user", "--net", probe, "--mode", "network", "--target", listener.Addr().String())
	child.Env = []string{"PATH=/nonexistent"}
	data, err = child.CombinedOutput()
	if err != nil || !strings.Contains(string(data), "NETWORK_DENIED") {
		t.Fatalf("namespace failed: %s / %v", data, err)
	}
}
