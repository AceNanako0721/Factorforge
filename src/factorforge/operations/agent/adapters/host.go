package adapters

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// HostProbe runs only fixed, read-only commands: `systemctl --user show`
// and `journalctl --user` with argument vectors (no shell), reads build info
// from the unit binaries, the main revision from the repository's refs and
// the thermal zones (S6-004).
type HostProbe struct {
	Systemctl  string
	Journalctl string
	Repository string
	Thermal    string // glob of thermal zone temperature files
	Skip       *regexp.Regexp
	Timeout    time.Duration
}

var unitName = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,200}\.(service|timer)$`)
var execPath = regexp.MustCompile(`path=([^ ;]+)`)
var alertField = regexp.MustCompile(`"alert"\s*:\s*"([A-Z0-9_]{1,80})"`)

func (h HostProbe) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"XDG_RUNTIME_DIR=" + os.Getenv("XDG_RUNTIME_DIR"), "LANG=C"}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, d.Fail("HOST_COMMAND_FAILED")
	}
	return out.Bytes(), nil
}

// Units reports each registered unit and counts the structured alerts it
// logged since the given time.
func (h HostProbe) Units(ctx context.Context, units []string, since time.Time) []d.Unit {
	out := make([]d.Unit, 0, len(units))
	for _, name := range units {
		u := d.Unit{Name: name}
		if !unitName.MatchString(name) {
			u.ReadFailure = "UNIT_NAME_INVALID"
			out = append(out, u)
			continue
		}
		raw, err := h.run(ctx, h.Systemctl, "--user", "show", name, "--timestamp=unix", "--property=ActiveState,SubState,UnitFileState,Type,StateChangeTimestamp,InactiveExitTimestamp,ExecStart")
		if err != nil {
			u.ReadFailure = d.Code(err)
			out = append(out, u)
			continue
		}
		props := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				props[k] = v
			}
		}
		u.Active, u.Sub, u.FileState, u.Type = props["ActiveState"], props["SubState"], props["UnitFileState"], props["Type"]
		u.EnteredAt, u.StartedAt = unixStamp(props["StateChangeTimestamp"]), unixStamp(props["InactiveExitTimestamp"])
		if m := execPath.FindStringSubmatch(props["ExecStart"]); m != nil && (h.Skip == nil || !h.Skip.MatchString(m[1])) {
			if info, err := buildinfo.ReadFile(m[1]); err == nil {
				for _, s := range info.Settings {
					if s.Key == "vcs.revision" {
						u.Revision = s.Value
					}
				}
			}
		}
		if !since.IsZero() && strings.HasSuffix(name, ".service") {
			if raw, err := h.run(ctx, h.Journalctl, "--user", "--unit", name, "--since", "@"+strconv.FormatInt(since.Unix(), 10), "--output", "cat", "--no-pager", "--quiet"); err == nil {
				sc := bufio.NewScanner(bytes.NewReader(raw))
				sc.Buffer(make([]byte, 64*1024), 1<<20)
				for sc.Scan() {
					if m := alertField.FindSubmatch(sc.Bytes()); m != nil {
						if u.Alerts == nil {
							u.Alerts = map[string]int{}
						}
						u.Alerts[string(m[1])]++
					}
				}
			}
		}
		out = append(out, u)
	}
	return out
}

// unixStamp parses systemd's "@<seconds>" (--timestamp=unix); empty or
// unset stamps give the zero time.
func unixStamp(v string) time.Time {
	if n, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64); err == nil && n > 0 {
		return time.Unix(n, 0).UTC()
	}
	return time.Time{}
}

// MainRevision reads origin/main from the repository's refs without git.
func (h HostProbe) MainRevision(context.Context) string {
	if h.Repository == "" {
		return ""
	}
	dir := filepath.Join(h.Repository, ".git")
	if b, err := os.ReadFile(filepath.Join(dir, "refs", "remotes", "origin", "main")); err == nil {
		return strings.TrimSpace(string(b))
	}
	b, err := os.ReadFile(filepath.Join(dir, "packed-refs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rev, ref, ok := strings.Cut(line, " "); ok && ref == "refs/remotes/origin/main" {
			return rev
		}
	}
	return ""
}

// Host reads the hottest thermal zone and the 1-minute load.
func (h HostProbe) Host(context.Context) d.HostSnapshot {
	s := d.HostSnapshot{Unavailable: map[string]string{}}
	if h.Thermal != "" {
		paths, _ := filepath.Glob(h.Thermal)
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil {
				if v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil && v/1000 > s.TemperatureC {
					s.TemperatureC = v / 1000
				}
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	return s
}
