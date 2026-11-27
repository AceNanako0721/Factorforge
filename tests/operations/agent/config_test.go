package agent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

// A fictional profile loads once its secret files exist and are private;
// unknown fields and readable secret files are refused (S6-009).
func TestExampleProfile(t *testing.T) {
	dir := t.TempDir()
	text := strings.ReplaceAll(exampleProfile, "/srv/ops-agent", dir)
	if err := os.MkdirAll(filepath.Join(dir, "tokens"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"database-url", "tokens/demo-1-trading", "tokens/demo-1-strategy", "tokens/demo-1-application"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fictional\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "profile.toml")
	os.WriteFile(path, []byte(text), 0o600)
	l, err := config.Load(path)
	if err != nil || len(l.Instances) != 1 || l.Bindings["demo-1"].Trading.Token != "fictional" || len(l.Profile.Horizons) != 3 {
		t.Fatal("example", err)
	}
	os.WriteFile(path, []byte(text+"\nsurprise = 1\n"), 0o600)
	if _, err := config.Load(path); d.Code(err) != "PROFILE_INVALID" {
		t.Fatal("unknown field", err)
	}
	os.WriteFile(path, []byte(text), 0o600)
	os.Chmod(filepath.Join(dir, "tokens/demo-1-strategy"), 0o644)
	if _, err := config.Load(path); d.Code(err) != "PRIVATE_FILE_REQUIRED" {
		t.Fatal("readable token", err)
	}
}

// exampleProfile is fictional; real profiles stay outside the repository.
const exampleProfile = `# Operations agent (P6) local profile. Copy outside the repository, keep it
# private (0600) and replace every value; all values here are fictional.
schema_version = 1
database_url_file = "/srv/ops-agent/database-url"   # one line: the PostgreSQL connection string
schema = "ops_agent"
interval_seconds = 60
timeout_seconds = 30
max_response_bytes = 16777216
scorecard_horizons = [4, 24, 48]

[host]
systemctl = "/usr/bin/systemctl"
journalctl = "/usr/bin/journalctl"
repository = "/srv/factorforge"
thermal = "/sys/class/thermal/thermal_zone*/temp"
revision_skip = '/tlsproxy$'

[[instances]]
instance_id = "demo-1"
object_id = "demo-object-1"
environment = "SIM"
account_id = "demo-account-1"
run_id = "demo-run-1"
venue = "DEMO"
instrument_id = "DEMOUSDT"
units = ["demo-1-trading-api.service", "demo-1-strategy-public.service", "demo-1-p3-api.service"]
[instances.trading]
url = "https://127.0.0.1:19001"
token_file = "/srv/ops-agent/tokens/demo-1-trading"
[instances.strategy]
url = "https://127.0.0.1:19002"
token_file = "/srv/ops-agent/tokens/demo-1-strategy"
[instances.application]
url = "https://127.0.0.1:19003"
token_file = "/srv/ops-agent/tokens/demo-1-application"
`
