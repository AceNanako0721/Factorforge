package trading_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/isolation"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"github.com/pelletier/go-toml/v2"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func probeAmount(t *testing.T, text string) decimal.Value {
	t.Helper()
	value, err := decimal.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestNativeIsolatedTestnetSignerAndAuthority(t *testing.T) {
	binary := buildNative(t, "./src/factorforge/trading/entrypoints/testnet-acceptance", "testnet-acceptance")
	refused := exec.Command(binary)
	refused.Env = []string{"PATH=/nonexistent"}
	data, err := refused.CombinedOutput()
	if err == nil || string(data) != "TESTNET_ORDER_AUTHORIZATION_REQUIRED\n" {
		t.Fatalf("testnet authority gate failed: %s", data)
	}
	requireNetworkNamespace(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	run, _, _, _ := serviceBase(t)
	run.RunKey.Environment = "LIVE"
	run.ExecutionMode = "LIVE"
	run.Policy.Version = "EXPERIMENT_ONLY"
	if run.Policy.Operational == nil {
		run.Policy.Operational = &d.OperationalPolicy{MinDiskBytes: 1, MaxClockSkewSeconds: probeAmount(t, "2"), MaxAuditRecords: 10000, MaxPendingCommands: 100, MaxCommandAgeSeconds: 300, LeaseSeconds: 180}
	}
	store, err := postgres.Open(ctx, database.LIVEDSN, "LIVE")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	private := configuration.Config{}
	private.Runtime.Environment = "LIVE"
	private.Services.ExchangeAPIURL = binance.TestnetURL
	private.Credentials.ExchangeAPIKey = "synthetic-native-key"
	private.Credentials.ExchangeAPISecret = "synthetic-native-secret"
	folder := t.TempDir()
	profile := filepath.Join(folder, "exchange.toml")
	data, err = toml.Marshal(private)
	if err != nil || os.WriteFile(profile, data, 0600) != nil {
		t.Fatal("synthetic private profile failed")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic testnet"}, DNSNames: []string{"demo-fapi.binance.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(folder, "synthetic-ca.pem")
	if os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600) != nil {
		t.Fatal("synthetic trust fixture failed")
	}
	t.Setenv("SSL_CERT_FILE", ca)
	var signed atomic.Int64
	venue := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-MBX-APIKEY") != "synthetic-native-key" || r.URL.Query().Get("signature") == "" {
			t.Error("native signer absent")
		}
		signed.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"canTrade": true, "dualSidePosition": false, "multiAssetsMargin": false})
	}))
	venue.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	venue.StartTLS()
	defer venue.Close()
	gatewayPath := filepath.Join(folder, "gate.sock")
	gateway, err := isolation.NewEgress(gatewayPath, "demo-fapi.binance.com:443", func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, venue.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Close()
	token, err := gateway.Issue()
	if err != nil {
		t.Fatal(err)
	}
	manifest := binance.ProbeManifest{Endpoint: binance.TestnetURL, Config: profile, Gateway: gatewayPath, Token: token, DSN: database.LIVEDSN, Key: run.RunKey, Holder: "native-probe", ExpiresAt: time.Now().Add(time.Minute), Symbol: "TESTUSD", MaxQuantity: probeAmount(t, "10"), MaxNotional: run.Policy.NotionalLimit}
	var logs processLog
	remote, err := binance.StartRemoteProbe(ctx, binary, manifest, &logs)
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	result, err := remote.Call(ctx, "raw", "/fapi/v1/accountConfig", binance.Params{})
	if err != nil {
		t.Fatal("native signed TLS query failed", err, logs.String())
	}
	if !json.Valid(result) || signed.Load() != 1 {
		t.Fatal("signed receipt missing")
	}
	if _, err = remote.Call(ctx, "claim"); err != nil {
		t.Fatal("native lease failed", err)
	}
	var problem *d.Error
	_, err = remote.Call(ctx, "manual_reduce", binance.Params{})
	if !errors.As(err, &problem) || problem.Code != "TESTNET_ORDER_AUTHORIZATION_REQUIRED" || signed.Load() != 1 {
		t.Fatal("readonly permit wrote venue", err)
	}
	_, err = remote.Call(ctx, "raw", "/fapi/v1/leverage", binance.Params{})
	if !errors.As(err, &problem) || problem.Code != "TESTNET_READ_SCOPE" || signed.Load() != 1 {
		t.Fatal("read allowlist escaped", err)
	}
	gateway.Revoke(token)
	_, err = remote.Call(ctx, "raw", "/fapi/v1/accountConfig", binance.Params{})
	if err == nil || signed.Load() != 1 {
		t.Fatal("revoked signer reconnected")
	}
	remote.Close()
	if _, err = os.Stat("/proc/" + big.NewInt(int64(remote.PID())).String()); !os.IsNotExist(err) {
		t.Fatal("native signer did not exit")
	}
}
