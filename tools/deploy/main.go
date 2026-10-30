// Deployment orchestration only. Credentials remain in canonical private TOML.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	cc "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/config"
	analysis "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/analysis"
	ic "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	sc "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	pc "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/configuration"
	"github.com/pelletier/go-toml/v2"
)

var services = []string{"trading-api", "replay-executor", "market-collector", "strategy-api", "strategy-internal", "strategy-scheduler", "strategy-feedback", "judgment-worker", "position-manager", "soxl-jev-api", "console-api"}
var roles = map[string]string{"ff_p1": "factorforge_sim", "ff_p2_public": "factorforge_strategy_sim_public", "ff_p2_worker": "factorforge_strategy_sim_worker", "ff_p3_read": "factorforge_instance_sim_read", "ff_p3_dynamic": "factorforge_pipeline_sim_dynamic", "ff_p3_dynamic_read": "factorforge_pipeline_sim_dynamic_read"}

type options struct {
	Project     string            `toml:"project"`
	DataRoot    string            `toml:"data_root"`
	Mode        string            `toml:"mode"`
	ImageTag    string            `toml:"image_tag"`
	ConsolePort int               `toml:"console_port"`
	RunID       string            `toml:"run_id"`
	Instrument  string            `toml:"instrument"`
	Collector   map[string]string `toml:"collector"`
	Database    struct {
		Password string            `toml:"password"`
		Roles    map[string]string `toml:"roles"`
	} `toml:"database"`
}
type deployment struct {
	root, config, profile, env string
	raw                        map[string]any
	o                          options
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func fail(s string) error { return errors.New(s) }
func secret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func object(m map[string]any, k string) map[string]any {
	v, ok := m[k].(map[string]any)
	if !ok {
		v = map[string]any{}
		m[k] = v
	}
	return v
}
func clone(m map[string]any) map[string]any {
	b, _ := toml.Marshal(m)
	var n map[string]any
	_ = toml.Unmarshal(b, &n)
	return n
}
func write(path string, b []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".deploy-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func privatePath(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		s, e := os.Lstat(p)
		if e != nil {
			if !os.IsNotExist(e) {
				return e
			}
		} else if s.Mode()&os.ModeSymlink != 0 {
			return fail("PRIVATE_SYMLINK_REJECTED")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}
func load() (*deployment, error) {
	root, e := os.Getwd()
	if e != nil {
		return nil, e
	}
	if _, e = os.Stat(filepath.Join(root, "go.mod")); e != nil {
		return nil, fail("RUN_FROM_REPOSITORY_ROOT")
	}
	d := &deployment{root: root, config: filepath.Join(root, "config/config.toml"), profile: filepath.Join(root, "runtime/deployment/profiles"), env: filepath.Join(root, "runtime/deployment/compose.env")}
	if e = privatePath(d.config); e != nil {
		return nil, e
	}
	s, e := os.Stat(d.config)
	if e != nil || s.Mode().Perm()&0077 != 0 {
		return nil, fail("PRIVATE_CONFIG_REQUIRED_0600")
	}
	b, e := os.ReadFile(d.config)
	if e != nil || toml.Unmarshal(b, &d.raw) != nil {
		return nil, fail("CONFIG_INVALID")
	}
	var r struct {
		Deployment options `toml:"deployment"`
	}
	if toml.Unmarshal(b, &r) != nil {
		return nil, fail("DEPLOYMENT_CONFIG_INVALID")
	}
	d.o = r.Deployment
	if d.o.Project == "" {
		d.o.Project = "factorforge"
	}
	if !regexp.MustCompile(`^factorforge(?:-lab-[a-z0-9-]+)?$`).MatchString(d.o.Project) {
		return nil, fail("DEPLOYMENT_PROJECT_INVALID")
	}
	if d.o.Mode != "internal" || d.o.ConsolePort < 1024 || d.o.ConsolePort > 65535 || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(d.o.ImageTag) || !filepath.IsAbs(d.o.DataRoot) || d.o.DataRoot == "/" {
		return nil, fail("DEPLOYMENT_SETTINGS_REQUIRED")
	}
	if e = privatePath(d.o.DataRoot); e != nil {
		return nil, e
	}
	return d, nil
}
func initConfig(dataRoot string) error {
	if !filepath.IsAbs(dataRoot) || dataRoot == "/" {
		return fail("ABSOLUTE_DEDICATED_DATA_ROOT_REQUIRED")
	}
	p := "config/config.toml"
	if _, e := os.Stat(p); e == nil {
		return fail("CONFIG_EXISTS_NOT_OVERWRITTEN")
	}
	b, e := os.ReadFile("config/config.example.toml")
	if e != nil {
		return e
	}
	var m map[string]any
	if toml.Unmarshal(b, &m) != nil {
		return fail("TEMPLATE_INVALID")
	}
	passwords := map[string]string{}
	for role := range roles {
		passwords[role] = secret()
	}
	version, e := os.ReadFile("VERSION")
	if e != nil {
		return e
	}
	dep := object(m, "deployment")
	for key, value := range map[string]any{"data_root": dataRoot, "mode": "internal", "image_tag": strings.TrimSpace(string(version)), "console_port": 8443, "database": map[string]any{"password": secret(), "roles": passwords}} {
		dep[key] = value
	}
	modelBlock, e := toml.Marshal(map[string]any{"model_access": m["model_access"]})
	if e != nil {
		return e
	}
	delete(m, "model_access")
	b, e = toml.Marshal(m)
	b = append(b, []byte("\n# BEGIN FACTORFORGE MODEL ACCESS\n")...)
	b = append(b, modelBlock...)
	b = append(b, []byte("# END FACTORFORGE MODEL ACCESS\n")...)
	if e != nil {
		return e
	}
	if e = os.MkdirAll("config", 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	fmt.Println("Created private deployment settings and database passwords; business/model configuration remains required")
	return nil
}
func (d *deployment) dsn(role string) string {
	password := d.o.Database.Roles[role]
	if role == "ff_admin" {
		password = d.o.Database.Password
	}
	u := url.URL{Scheme: "postgresql", Host: "postgres:5432", Path: "/factorforge", User: url.UserPassword(role, password)}
	u.RawQuery = "sslmode=disable"
	return u.String()
}
func (d *deployment) compose(args ...string) error {
	argv := append([]string{"compose", "--env-file", d.env, "-p", d.o.Project, "-f", filepath.Join(d.root, "tools/deploy/compose.yaml")}, args...)
	c := exec.Command("docker", argv...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}
func (d *deployment) command(args ...string) *exec.Cmd {
	argv := append([]string{"compose", "--env-file", d.env, "-p", d.o.Project, "-f", filepath.Join(d.root, "tools/deploy/compose.yaml")}, args...)
	return exec.Command("docker", argv...)
}
func (d *deployment) stopped() error {
	c := d.command("ps", "--services", "--status", "running")
	b, e := c.Output()
	if e != nil {
		return fail("COMPOSE_STATUS_FAILED")
	}
	for _, name := range strings.Fields(string(b)) {
		if name != "postgres" {
			return fail("STOP_APPLICATIONS_BEFORE_THIS_OPERATION")
		}
	}
	return nil
}
func (d *deployment) prepare() error {
	if len(d.o.Database.Password) < 32 {
		return fail("DATABASE_PASSWORD_REQUIRED")
	}
	for role := range roles {
		if len(d.o.Database.Roles[role]) < 32 {
			return fail("DATABASE_ROLE_PASSWORD_REQUIRED")
		}
	}
	// Do not replace profile directories underneath running containers.
	if _, e := os.Stat(d.env); e == nil {
		if e = d.stopped(); e != nil {
			return e
		}
	}
	marker := filepath.Join(d.o.DataRoot, ".deployment-project")
	if b, e := os.ReadFile(marker); e == nil && string(b) != d.o.Project {
		return fail("DATA_ROOT_PROJECT_MISMATCH")
	}
	for _, dir := range []string{"postgres", "assets", "model-runtime", "logs", "backups", "tls", "ops"} {
		if e := os.MkdirAll(filepath.Join(d.o.DataRoot, dir), 0700); e != nil {
			return e
		}
	}
	for _, name := range append(append([]string{}, services...), "postgres", "gateway", "operator") {
		if e := os.MkdirAll(filepath.Join(d.o.DataRoot, "logs", name), 0700); e != nil {
			return e
		}
	}
	if e := write(marker, []byte(d.o.Project)); e != nil {
		return e
	}
	if e := makeTLS(d.o.DataRoot); e != nil {
		return e
	}
	if e := write(filepath.Join(d.profile, "postgres/postgres-password"), []byte(d.o.Database.Password)); e != nil {
		return e
	}
	env := map[string]string{"FF_DATA_ROOT": d.o.DataRoot, "FF_PROFILE_ROOT": d.profile, "FF_PROMPTS_DIR": filepath.Join(d.root, "prompts"), "FF_UID": strconv.Itoa(os.Getuid()), "FF_GID": strconv.Itoa(os.Getgid()), "FF_IMAGE_TAG": d.o.ImageTag, "FF_CONSOLE_PORT": strconv.Itoa(d.o.ConsolePort), "FF_RUN_ID": d.o.RunID, "FF_INSTRUMENT": d.o.Instrument}
	for _, k := range []string{"candle_interval", "lookback_seconds", "poll_seconds", "multiplier", "publication_delay_ms", "trade_limit"} {
		env["FF_"+strings.ToUpper(k)] = d.o.Collector[k]
	}
	// Missing business values are conspicuous placeholders, never used after doctor fails.
	keys := []string{}
	for k, v := range env {
		if strings.ContainsAny(v, "\n\r'\x00") {
			return fail("DEPLOYMENT_PATH_INVALID")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, k := range keys {
		v := env[k]
		if v == "" {
			v = "UNCONFIGURED"
		}
		fmt.Fprintf(&out, "%s='%s'\n", k, v)
	}
	if e := write(d.env, []byte(out.String())); e != nil {
		return e
	}
	if e := d.profiles(); e != nil {
		return e
	}
	if e := d.modelFile(); e != nil {
		return e
	}
	return d.compose("config", "--quiet")
}
func (d *deployment) profiles() error {
	overrides := object(object(d.raw, "deployment"), "profiles")
	for _, name := range services {
		p := map[string]any{}
		switch {
		case name == "trading-api" || name == "replay-executor" || name == "market-collector":
			for _, k := range []string{"runtime", "services", "credentials", "trading"} {
				p[k] = clone(object(d.raw, k))
			}
			object(p, "runtime")["environment"] = "SIM"
			tr := object(p, "trading")
			tr["adapter"] = "mock"
			tr["allow_live"] = false
			tr["storage_path"] = "/tmp"
			if value, ok := object(tr, "runtime_health")["storage_path"].(string); ok && value != "" {
				object(tr, "runtime_health")["storage_path"] = "/logs"
			}
			p["credentials"] = map[string]any{"trading_api_token": object(p, "credentials")["trading_api_token"]}
			sv := object(p, "services")
			sv["database_url"] = d.dsn("ff_p1")
			delete(sv, "execution_database_url")
			sv["trading_api_url"] = "https://gateway:8443"
		case strings.HasPrefix(name, "strategy-"):
			s := clone(object(d.raw, "strategy"))
			p["strategy"] = s
			s["environment"] = "SIM"
			delete(s, "migration_database_url")
			s["public_host"] = "0.0.0.0"
			s["internal_host"] = "0.0.0.0"
			s["public_port"] = 8001
			s["internal_port"] = 8002
			s["public_api_url"] = "https://gateway:9443"
			s["internal_api_url"] = "https://gateway:9444"
			s["trading_api_url"] = "https://gateway:8443"
			if name == "strategy-api" {
				s["public_database_url"] = d.dsn("ff_p2_public")
				delete(s, "worker_database_url")
				delete(s, "workload_token")
				delete(s, "workload_identity")
			} else {
				s["worker_database_url"] = d.dsn("ff_p2_worker")
				delete(s, "public_database_url")
				delete(s, "public_token")
				delete(s, "public_identity")
			}
		case name == "soxl-jev-api":
			s := clone(object(object(d.raw, "application"), "read_api"))
			s["environment"] = "SIM"
			s["database_url"] = d.dsn("ff_p3_read")
			s["dynamic_database_url"] = d.dsn("ff_p3_dynamic_read")
			s["host"] = "0.0.0.0"
			s["port"] = 8003
			p["application"] = map[string]any{"read_api": s}
		case name == "judgment-worker" || name == "position-manager":
			p = clone(object(overrides, name))
			p["environment"] = "SIM"
			p["root"] = "/app"
			p["model_config"] = "/private/model.toml"
			a := object(p, "access")
			a["database_url"] = d.dsn("ff_p3_dynamic")
			a["framework_url"] = "https://gateway:9444"
			a["trading_read_url"] = "https://gateway:8443"
		case name == "console-api":
			continue
		}
		b, e := toml.Marshal(p)
		if e != nil {
			return e
		}
		if e = write(filepath.Join(d.profile, name, "config.toml"), b); e != nil {
			return e
		}
	}
	// Use the console's existing private wire serializer, not a second auth format.
	console := clone(object(d.raw, "console"))
	console["host"] = "0.0.0.0"
	console["port"] = 8443
	console["origin"] = fmt.Sprintf("https://localhost:%d", d.o.ConsolePort)
	console["tls_cert_file"] = "/tls/cert.pem"
	console["tls_key_file"] = "/tls/key.pem"
	console["static_dir"] = "/app/runtime/web-build"
	console["fixture_only"] = false
	b, _ := toml.Marshal(map[string]any{"console": console})
	tmp := filepath.Join(d.profile, "console-api/source.toml")
	if e := write(tmp, b); e != nil {
		return e
	}
	defer os.Remove(tmp)
	out := filepath.Join(d.profile, "console-api/profile.json")
	_ = os.Remove(out)
	if e := cc.Prepare(tmp, out); e != nil {
		fmt.Println("MISSING console users/bindings/policy (profile not generated)")
	}
	version, e := os.ReadFile(filepath.Join(d.root, "VERSION"))
	if e != nil {
		return e
	}
	schema := map[string]any{"environment": "SIM", "instance_id": object(d.raw, "strategy")["instance_id"], "dsn": d.dsn("ff_admin"), "roles": d.o.Database.Roles, "version": strings.TrimSpace(string(version))}
	b, _ = json.Marshal(schema)
	return write(filepath.Join(d.profile, "operator/schema.json"), b)
}
func (d *deployment) modelFile() error {
	access := clone(object(d.raw, "model_access"))
	if len(access) == 0 {
		return fail("MODEL_CONFIGURATION_INCOMPLETE")
	}
	access["state_path"] = "/private/runtime/model-state.json"
	b, e := toml.Marshal(map[string]any{"model_access": access})
	if e != nil {
		return e
	}
	return write(filepath.Join(d.profile, "judgment-worker/model.toml"), b)
}
func (d *deployment) doctor() error {
	missing := []string{}
	confirmed := object(object(d.raw, "deployment"), "confirmed")
	for _, field := range []string{"prompt_reviewed", "risk_policy_reviewed", "model_budget_reviewed"} {
		if approved, _ := confirmed[field].(bool); !approved {
			missing = append(missing, "deployment.confirmed."+field)
		}
	}
	if d.o.RunID == "" {
		missing = append(missing, "deployment.run_id")
	}
	if d.o.Instrument == "" {
		missing = append(missing, "deployment.instrument")
	}
	for _, k := range []string{"candle_interval", "lookback_seconds", "poll_seconds", "multiplier", "publication_delay_ms", "trade_limit"} {
		if d.o.Collector[k] == "" {
			missing = append(missing, "deployment.collector."+k)
		}
	}
	for _, name := range services {
		p := filepath.Join(d.profile, name, "config.toml")
		var e error
		switch {
		case name == "trading-api" || name == "replay-executor" || name == "market-collector":
			var c pc.Config
			c, e = pc.Load(p)
			if e == nil {
				e = c.ValidateAPI()
			}
		case strings.HasPrefix(name, "strategy-"):
			var c sc.Config
			c, e = sc.Load(p)
			if e == nil {
				_, e = c.Identity(name != "strategy-api")
			}
		case name == "judgment-worker" || name == "position-manager":
			_, e = ic.LoadDynamic(p)
		case name == "soxl-jev-api":
			_, e = ic.LoadReadAPI(p)
		case name == "console-api":
			_, e = cc.Load(filepath.Join(d.profile, name, "profile.json"))
		}
		if e != nil {
			missing = append(missing, "profile."+name)
		}
	}
	if _, e := ic.LoadModelAccess(filepath.Join(d.profile, "judgment-worker/model.toml")); e != nil {
		missing = append(missing, "profile.judgment-worker/model.toml")
	}
	judgment, profileErr := ic.LoadDynamic(filepath.Join(d.profile, "judgment-worker/config.toml"))
	if profileErr == nil {
		prefix := "/private/prompts/"
		if !strings.HasPrefix(judgment.PromptPath, prefix) || strings.Contains(strings.TrimPrefix(judgment.PromptPath, prefix), "..") {
			missing = append(missing, "judgment prompt_path under /private/prompts/")
		} else {
			path := filepath.Join(d.root, "prompts", strings.TrimPrefix(judgment.PromptPath, prefix))
			if e := privatePath(path); e != nil {
				missing = append(missing, "private judgment prompt path")
			} else if _, e := analysis.LoadJudgmentTemplate(path, judgment.PromptVersion, judgment.MaxBytes); e != nil {
				missing = append(missing, "private judgment prompt contents/version")
			}
		}
	}
	var disk syscall.Statfs_t
	if syscall.Statfs(d.o.DataRoot, &disk) != nil || disk.Bavail*uint64(disk.Bsize) < 1<<30 {
		missing = append(missing, "data-root >= 1GiB free (startup guard, not annual capacity estimate)")
	}
	for _, m := range missing {
		fmt.Println("MISSING", m)
	}
	if len(missing) > 0 {
		return fail("DEPLOYMENT_NOT_READY")
	}
	fmt.Println("Configuration preflight passed; business initialization/reconciliation is still required")
	return nil
}
func run() error {
	if len(os.Args) < 2 {
		return fail("Usage: go run ./tools/deploy init|prepare|doctor|build|infra-up|schema|up|stop|status|backup|restore")
	}
	action := os.Args[1]
	f := flag.NewFlagSet(action, flag.ContinueOnError)
	data := f.String("data-root", "", "dedicated absolute data root")
	source := f.String("source", "", "completed backup directory")
	dest := f.String("destination", "", "new isolated restore directory")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if action == "init" {
		return initConfig(*data)
	}
	d, e := load()
	if e != nil {
		return e
	}
	// Serialize operators without interfering with the model's own lock.
	if e = os.MkdirAll(filepath.Join(d.root, "runtime/deployment"), 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(d.root, "runtime/deployment/.operator.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return fail("DEPLOYMENT_OPERATION_BUSY")
	}
	switch action {
	case "prepare":
		return d.prepare()
	case "doctor":
		return d.doctor()
	case "build":
		for _, target := range []string{"postgres", "core"} {
			c := exec.Command("docker", "build", "-f", "tools/deploy/Dockerfile", "--target", target, "--build-arg", "FF_UID="+strconv.Itoa(os.Getuid()), "--build-arg", "FF_GID="+strconv.Itoa(os.Getgid()), "-t", "factorforge-"+target+":"+d.o.ImageTag, ".")
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			if e = c.Run(); e != nil {
				return fail("IMAGE_BUILD_FAILED")
			}
		}
		return nil
	case "infra-up":
		return d.compose("up", "-d", "--wait", "postgres")
	case "schema":
		if e = d.stopped(); e != nil {
			return e
		}
		return d.compose("--profile", "ops", "run", "--rm", "operator", "deploy-schema", "--config", "/profile/schema.json")
	case "up":
		if e = d.doctor(); e != nil {
			return e
		}
		return d.compose("--profile", "app", "up", "-d", "--wait")
	case "stop":
		return d.compose("--profile", "app", "stop")
	case "status":
		return d.compose("--profile", "app", "ps", "-a")
	case "backup":
		return d.backup()
	case "restore":
		return d.restore(*source, *dest)
	default:
		return fail("UNKNOWN_DEPLOYMENT_ACTION")
	}
}
