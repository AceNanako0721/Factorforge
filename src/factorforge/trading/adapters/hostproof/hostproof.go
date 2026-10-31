// Package hostproof identifies the running process on this Linux machine and
// judges whether a process recorded earlier has stopped. It reads only /proc,
// /etc/machine-id and nothing else, and it answers "stopped" only when the
// evidence is conclusive.
package hostproof

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const version = "linux1"

// Prober reads the machine under Root ("/" when empty). PID is the process whose
// identity Self reports (the current process when zero).
type Prober struct {
	Root string
	PID  int
}

type identity struct {
	host, boot, pidns string
	pid               int
	start             string
}

func (p Prober) path(parts ...string) string {
	root := p.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func (p Prober) read(parts ...string) (string, error) {
	data, err := os.ReadFile(p.path(parts...))
	return strings.TrimSpace(string(data)), err
}

// machine returns the host and boot identifiers. The host is a salted hash of
// the machine id so the raw id never reaches the database.
func (p Prober) machine() (host, boot string, err error) {
	id, err := p.read("etc", "machine-id")
	if err != nil || id == "" {
		return "", "", errors.New("HOST_IDENTITY_UNAVAILABLE")
	}
	boot, err = p.read("proc", "sys", "kernel", "random", "boot_id")
	if err != nil || boot == "" {
		return "", "", errors.New("BOOT_IDENTITY_UNAVAILABLE")
	}
	sum := sha256.Sum256([]byte("factorforge-host:" + id))
	return hex.EncodeToString(sum[:])[:16], boot, nil
}

func (p Prober) namespace() (string, error) {
	link, err := os.Readlink(p.path("proc", "self", "ns", "pid"))
	if err != nil || link == "" {
		return "", errors.New("PID_NAMESPACE_UNAVAILABLE")
	}
	return link, nil
}

// processState returns the start time (clock ticks after boot) and state letter
// of a process. The command name may contain spaces and parentheses, so fields
// are counted after the last ')'.
func (p Prober) processState(pid int) (start, state string, err error) {
	stat, err := p.read("proc", strconv.Itoa(pid), "stat")
	if err != nil {
		return "", "", err
	}
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", "", errors.New("PROCESS_STAT_UNREADABLE")
	}
	fields := strings.Fields(stat[end+1:])
	// fields[0] is the state (stat field 3), fields[19] the start time (field 22).
	if len(fields) < 20 {
		return "", "", errors.New("PROCESS_STAT_UNREADABLE")
	}
	return fields[19], fields[0], nil
}

// Self returns the identity token of the process: version, host, boot,
// PID namespace, pid and start time, joined by ':' (none contains one).
func (p Prober) Self() (string, error) {
	host, boot, err := p.machine()
	if err != nil {
		return "", err
	}
	ns, err := p.namespace()
	if err != nil {
		return "", err
	}
	pid := p.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	start, _, err := p.processState(pid)
	if err != nil {
		return "", errors.New("PROCESS_IDENTITY_UNAVAILABLE")
	}
	return strings.Join([]string{version, host, boot, strings.NewReplacer(":", "_", " ", "_").Replace(ns), strconv.Itoa(pid), start}, ":"), nil
}

func parse(token string) (identity, bool) {
	parts := strings.Split(token, ":")
	if len(parts) != 6 || parts[0] != version {
		return identity{}, false
	}
	pid, err := strconv.Atoi(parts[4])
	if err != nil || pid <= 0 || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[5] == "" {
		return identity{}, false
	}
	return identity{host: parts[1], boot: parts[2], pidns: parts[3], pid: pid, start: parts[5]}, true
}

// Stopped reports whether the process described by a recorded token no longer
// exists on this machine, with the evidence. It is conclusive only when the
// token was written on this host and either the machine has rebooted since or,
// in the same boot and PID namespace, that process has exited or its pid now
// belongs to another process. A different host or namespace, an unreadable or
// unknown token and a process that still exists are all "not stopped".
func (p Prober) Stopped(recorded string) (string, bool) {
	old, ok := parse(recorded)
	if !ok {
		return "", false
	}
	host, boot, err := p.machine()
	if err != nil || host != old.host {
		return "", false
	}
	if boot != old.boot {
		return fmt.Sprintf("HOST_REBOOTED host=%s boot=%s->%s previous_pid=%d", host, short(old.boot), short(boot), old.pid), true
	}
	ns, err := p.namespace()
	if err != nil || strings.NewReplacer(":", "_", " ", "_").Replace(ns) != old.pidns {
		return "", false
	}
	start, state, err := p.processState(old.pid)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("PROCESS_EXITED host=%s boot=%s pid=%d start=%s", host, short(boot), old.pid, old.start), true
	case err != nil:
		return "", false
	case state == "Z" || state == "X":
		return fmt.Sprintf("PROCESS_EXITED host=%s boot=%s pid=%d start=%s state=%s", host, short(boot), old.pid, old.start, state), true
	case start != old.start:
		return fmt.Sprintf("PROCESS_REPLACED host=%s boot=%s pid=%d recorded_start=%s current_start=%s", host, short(boot), old.pid, old.start, start), true
	}
	return "", false
}

func short(boot string) string {
	if len(boot) > 8 {
		return boot[:8]
	}
	return boot
}
