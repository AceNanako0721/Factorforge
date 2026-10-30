// Opt-in Docker lifecycle experiment. Uses only a fresh project, scratch image,
// synthetic output, and a newly created runtime directory. No production imports.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type lab struct {
	root, dir, project, image string
	checks                    []string
	baseline                  map[string]string
}

func command(timeout time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	data, err := cmd.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("%s failed: %w: %s", name, err, data)
	}
	return string(data), nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func (l *lab) save(name, value string) {
	must(os.WriteFile(filepath.Join(l.dir, name), []byte(value), 0600))
}
func (l *lab) compose(args ...string) string {
	all := append([]string{"compose", "-p", l.project, "-f", filepath.Join(l.dir, "compose.yaml")}, args...)
	output, err := command(40*time.Second, l.root, nil, "docker", all...)
	must(err)
	return output
}
func (l *lab) ok(message string, condition bool) {
	if !condition {
		panic("ASSERTION_FAILED: " + message)
	}
	l.checks = append(l.checks, message)
	fmt.Println("PASS", message)
}
func (l *lab) config(tag, folder string, readOnly bool) {
	path := filepath.Join(l.dir, folder)
	must(os.MkdirAll(path, 0700))
	l.save("compose.yaml", fmt.Sprintf(`services:
  probe:
    image: %s
    pull_policy: never
    user: "%d:%d"
    environment:
      FF_LAB_TAG: %s
    network_mode: none
    read_only: true
    cap_drop: [ALL]
    security_opt: [no-new-privileges:true]
    pids_limit: 32
    mem_limit: 64m
    cpus: 0.25
    stop_grace_period: 5s
    restart: "no"
    logging:
      driver: local
      options:
        max-size: "16k"
        max-file: "2"
    volumes:
      - type: bind
        source: %q
        target: /logs
        read_only: %t
`, l.image, os.Getuid(), os.Getgid(), tag, path, readOnly))
	l.save("compose-"+tag+".yaml", mustRead(filepath.Join(l.dir, "compose.yaml")))
}
func mustRead(path string) string { b, err := os.ReadFile(path); must(err); return string(b) }
func (l *lab) waitFor(message string, fn func() bool) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	panic("TIMEOUT: " + message)
}
func (l *lab) has(folder, marker string) bool {
	b, err := os.ReadFile(filepath.Join(l.dir, folder, "service.log"))
	return err == nil && strings.Contains(string(b), marker)
}
func (l *lab) id() string { return strings.TrimSpace(l.compose("ps", "-a", "-q", "probe")) }
func docker(args ...string) string {
	out, err := command(20*time.Second, "", nil, "docker", args...)
	must(err)
	return strings.TrimSpace(out)
}
func (l *lab) exited(id string, code int) {
	want := fmt.Sprintf("exited %d", code)
	l.waitFor(want, func() bool { return docker("inspect", "--format", "{{.State.Status}} {{.State.ExitCode}}", id) == want })
}
func snapshot() map[string]string {
	result := map[string]string{}
	for _, id := range strings.Fields(docker("ps", "-q", "--no-trunc")) {
		result[id] = docker("inspect", "--format", "{{.State.StartedAt}}", id)
	}
	return result
}

