package engineering_test

import (
	"bytes"
	guard "github.com/AceNanako0721/Factorforge/tools/repoguard"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicGuardIncludesOAuthCredentialAssignments(t *testing.T) {
	for _, key := range []string{"refresh_token", "id_token", "access_token"} {
		issues := guard.ContentIssues("tests/fixture.ts", []byte(key+`: "`+strings.Repeat("A", 24)+`"`), false)
		if !strings.Contains(strings.Join(issues, ","), "credential-assignment") {
			t.Fatal("OAuth credential was accepted", key)
		}
	}
}

func TestModelConfigInitializationPreservesPrivateBytesAndRespectsLock(t *testing.T) {
	root := repoRoot(t)
	binary := filepath.Join(t.TempDir(), "init-model-access")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./tools/init-model-access")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
	temp := t.TempDir()
	if err := os.Mkdir(filepath.Join(temp, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join(root, "config/config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, temp, "config/config.example.toml", string(template))
	prefix := []byte("# synthetic credential and comment\n[credentials]\nexchange_api_key = \"fixture-value\"\n")
	put(t, temp, "config/config.toml", string(prefix))
	run := func() ([]byte, error) { cmd := exec.Command(binary); cmd.Dir = temp; return cmd.CombinedOutput() }
	put(t, temp, "config/.model-access.lock", "fixture-lock")
	if output, err := run(); err == nil || bytes.Contains(output, []byte("fixture-value")) {
		t.Fatal("did not honor lock or leaked data")
	}
	if data, _ := os.ReadFile(filepath.Join(temp, "config/config.toml")); !bytes.Equal(data, prefix) {
		t.Fatal("locked config changed")
	}
	if err = os.Remove(filepath.Join(temp, "config/.model-access.lock")); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err != nil || bytes.Contains(output, []byte("fixture-value")) {
		t.Fatal("initialization failed or leaked data", err)
	}
	first, err := os.ReadFile(filepath.Join(temp, "config/config.toml"))
	if err != nil || !bytes.HasPrefix(first, prefix) || bytes.Count(first, []byte("# BEGIN FACTORFORGE MODEL ACCESS")) != 1 {
		t.Fatal("private original was not preserved")
	}
	if _, err := run(); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(filepath.Join(temp, "config/config.toml"))
	if !bytes.Equal(first, second) {
		t.Fatal("second init changed config")
	}
}
