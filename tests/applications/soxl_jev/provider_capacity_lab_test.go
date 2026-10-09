package soxl_jev_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
)

// Opt-in counterexample: correct local queue limits cannot reserve a capacity
// shared by separate instances. The HTTP capacity of one is a laboratory
// control, not a discovered JEV account limit; no real supplier is contacted.
func TestSharedAccountLocalQueueCounterexample(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_LOCAL_CAPACITY_LAB")
	if lab == "" {
		t.Skip("opt-in shared account counterexample")
	}
	if !filepath.IsAbs(lab) {
		t.Fatal("CAPACITY_LAB_PATH_INVALID")
	}
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	stores := []*pg.PipelineStore{}
	jobs := []*d.PipelineJob{}
	for i := 0; i < 2; i++ {
		evidence, request, _, now := pipelineFixture()
		binding := d.Binding{InstanceID: fmt.Sprintf("lab-instance-%d", i), Environment: "SIM"}
		request.Binding, request.Routing.Binding = binding, binding
		request.RequestID = fmt.Sprintf("lab-job-%d", i)
		if err = pg.InitializePipeline(ctx, server.AdminDSN, binding); err != nil {
			t.Fatal(err)
		}
		local := map[string]*pg.PipelineStore{}
		for _, kind := range []string{"INGEST", "TRADING"} {
			login := fmt.Sprintf("lab_%d_%s", i, kind)
			if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
				t.Fatal(err)
			}
			if err = pg.GrantPipeline(ctx, server.AdminDSN, binding, login, kind); err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(server.AdminDSN)
			u.User = url.UserPassword(login, "fixture-only-password")
			local[kind], err = pg.OpenPipeline(ctx, u.String(), binding, kind, 100000)
			if err != nil {
				t.Fatal(err)
			}
			defer local[kind].Close()
		}
		if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: binding, Kind: "SIM", Bucket: "lab-local", PolicyRef: "lab-policy", MaxJobs: 1, MaxConcurrent: 1, ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err = local["INGEST"].Record(ctx, evidence, request.Routing); err != nil {
			t.Fatal(err)
		}
		job := d.PipelineJob{JobID: request.RequestID, Binding: binding, QueueKind: "SIM", Request: request, State: "QUEUED", CreatedAt: now, Deadline: request.Deadline, ReasonCodes: []string{}}
		if err = local["INGEST"].Enqueue(ctx, job, "lab-local"); err != nil {
			t.Fatal(err)
		}
		claimed, err := local["TRADING"].Claim(ctx, "lab-worker", now, time.Minute)
		if err != nil || claimed == nil {
			t.Fatal("local admission failed", err)
		}
		if err = local["TRADING"].StartProvider(ctx, *claimed, now); err != nil {
			t.Fatal(err)
		}
		stores, jobs = append(stores, local["TRADING"]), append(jobs, claimed)
	}
	var inFlight, accepted, rateLimited atomic.Int32
	entered, release := make(chan struct{}, 1), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !inFlight.CompareAndSwap(0, 1) {
			rateLimited.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		accepted.Add(1)
		entered <- struct{}{}
		<-release
		inFlight.Store(0)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	first := make(chan int, 1)
	go func() {
		response, err := client.Post(upstream.URL, "application/json", nil)
		if err != nil {
			first <- 0
			return
		}
		response.Body.Close()
		first <- response.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("synthetic upstream not reached")
	}
	second, err := client.Post(upstream.URL, "application/json", nil)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if <-first != 200 || second.StatusCode != 429 || accepted.Load() != 1 || rateLimited.Load() != 1 {
		t.Fatal("shared account counterexample not reproduced")
	}
	for i, store := range stores {
		// Both actual persisted queues authorized the one model call independently.
		if err = store.Complete(ctx, *jobs[i], "FAILED", []string{"LAB_ONLY"}, jobs[i].CreatedAt); err != nil {
			t.Fatal(err)
		}
	}
	report, _ := json.MarshalIndent(map[string]any{"purpose": "LOCAL_QUEUE_ISOLATION_IS_NOT_SHARED_ACCOUNT_RESERVATION", "instances": 2,
		"local_max_concurrent_each": 1, "local_admissions": 2, "synthetic_upstream_concurrency": 1, "accepted": accepted.Load(), "http_429": rateLimited.Load(),
		"real_supplier_calls": 0, "real_account_limit": nil, "production_modified": false, "orders": 0}, "", "  ")
	f, err := os.OpenFile(filepath.Join(lab, "local-queue-counterexample.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("CAPACITY_REPORT_EXISTS_OR_UNAVAILABLE")
	}
	_, written := f.Write(report)
	closed := f.Close()
	if written != nil || closed != nil {
		t.Fatal("CAPACITY_REPORT_WRITE_FAILED")
	}
	t.Log("actual_postgres_local_admissions=2; synthetic_upstream_429=1; real_supplier_calls=0; orders=0")
}

// A disposable SQL prototype tests the proposed shared reservation method
// before its design is finalized. Tables/functions are confined to this test
// database; production packages neither embed nor invoke this SQL.
func TestSharedReservationPrototype(t *testing.T) {
	lab := os.Getenv("FACTORFORGE_LOCAL_CAPACITY_LAB")
	if lab == "" {
		t.Skip("opt-in shared reservation prototype")
	}
	ctx := context.Background()
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	// All numeric limits below are explicit development controls only.
	_, err = admin.Exec(ctx, `
CREATE SCHEMA capacity_lab;
REVOKE ALL ON SCHEMA capacity_lab FROM PUBLIC;
CREATE TABLE capacity_lab.pool(id int PRIMARY KEY, used int NOT NULL, paused boolean NOT NULL);
INSERT INTO capacity_lab.pool VALUES(1,0,false);
CREATE TABLE capacity_lab.grants(login text PRIMARY KEY, class text NOT NULL);
CREATE TABLE capacity_lab.calls(login text NOT NULL,job text NOT NULL,class text NOT NULL,state text NOT NULL,PRIMARY KEY(login,job));
CREATE FUNCTION capacity_lab.acquire(job_id text) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE c text; p capacity_lab.pool; n int;
BEGIN
 SELECT class INTO c FROM capacity_lab.grants WHERE login=session_user;
 IF c IS NULL OR NOT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=session_user AND rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND NOT rolreplication) THEN RETURN 'FORBIDDEN'; END IF;
 SELECT * INTO p FROM capacity_lab.pool WHERE id=1 FOR UPDATE;
 IF EXISTS(SELECT 1 FROM capacity_lab.calls WHERE login=session_user AND job=job_id) THEN RETURN 'DUPLICATE_NO_RETRY'; END IF;
 IF p.paused THEN RETURN 'PAUSED'; END IF;
 SELECT count(*) INTO n FROM capacity_lab.calls WHERE class=c;
 IF p.used>=6 OR n>=2 THEN RETURN 'BUDGET'; END IF;
 SELECT count(*) INTO n FROM capacity_lab.calls WHERE class=c AND state IN ('STARTED','UNKNOWN');
 IF n>=1 THEN RETURN 'CONCURRENCY'; END IF;
 SELECT count(*) INTO n FROM capacity_lab.calls WHERE state IN ('STARTED','UNKNOWN');
 IF n>=3 THEN RETURN 'CONCURRENCY'; END IF;
 INSERT INTO capacity_lab.calls VALUES(session_user,job_id,c,'STARTED');
 UPDATE capacity_lab.pool SET used=used+1 WHERE id=1;
 RETURN 'ACQUIRED';
END $$;
CREATE FUNCTION capacity_lab.finish(job_id text,outcome text) RETURNS text LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM 1 FROM capacity_lab.pool WHERE id=1 FOR UPDATE;
 IF NOT EXISTS(SELECT 1 FROM capacity_lab.grants WHERE login=session_user) THEN RETURN 'FORBIDDEN'; END IF;
 IF outcome NOT IN ('COMPLETE','UNKNOWN','RATE_LIMITED') THEN RETURN 'INVALID'; END IF;
 UPDATE capacity_lab.calls SET state=outcome WHERE login=session_user AND job=job_id AND state='STARTED';
 IF NOT FOUND THEN RETURN 'FINAL_OR_MISSING'; END IF;
 IF outcome='RATE_LIMITED' THEN UPDATE capacity_lab.pool SET paused=true WHERE id=1; END IF;
 RETURN 'FINISHED';
END $$;
REVOKE ALL ON FUNCTION capacity_lab.acquire(text),capacity_lab.finish(text,text) FROM PUBLIC;`)
	if err != nil {
		t.Fatal(err)
	}
	dsns := map[string]string{}
	for _, name := range []string{"research_a", "research_b", "sim", "live"} {
		class := "RESEARCH"
		if name == "sim" {
			class = "SIM"
		}
		if name == "live" {
			class = "LIVE"
		}
		login := "lab_" + name
		_, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'; GRANT USAGE ON SCHEMA capacity_lab TO "+pgx.Identifier{login}.Sanitize()+"; GRANT EXECUTE ON FUNCTION capacity_lab.acquire(text),capacity_lab.finish(text,text) TO "+pgx.Identifier{login}.Sanitize())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = admin.Exec(ctx, "INSERT INTO capacity_lab.grants VALUES($1,$2)", login, class); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[name] = u.String()
	}
	query := func(name, sql string, args ...any) string {
		t.Helper()
		conn, err := pgx.Connect(ctx, dsns[name])
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		var result string
		if err = conn.QueryRow(ctx, sql, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	var group sync.WaitGroup
	results := make(chan string, 16)
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			name := "research_a"
			if i%2 == 1 {
				name = "research_b"
			}
			conn, e := pgx.Connect(ctx, dsns[name])
			if e != nil {
				results <- "ERROR"
				return
			}
			defer conn.Close(ctx)
			var value string
			e = conn.QueryRow(ctx, "SELECT capacity_lab.acquire($1)", fmt.Sprintf("research-%d", i)).Scan(&value)
			if e != nil {
				results <- "ERROR"
			} else {
				results <- value
			}
		}(i)
	}
	group.Wait()
	close(results)
	granted := 0
	for result := range results {
		if result == "ACQUIRED" {
			granted++
		} else if result != "CONCURRENCY" {
			t.Fatal("unexpected reservation result", result)
		}
	}
	if granted != 1 {
		t.Fatal("cross-instance shared cap failed", granted)
	}
	if query("sim", "SELECT capacity_lab.acquire($1)", "sim-1") != "ACQUIRED" || query("live", "SELECT capacity_lab.acquire($1)", "live-1") != "ACQUIRED" {
		t.Fatal("reserved partitions borrowed")
	}
	if query("sim", "SELECT capacity_lab.finish($1,$2)", "sim-1", "UNKNOWN") != "FINISHED" || query("sim", "SELECT capacity_lab.acquire($1)", "sim-2") != "CONCURRENCY" {
		t.Fatal("unknown delivery freed capacity")
	}
	if query("sim", "SELECT capacity_lab.acquire($1)", "sim-1") != "DUPLICATE_NO_RETRY" {
		t.Fatal("reconnect retried delivery")
	}
	if query("live", "SELECT capacity_lab.finish($1,$2)", "sim-1", "COMPLETE") != "FINAL_OR_MISSING" {
		t.Fatal("peer receipt changed")
	}
	if query("live", "SELECT capacity_lab.finish($1,$2)", "live-1", "RATE_LIMITED") != "FINISHED" || query("live", "SELECT capacity_lab.acquire($1)", "live-2") != "PAUSED" {
		t.Fatal("shared pause lost")
	}
	conn, err := pgx.Connect(ctx, dsns["research_a"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, sql := range []string{"SELECT * FROM capacity_lab.calls", "UPDATE capacity_lab.pool SET used=0", "DELETE FROM capacity_lab.grants", "CREATE TABLE capacity_lab.bypass(id int)"} {
		if _, err = conn.Exec(ctx, sql); err == nil {
			t.Fatal("private state or operator privilege exposed")
		}
	}
	var used int
	if err = admin.QueryRow(ctx, "SELECT used FROM capacity_lab.pool WHERE id=1").Scan(&used); err != nil || used != 3 {
		t.Fatal("denial debited or finish refunded", used, err)
	}
	report, _ := json.MarshalIndent(map[string]any{"purpose": "DISPOSABLE_SHARED_RESERVATION_PROTOTYPE", "research_attempts": 16, "research_grants": granted, "sim_reserved": true, "live_reserved": true, "unknown_delivery_held": true, "duplicate_no_retry": true, "cross_role_mutation_rejected": true, "worker_direct_state_rejected": true, "pause_persisted_across_connections": true, "used_requests": used, "denied_requests_debited": false, "real_supplier_calls": 0, "production_installed": false, "orders": 0}, "", "  ")
	f, err := os.OpenFile(filepath.Join(lab, "shared-reservation-prototype.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("CAPACITY_REPORT_EXISTS_OR_UNAVAILABLE")
	}
	_, written := f.Write(report)
	closed := f.Close()
	if written != nil || closed != nil {
		t.Fatal("CAPACITY_REPORT_WRITE_FAILED")
	}
	t.Log("research_attempts=16; shared_grants=1; SIM/LIVE_reservations_preserved=true; unknown_held=true; private_state_rejected=true; supplier_calls=0")
}