func (l *lab) run() {
	l.baseline = snapshot()
	env := append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	build, err := command(2*time.Minute, l.root, env, "go", "build", "-trimpath", "-o", filepath.Join(l.dir, "probe"), "./tests/engineering/experiments/dockerlogs/probe")
	l.save("go-build.log", build)
	must(err)
	l.save("Dockerfile", "FROM scratch\nCOPY probe /probe\nENTRYPOINT [\"/probe\"]\n")
	l.save(".dockerignore", "*\n!probe\n!Dockerfile\n")
	build, err = command(2*time.Minute, l.root, nil, "docker", "build", "--network=none", "--label", "factorforge.experiment="+l.project, "-t", l.image, l.dir)
	l.save("docker-build.log", build)
	must(err)
	l.config("A", "retained", false)
	l.compose("config", "--quiet")
	l.compose("up", "-d", "--no-build", "--pull", "never")
	idA := l.id()
	l.waitFor("first stdout and stderr", func() bool { return l.has("retained", "BOOT A") && l.has("retained", "STDERR A") })
	console := docker("logs", idA)
	l.save("docker-before-restart.log", console)
	l.ok("stdout and stderr reach both persistent file and Docker logs", strings.Contains(console, "BOOT A") && strings.Contains(console, "STDERR A"))
	l.compose("restart", "probe")
	l.waitFor("second boot", func() bool {
		return strings.Count(mustRead(filepath.Join(l.dir, "retained/service.log")), "BOOT A") >= 2
	})
	l.ok("restart preserves container identity and earlier logs", l.id() == idA && strings.Count(docker("logs", idA), "BOOT A") >= 2)
	l.ok("SIGTERM reaches child and final line is flushed", l.has("retained", "TERM A"))
	l.compose("down", "--timeout", "5")
	_, err = command(10*time.Second, l.root, nil, "docker", "logs", idA)
	l.ok("removed container Docker logs are unavailable but persistent copy remains", err != nil && l.has("retained", "BOOT A"))
	l.config("B", "retained", false)
	l.compose("up", "-d", "--no-build", "--pull", "never")
	l.waitFor("replacement boot", func() bool { return l.has("retained", "BOOT B") })
	l.ok("new container retains old stdout, stderr and shutdown records", l.id() != idA && l.has("retained", "BOOT A") && l.has("retained", "STDERR A") && l.has("retained", "TERM A"))
	// A second writer must not silently corrupt the same service's rotation set.
	out, err := command(15*time.Second, l.root, nil, "docker", "exec", l.id(), "/probe")
	l.save("concurrent-writer.log", out)
	l.ok("concurrent writer is refused without disturbing original child", err != nil && strings.Contains(out, "LAB_LOG_OPEN_FAILED") && docker("inspect", "--format", "{{.State.Running}}", l.id()) == "true")
	l.compose("stop", "probe")
	l.exited(l.id(), 0)
	l.ok("graceful stop exits zero with final child log retained", l.has("retained", "TERM B"))
	l.compose("down", "--timeout", "5")
	l.config("ROTATE", "rotated", false)
	l.compose("up", "-d", "--no-build", "--pull", "never")
	l.waitFor("rotation completion", func() bool { return l.has("rotated", "FLOOD_END ROTATE") })
	l.compose("stop", "probe")
	l.exited(l.id(), 0)
	files, err := filepath.Glob(filepath.Join(l.dir, "rotated", "service.log*"))
	must(err)
	var total int64
	all := ""
	permissions := true
	for _, p := range files {
		s, e := os.Stat(p)
		must(e)
		total += s.Size()
		permissions = permissions && s.Mode().Perm() == 0600 && s.Size() <= 16384
		all += mustRead(p)
	}
	l.ok("rotation bounded to four files and 64 KiB with mode 0600", len(files) == 4 && total <= 65536 && permissions)
	l.ok("rotation retains newest and shutdown records, evicts oldest as specified", strings.Contains(all, "FLOOD_END ROTATE") && strings.Contains(all, "TERM ROTATE") && !strings.Contains(all, "BOOT ROTATE"))
	l.compose("down", "--timeout", "5")
	l.config("EXIT", "exit-code", false)
	l.compose("up", "-d", "--no-build", "--pull", "never")
	l.exited(l.id(), 37)
	l.ok("child exit code 37 is preserved, output drained", l.has("exit-code", "EXIT_MARKER EXIT"))
	l.compose("down", "--timeout", "5")
	l.config("READONLY", "read-only", true)
	l.compose("up", "-d", "--no-build", "--pull", "never")
	l.exited(l.id(), 73)
	l.save("read-only-error.log", docker("logs", l.id()))
	l.ok("unwritable log directory refuses startup before launching child", !l.has("read-only", "BOOT READONLY") && strings.Contains(docker("logs", l.id()), "LAB_LOG_OPEN_FAILED"))
	l.compose("down", "--timeout", "5")
	after := snapshot()
	unchanged := len(after) == len(l.baseline)
	for id, start := range l.baseline {
		unchanged = unchanged && after[id] == start
	}
	l.ok("pre-existing running containers and start times unchanged", unchanged)
	report := map[string]any{"project": l.project, "image": l.image, "checks": l.checks, "existing_containers": len(l.baseline), "rotation_bytes": total, "synthetic_only": true, "production_integrated": false, "finished_at": time.Now().UTC().Format(time.RFC3339)}
	b, err := json.MarshalIndent(report, "", "  ")
	must(err)
	l.save("result.json", string(b)+"\n")
}

func execute() (err error) {
	enabled := flag.Bool("run", false, "authorize isolated synthetic Docker lifecycle experiment")
	flag.Parse()
	if !*enabled {
		return fmt.Errorf("requires explicit --run; creates only its own scratch image and test containers")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("run from repository root")
	}
	stamp := time.Now().UTC().Format("20060102t150405") + fmt.Sprintf("-%d", os.Getpid())
	l := &lab{root: root, dir: filepath.Join(root, "runtime", "docker-log-lab-"+stamp), project: "ff-log-lab-" + stamp, image: "factorforge-log-lab:" + stamp}
	if err = os.Mkdir(l.dir, 0700); err != nil {
		return err
	}
	fmt.Println("Evidence:", l.dir)
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
		// Project and image names were freshly generated by this invocation only.
		if _, e := os.Stat(filepath.Join(l.dir, "compose.yaml")); e == nil {
			out, e := command(30*time.Second, root, nil, "docker", "compose", "-p", l.project, "-f", filepath.Join(l.dir, "compose.yaml"), "down", "--timeout", "5")
			_ = os.WriteFile(filepath.Join(l.dir, "cleanup.log"), []byte(out), 0600)
			if e != nil {
				err = fmt.Errorf("cleanup: %v; task: %v", e, err)
			}
		}
		if _, e := command(10*time.Second, root, nil, "docker", "image", "inspect", l.image); e == nil {
			if _, e = command(20*time.Second, root, nil, "docker", "image", "rm", l.image); e != nil {
				err = fmt.Errorf("image cleanup: %v; task: %v", e, err)
			}
		}
	}()
	l.run()
	return nil
}

func main() {
	if err := execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
