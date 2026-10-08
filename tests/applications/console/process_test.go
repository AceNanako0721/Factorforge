package console_test

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/api"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"github.com/pelletier/go-toml/v2"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIndependentHTTPSNativeConsoleAndPrivateProfile(t *testing.T) {
	w := world(t)
	root, _ := filepath.Abs("../../..")
	dir := t.TempDir()
	binary := filepath.Join(dir, "console-api")
	build := exec.Command("go", "build", "-o", binary, "./src/factorforge/applications/console/entrypoints/console-api")
	build.Dir = root
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	template := httptest.NewTLSServer(http.NotFoundHandler())
	defer template.Close()
	certificate := template.TLS.Certificates[0]
	key, e := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0600)
	web := filepath.Join(dir, "web")
	os.Mkdir(web, 0700)
	os.WriteFile(filepath.Join(web, "index.html"), []byte("<!doctype html><html><body>Fixture</body></html>"), 0600)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	origin := "https://127.0.0.1:" + strconv.Itoa(port)
	s := w.Selection
	p := config.Profile{SchemaVersion: 1, Origin: origin, Host: "127.0.0.1", Port: port, TLSCertFile: certFile, TLSKeyFile: keyFile, StaticDir: web, FixtureOnly: true, SessionSeconds: 3600, ChallengeSeconds: 60, RateWindowSeconds: 60, CursorSeconds: 60, TimeoutSeconds: 10, RefreshSeconds: 60, MaxRetries: 0, MaxAttempts: 10, MaxSessions: 20, MaxChallenges: 100, MaxBytes: 1 << 20, MaxRecords: 1000, DefaultLimit: 2, MaxLimit: 20, MaxPages: 10, MaxHashIterations: 1000, CursorKey: strings.Repeat("fixture-", 4), Users: []d.User{w.User}, Bindings: []config.Binding{{SelectionID: s.ID, DeploymentID: s.DeploymentID, Environment: s.Environment, InstanceID: s.InstanceID, ObjectID: s.ObjectID, AccountID: s.TradingRunKey.AccountID, RunID: s.TradingRunKey.RunID, Venue: s.InstrumentKey.Venue, Product: s.InstrumentKey.Product, InstrumentID: s.InstrumentKey.InstrumentID, OwnerID: s.OwnerID, BindingVersion: s.BindingVersion, Services: w.Client.Bindings[s.ID]}}}
	raw, e := toml.Marshal(map[string]any{"console": p, "credentials": map[string]string{"jev_api_key": "private-canary"}})
	if e != nil {
		t.Fatal(e)
	}
	canonical := filepath.Join(dir, "config.toml")
	os.WriteFile(canonical, raw, 0600)
	profile := filepath.Join(dir, "profile.json")
	if e = config.Prepare(canonical, profile); e != nil {
		t.Fatal(e)
	}
	derived, _ := os.ReadFile(profile)
	if bytes.Contains(derived, []byte("private-canary")) || bytes.Contains(derived, []byte("jev_api_key")) {
		t.Fatal("peer credential copied")
	}
	if e = config.Prepare(canonical, profile); e == nil {
		t.Fatal("profile overwritten")
	}
	if _, e = config.Load(canonical); e == nil {
		t.Fatal("canonical file accepted as runtime profile")
	}
	child := exec.Command(binary, "--profile", profile)
	child.Env = []string{"PATH=/nonexistent"}
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	var stopped sync.Once
	stopChild := func() { stopped.Do(func() { child.Process.Signal(os.Interrupt); child.Wait() }) }
	defer stopChild()
	client := template.Client()
	client.Jar, _ = cookiejar.New(nil)
	ready := false
	for i := 0; i < 100; i++ {
		resp, e := client.Get(origin + "/login")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("native console unavailable")
	}
	call := func(method, path string, body []byte) (int, []byte) {
		t.Helper()
		request, _ := http.NewRequest(method, origin+api.Prefix+path, bytes.NewReader(body))
		request.Header.Set("Origin", origin)
		request.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}
	status, raw := call("GET", "/session/challenge", nil)
	var ch d.Challenge
	json.Unmarshal(raw, &ch)
	if status != 200 {
		t.Fatal(status)
	}
	body, _ := json.Marshal(map[string]string{"username": "fixture-user", "password": "fixture-pass", "csrf": ch.CSRF})
	status, _ = call("POST", "/session", body)
	if status != 200 {
		t.Fatal(status)
	}
	status, raw = call("GET", "/overview?selection_id="+s.ID, nil)
	if status != 200 || !bytes.Contains(raw, []byte(`"equity":"999.900"`)) {
		t.Fatal("native actual SIM read", status, string(raw))
	}
	if status, _ = call("POST", "/events", []byte(`{}`)); status != 405 {
		t.Fatal("write accepted")
	}
	if resp, e := client.Get(origin + "/api/not-a-console-route"); e != nil || resp.StatusCode != 404 {
		t.Fatal("API SPA fallback")
	}
	stopChild()
	if w.Writes.Load() != 0 || strings.Contains(output.String(), "fixture-p1-read") {
		t.Fatal("write or credential log")
	}
}
