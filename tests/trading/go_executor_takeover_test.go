package trading_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/hostproof"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/memory"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
)

// fakeMachine writes a minimal /proc and /etc tree. Each process is a pid with
// a stat line whose command name contains spaces and parentheses.
type fakeMachine struct {
	t    *testing.T
	root string
}

func newFakeMachine(t *testing.T, machineID, bootID, pidNamespace string) *fakeMachine {
	t.Helper()
	m := &fakeMachine{t: t, root: t.TempDir()}
	m.write("etc/machine-id", machineID+"\n")
	m.write("proc/sys/kernel/random/boot_id", bootID+"\n")
	m.namespace(pidNamespace)
	return m
}
func (m *fakeMachine) write(name, text string) {
	m.t.Helper()
	path := filepath.Join(m.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		m.t.Fatal(err)
	}
}
func (m *fakeMachine) namespace(link string) {
	m.t.Helper()
	path := filepath.Join(m.root, "proc/self/ns/pid")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(link, path); err != nil {
		m.t.Skip("symlinks unavailable:", err)
	}
}
func (m *fakeMachine) process(pid int, state, start string) {
	m.t.Helper()
	fields := []string{state, "1", "1", "1", "0", "-1", "4194560", "0", "0", "0", "0", "0", "0", "0", "0", "20", "0", "1", "0", start, "1000", "1000"}
	m.write("proc/"+strconv.Itoa(pid)+"/stat", strconv.Itoa(pid)+" (exec (sim) x) "+strings.Join(fields, " ")+"\n")
}
func (m *fakeMachine) exit(pid int) {
	m.t.Helper()
	if err := os.RemoveAll(filepath.Join(m.root, "proc", strconv.Itoa(pid))); err != nil {
		m.t.Fatal(err)
	}
}

func TestHostProofJudgesRecordedProcess(t *testing.T) {
	const ns = "pid:[4026531836]"
	m := newFakeMachine(t, "machine-a", "boot-0001-aaaa", ns)
	m.process(4242, "S", "9001")
	recorded, err := hostproof.Prober{Root: m.root, PID: 4242}.Self()
	if err != nil || strings.Count(recorded, ":") != 5 || strings.Contains(recorded, "machine-a") {
		t.Fatalf("identity token %q %v", recorded, err)
	}
	judge := func(p hostproof.Prober, token string) (string, bool) { return p.Stopped(token) }
	now := hostproof.Prober{Root: m.root}

	if ev, stopped := judge(now, recorded); stopped {
		t.Fatalf("a running process was judged stopped: %s", ev)
	}
	m.process(4242, "S", "9002")
	if ev, stopped := judge(now, recorded); !stopped || !strings.HasPrefix(ev, "PROCESS_REPLACED") {
		t.Fatalf("reused pid not recognised: %q %v", ev, stopped)
	}
	m.process(4242, "Z", "9001")
	if ev, stopped := judge(now, recorded); !stopped || !strings.HasPrefix(ev, "PROCESS_EXITED") || !strings.Contains(ev, "state=Z") {
		t.Fatalf("zombie not recognised as exited: %q %v", ev, stopped)
	}
	m.exit(4242)
	if ev, stopped := judge(now, recorded); !stopped || !strings.HasPrefix(ev, "PROCESS_EXITED") || !strings.Contains(ev, "pid=4242") {
		t.Fatalf("missing process not recognised: %q %v", ev, stopped)
	}

	// A reboot proves the process gone even if its pid exists again.
	m.process(4242, "S", "9001")
	m.write("proc/sys/kernel/random/boot_id", "boot-0002-bbbb\n")
	if ev, stopped := judge(now, recorded); !stopped || !strings.HasPrefix(ev, "HOST_REBOOTED") || !strings.Contains(ev, "boot-000") {
		t.Fatalf("reboot not recognised: %q %v", ev, stopped)
	}
	m.write("proc/sys/kernel/random/boot_id", "boot-0001-aaaa\n")

	// Anything not conclusive means not stopped.
	m.exit(4242)
	m.write("etc/machine-id", "machine-b\n")
	m.write("proc/sys/kernel/random/boot_id", "boot-0002-bbbb\n")
	if ev, stopped := judge(now, recorded); stopped {
		t.Fatalf("a record from another host was judged stopped: %s", ev)
	}
	m.write("etc/machine-id", "machine-a\n")
	m.write("proc/sys/kernel/random/boot_id", "boot-0001-aaaa\n")
	m.namespace("pid:[4026532000]")
	if ev, stopped := judge(now, recorded); stopped {
		t.Fatalf("a process in another PID namespace was judged stopped: %s", ev)
	}
	m.namespace(ns)
	for _, token := range []string{"", "linux1", "linux2:h:b:n:1:1", strings.Replace(recorded, "linux1", "linux0", 1), recorded + ":x", strings.Replace(recorded, ":4242:", ":0:", 1), strings.Replace(recorded, ":4242:", ":x:", 1)} {
		if ev, stopped := judge(now, token); stopped {
			t.Fatalf("token %q judged stopped: %s", token, ev)
		}
	}
	if err = os.Remove(filepath.Join(m.root, "etc/machine-id")); err != nil {
		t.Fatal(err)
	}
	if ev, stopped := judge(now, recorded); stopped {
		t.Fatalf("an unreadable host identity judged stopped: %s", ev)
	}
	if _, err = (hostproof.Prober{Root: m.root, PID: 4242}).Self(); err == nil {
		t.Fatal("identity produced without a host id")
	}
}

func TestHostProofWithRealProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no /proc")
	}
	self, err := hostproof.Prober{}.Self()
	if err != nil {
		t.Skip("process identity unavailable:", err)
	}
	if ev, stopped := (hostproof.Prober{}).Stopped(self); stopped {
		t.Fatalf("the running test process was judged stopped: %s", ev)
	}
	child := exec.Command("sleep", "60")
	if err = child.Start(); err != nil {
		t.Skip("cannot start a child:", err)
	}
	defer child.Process.Kill()
	token, err := hostproof.Prober{PID: child.Process.Pid}.Self()
	if err != nil {
		t.Fatal(err)
	}
	if ev, stopped := (hostproof.Prober{}).Stopped(token); stopped {
		t.Fatalf("a running child was judged stopped: %s", ev)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if ev, stopped := (hostproof.Prober{}).Stopped(token); !stopped || !strings.HasPrefix(ev, "PROCESS_EXITED") {
		t.Fatalf("an exited child was not recognised: %q %v", ev, stopped)
	}
}

func TestExecutorTakeoverNeedsProvenStoppedProcess(t *testing.T) {
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	calls := 0
	verdict := func(evidence string, stopped bool) d.ProcessProof {
		return func(recorded string) (string, bool) {
			calls++
			if recorded != "proc-a" {
				t.Fatalf("proof asked about %q", recorded)
			}
			return evidence, stopped
		}
	}
	held := func() *d.ExecutorLease {
		lease := &d.ExecutorLease{Outbox: []d.ExecutorCommand{{State: "PENDING"}}}
		if epoch, evidence, err := lease.AcquireAs("worker-a", "proc-a", at, time.Minute, nil); err != nil || epoch != 1 || evidence != "" {
			t.Fatal(epoch, evidence, err)
		}
		return lease
	}
	expired := at.Add(time.Minute)

	lease := held()
	epoch, evidence, err := lease.AcquireAs("worker-b", "proc-b", expired, time.Minute, verdict("HOST_REBOOTED fixture", true))
	if err != nil || epoch != 2 || evidence != "HOST_REBOOTED fixture" || lease.RunState != "RECOVERY_CHECK" || lease.Instance != "proc-b" {
		t.Fatal("takeover failed", epoch, evidence, err, lease.RunState, lease.Instance)
	}
	if lease.IsolatedEpoch != 1 || lease.IsolationEvidence != "HOST_REBOOTED fixture" || lease.Outbox[0].ExecutorEpoch != 2 {
		t.Fatal("previous epoch not isolated and outbox not re-owned", *lease)
	}
	if lease.Assert("worker-a", 1, expired) == nil || lease.Assert("worker-b", 2, expired) != nil {
		t.Fatal("authority did not move to the new executor")
	}
	// the same name may take over its own expired lease
	lease = held()
	if epoch, _, err = lease.AcquireAs("worker-a", "proc-a2", expired, time.Minute, verdict("PROCESS_EXITED fixture", true)); err != nil || epoch != 2 {
		t.Fatal("restart of the same executor was refused", epoch, err)
	}

	refused := func(name string, lease *d.ExecutorLease, now time.Time, proof d.ProcessProof, code string, asked int) {
		t.Helper()
		calls = 0
		before := *lease
		_, evidence, err := lease.AcquireAs("worker-b", "proc-b", now, time.Minute, proof)
		var failure *d.Error
		if err == nil || evidence != "" || !asErr(err, &failure) || failure.Code != code {
			t.Fatalf("%s: got evidence %q error %v, want %s", name, evidence, err, code)
		}
		if calls != asked {
			t.Fatalf("%s: proof consulted %d times, want %d", name, calls, asked)
		}
		if lease.Epoch != before.Epoch || lease.Holder != before.Holder || lease.Instance != before.Instance || lease.RunState != before.RunState || lease.IsolatedEpoch != before.IsolatedEpoch {
			t.Fatalf("%s: a refused takeover changed the lease", name)
		}
	}
	refused("process still running", held(), expired, verdict("", false), "PREVIOUS_EXECUTOR_NOT_ISOLATED", 1)
	refused("positive answer without evidence", held(), expired, verdict("", true), "PREVIOUS_EXECUTOR_NOT_ISOLATED", 1)
	refused("no proof available", held(), expired, nil, "PREVIOUS_EXECUTOR_NOT_ISOLATED", 0)
	refused("lease still valid", held(), at.Add(30*time.Second), verdict("HOST_REBOOTED fixture", true), "EXECUTOR_LEASE_HELD", 0)
	legacy := &d.ExecutorLease{}
	if _, err = legacy.Acquire("worker-a", at, time.Minute); err != nil {
		t.Fatal(err)
	}
	refused("record without a process identity", legacy, expired, verdict("HOST_REBOOTED fixture", true), "PREVIOUS_EXECUTOR_NOT_ISOLATED", 0)

	// an operator fence stays valid and needs no proof
	fenced := held()
	if err = fenced.Isolate(1, "operator fence"); err != nil {
		t.Fatal(err)
	}
	calls = 0
	if epoch, evidence, err = fenced.AcquireAs("worker-b", "proc-b", expired, time.Minute, verdict("unused", true)); err != nil || epoch != 2 || evidence != "" || calls != 0 {
		t.Fatal("manual fence not honoured", epoch, evidence, err, calls)
	}

	// a quick restart inside the lease window keeps the lease and records the new process
	window := held()
	calls = 0
	if epoch, evidence, err = window.AcquireAs("worker-a", "proc-a2", at.Add(10*time.Second), time.Minute, verdict("unused", true)); err != nil || epoch != 1 || evidence != "" || calls != 0 || window.Instance != "proc-a2" || window.RunState == "RECOVERY_CHECK" {
		t.Fatal("restart inside the window changed epoch or state", epoch, evidence, err, calls, window.Instance)
	}
	if _, _, err = (&d.ExecutorLease{}).AcquireAs("worker-a", "proc-a", at, 0, nil); err == nil {
		t.Fatal("missing lease policy accepted")
	}
}

