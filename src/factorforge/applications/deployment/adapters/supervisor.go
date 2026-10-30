package adapters

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Health struct {
	At        time.Time `json:"at"`
	PID       int       `json:"pid"`
	Logging   bool      `json:"logging"`
	FreeBytes uint64    `json:"free_bytes"`
}
type mirror struct {
	archive *Archive
	console io.Writer
	failed  *atomic.Bool
}

func (m mirror) Write(p []byte) (int, error) {
	if _, e := m.archive.Write(p); e != nil {
		if !m.failed.Swap(true) {
			io.WriteString(os.Stderr, "PERSISTENT_LOG_FAILED\n")
		}
	}
	// Log failures must not terminate an existing risk-protection process.
	_, _ = m.console.Write(p)
	return len(p), nil
}
func writeHealth(path, logs string, pid int, okay bool) {
	var stat syscall.Statfs_t
	err := syscall.Statfs(logs, &stat)
	h := Health{At: time.Now().UTC(), PID: pid, Logging: okay && err == nil, FreeBytes: stat.Bavail * uint64(stat.Bsize)}
	if h.FreeBytes < 256<<20 {
		h.Logging = false
	}
	b, _ := json.Marshal(h)
	if os.WriteFile(path+".tmp", b, 0600) == nil {
		_ = os.Rename(path+".tmp", path)
	}
}
func Supervise(logs, health string, argv []string) int {
	if len(argv) == 0 {
		return 64
	}
	archive, e := OpenArchive(logs, 16<<20, time.Now)
	if e != nil {
		io.WriteString(os.Stderr, "PERSISTENT_LOG_OPEN_FAILED\n")
		return 73
	}
	defer archive.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	var failed atomic.Bool
	cmd.Stdout = mirror{archive, os.Stdout, &failed}
	cmd.Stderr = mirror{archive, os.Stderr, &failed}
	cmd.Stdin = os.Stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	if e = cmd.Start(); e != nil {
		io.WriteString(os.Stderr, "CHILD_START_FAILED\n")
		return 74
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case sig := <-signals:
				_ = syscall.Kill(-cmd.Process.Pid, sig.(syscall.Signal))
			case <-tick.C:
				writeHealth(health, logs, cmd.Process.Pid, !failed.Load())
			case <-done:
				return
			}
		}
	}()
	writeHealth(health, logs, cmd.Process.Pid, true)
	e = cmd.Wait()
	close(done)
	wg.Wait()
	_ = os.Remove(health)
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			if exit.ExitCode() >= 0 {
				return exit.ExitCode()
			}
			if s, ok := exit.Sys().(syscall.WaitStatus); ok {
				return 128 + int(s.Signal())
			}
		}
		return 74
	}
	if e = archive.Close(); e != nil {
		io.WriteString(os.Stderr, "PERSISTENT_LOG_CLOSE_FAILED\n")
		return 73
	}
	return 0
}
func CheckHealth(path string) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return StableError("SUPERVISOR_UNAVAILABLE")
	}
	var h Health
	if json.Unmarshal(b, &h) != nil || !h.Logging || h.PID <= 0 || time.Since(h.At) > 20*time.Second || time.Until(h.At) > time.Second {
		return StableError("SUPERVISOR_UNHEALTHY")
	}
	if syscall.Kill(h.PID, 0) != nil {
		return StableError("CHILD_UNAVAILABLE")
	}
	return nil
}
