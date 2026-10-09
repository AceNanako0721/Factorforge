package soxl_jev_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
)

func TestProviderRegistrationPrivateClosedBoundedAndNoLinks(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "runtime")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	registration := config.ProviderBudgetRegistration{Policy: providerPolicy(), Grants: []d.ProviderGrant{{Login: "fixture-worker", Binding: d.Binding{InstanceID: "fixture-instance", Environment: "SIM"}, QueueKind: "SIM", PoolID: "fixture-pool"}}}
	raw, _ := json.Marshal(registration)
	path := filepath.Join(private, "policy.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadProviderBudgetRegistration(root, path, len(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadProviderBudgetRegistration(root, path, len(raw)-1); err == nil {
		t.Fatal("byte budget ignored")
	}
	outside := filepath.Join(root, "outside.json")
	os.WriteFile(outside, raw, 0600)
	if _, err := config.LoadProviderBudgetRegistration(root, outside, 100000); err == nil {
		t.Fatal("outside runtime read")
	}
	for _, body := range []string{`{"unknown":true}`, `{"policy":{},"policy":{},"grants":[]}`} {
		os.WriteFile(path, []byte(body), 0600)
		if _, err := config.LoadProviderBudgetRegistration(root, path, 100000); err == nil {
			t.Fatal("open or duplicate JSON admitted")
		}
	}
	registration.Grants = append(registration.Grants, registration.Grants[0])
	raw, _ = json.Marshal(registration)
	os.WriteFile(path, raw, 0600)
	if _, err := config.LoadProviderBudgetRegistration(root, path, 100000); err == nil {
		t.Fatal("duplicate login accepted")
	}
	registration.Grants = registration.Grants[:1]
	registration.Grants[0].QueueKind = "LIVE"
	raw, _ = json.Marshal(registration)
	os.WriteFile(path, raw, 0600)
	if _, err := config.LoadProviderBudgetRegistration(root, path, 100000); err == nil {
		t.Fatal("environment upgraded")
	}
	link := filepath.Join(private, "linked.json")
	if err := os.Symlink(outside, link); err == nil {
		if _, err = config.LoadProviderBudgetRegistration(root, link, 100000); err == nil {
			t.Fatal("linked file read")
		}
	}
	directoryLink := filepath.Join(private, "linked-dir")
	if err := os.Symlink(root, directoryLink); err == nil {
		if _, err = config.LoadProviderBudgetRegistration(root, filepath.Join(directoryLink, "outside.json"), 100000); err == nil {
			t.Fatal("linked ancestor read")
		}
	}
}