func asErr(err error, target **d.Error) bool {
	failure, ok := err.(*d.Error)
	*target = failure
	return ok
}

// takeoverFlow runs the executor start-up acquisition twice against a store: a
// first executor, then a new process after the lease has lapsed.
func takeoverFlow(t *testing.T, store ports.Store) {
	t.Helper()
	ctx := context.Background()
	run, _, _, _ := serviceBase(t)
	if err := store.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	lease := 30 * time.Second
	start := time.Now().UTC()
	acquire := func(holder, instance string, at time.Time, proof d.ProcessProof) (epoch int64, err error) {
		err = store.Transaction(ctx, run.RunKey, func(r *d.Aggregate) error {
			epoch, err = a.AcquireExecutorLease(r, holder, instance, at, lease, proof)
			r.Version++
			return err
		})
		return epoch, err
	}
	gone := func(evidence string) d.ProcessProof {
		return func(recorded string) (string, bool) { return evidence, recorded == "boot1:pid100" }
	}
	if epoch, err := acquire("exec", "boot1:pid100", start, gone("unused")); err != nil || epoch != 1 {
		t.Fatal(epoch, err)
	}
	lapsed := start.Add(10 * time.Minute)
	// A process that cannot be proven stopped leaves the lease untouched.
	if _, err := acquire("exec", "boot2:pid7", lapsed, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("takeover without proof")
	} else if failure, ok := err.(*d.Error); !ok || failure.Code != "PREVIOUS_EXECUTOR_NOT_ISOLATED" {
		t.Fatal(err)
	}
	before, err := store.Read(ctx, run.RunKey)
	if err != nil || before.LeaseEpoch != 1 || before.IsolatedEpoch != 0 || before.State == "RECOVERY_CHECK" || len(before.Audit) != 0 {
		t.Fatal("a refused takeover left traces", err)
	}
	epoch, err := acquire("exec", "boot2:pid7", lapsed, gone("HOST_REBOOTED fixture boot=1->2"))
	if err != nil || epoch != 2 {
		t.Fatal(epoch, err)
	}
	after, err := store.Read(ctx, run.RunKey)
	if err != nil {
		t.Fatal(err)
	}
	if after.LeaseEpoch != 2 || after.ExecutorEpoch != 2 || after.IsolatedEpoch != 1 || after.State != "RECOVERY_CHECK" ||
		after.LeaseHolder == nil || *after.LeaseHolder != "exec" || after.LeaseInstance == nil || *after.LeaseInstance != "boot2:pid7" ||
		after.IsolationEvidence == nil || *after.IsolationEvidence != "HOST_REBOOTED fixture boot=1->2" {
		t.Fatalf("persisted lease: epoch %d isolated %d state %s", after.LeaseEpoch, after.IsolatedEpoch, after.State)
	}
	if after.LeaseUntil == nil || !after.LeaseUntil.After(lapsed) {
		t.Fatal("new lease not running")
	}
	var found bool
	err = store.View(ctx, run.RunKey, func(_ *d.Aggregate, history ports.History) error {
		events, err := history.Audit(0, 0)
		for _, event := range events {
			var action string
			_ = json.Unmarshal(event["action"], &action)
			if action == "EXECUTOR_TAKEOVER" && strings.Contains(string(event["detail"]), "HOST_REBOOTED fixture") {
				found = true
			}
		}
		return err
	})
	if err != nil || !found {
		t.Fatal("takeover not audited", err)
	}
	// The takeover is a single use: the next lapse needs fresh proof of the new process.
	if _, err = acquire("exec", "boot3:pid9", lapsed.Add(10*time.Minute), gone("HOST_REBOOTED again")); err == nil {
		t.Fatal("proof about the old process accepted for the new one")
	}
}

func TestExecutorTakeoverPersistsInMemoryStore(t *testing.T) {
	takeoverFlow(t, memory.New("SIM"))
}

func TestNativePostgresExecutorTakeover(t *testing.T) {
	ctx := context.Background()
	server, err := postgres.StartTemporary(ctx, t.TempDir(), nativePostgresBin(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	store, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	takeoverFlow(t, store)
	// A restart of the database connection keeps the recorded process.
	reopened, err := postgres.Open(ctx, server.SIMDSN, "SIM")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	run, _, _, _ := serviceBase(t)
	snapshot, err := reopened.Read(ctx, run.RunKey)
	if err != nil || snapshot.LeaseInstance == nil || *snapshot.LeaseInstance != "boot2:pid7" || snapshot.IsolatedEpoch != 1 {
		t.Fatal("lease record lost on reopen", err)
	}
}
