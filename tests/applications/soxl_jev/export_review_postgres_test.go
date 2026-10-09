package soxl_jev_test

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/config"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	tradingpg "github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/pelletier/go-toml/v2"
)

func TestExportReviewPostgresNativeReadOnlyAndRoleBoundary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	request := bundleRequest(`<p>Acme preliminary Q3 2026 revenue was 3-3/4 USD.</p>`)
	server, err := tradingpg.StartTemporary(ctx, t.TempDir(), nativePG(t))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if err = pg.InitializePipeline(ctx, server.AdminDSN, request.Binding); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	dsns := map[string]string{}
	for _, kind := range []string{"INGEST", "RESEARCH", "TRADING"} {
		login := "bundle_" + strings.ToLower(kind)
		if _, err = admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{login}.Sanitize()+" LOGIN PASSWORD 'fixture-only-password'"); err != nil {
			t.Fatal(err)
		}
		if err = pg.GrantPipeline(ctx, server.AdminDSN, request.Binding, login, kind); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(server.AdminDSN)
		u.User = url.UserPassword(login, "fixture-only-password")
		dsns[kind] = u.String()
	}
	store, err := pg.OpenPipeline(ctx, dsns["INGEST"], request.Binding, "INGEST", 200000)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	// A failed/unknown extraction is still the exact archived original. Export
	// must neither mark it complete nor alter its first-public/received times.
	unknown := d.ExtractedEvidence{Raw: request.ProposalRequest.Raw, ExtractorID: "fixture-unavailable", ExtractorVersion: "fixture-v1", CompletedAt: request.ProposalRequest.Raw.ReceivedAt.Add(time.Second), Complete: false, VerificationManifest: "fixture-unknown"}
	receipt := d.RoutingReceipt{RoutingID: "fixture-quarantine", Binding: request.Binding, EvidenceID: unknown.Raw.EvidenceID, ContentHash: unknown.Raw.ContentHash, ManifestHash: d.Digest(unknown), Route: "QUARANTINE", AvailableAt: unknown.CompletedAt, EvaluatedAt: unknown.CompletedAt, ReasonCodes: []string{"EVIDENCE_UNVERIFIED"}}
	if err = store.Record(ctx, unknown, receipt); err != nil {
		t.Fatal(err)
	}
	if err = pg.ConfigureQueueBudget(ctx, server.AdminDSN, pg.QueueBudget{Binding: request.Binding, Kind: "SIM", Bucket: "fixture-budget", PolicyRef: "fixture-policy", MaxJobs: 2, MaxConcurrent: 1, ValidFrom: unknown.CompletedAt.Add(-time.Hour), ValidUntil: unknown.CompletedAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Snapshot every existing table, not just the selected manifest. This catches
	// accidental task, budget, operation or report mutations on operator reads.
	snapshot := func() string {
		rows, e := admin.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='instance_pipeline_sim' ORDER BY tablename")
		if e != nil {
			t.Fatal(e)
		}
		names := []string{}
		for rows.Next() {
			var name string
			if rows.Scan(&name) != nil {
				t.Fatal("table name")
			}
			names = append(names, name)
		}
		rows.Close()
		values := map[string]string{}
		for _, name := range names {
			var value string
			sql := "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb)::text FROM instance_pipeline_sim." + pgx.Identifier{name}.Sanitize() + " t"
			if admin.QueryRow(ctx, sql).Scan(&value) != nil {
				t.Fatal("table snapshot", name)
			}
			values[name] = value
		}
		return d.Digest(values)
	}
	before := snapshot()
	plan := operations.ExportReviewRequest{SchemaVersion: 1, Binding: request.Binding, EvidenceID: unknown.Raw.EvidenceID, Catalog: request.ProposalRequest.Catalog, Limits: request.ProposalRequest.Limits, MediaType: request.MediaType, ViewLimits: request.ViewLimits}
	input := filepath.Join(root, "export-plan.json")
	writePrivateJSON(t, input, plan)
	out := filepath.Join(root, "runtime", "export.json")
	if err = operations.ExportReviewBundleFile(ctx, store, request.Binding, root, input, out, 200000); err != nil {
		t.Fatal(err)
	}
	var bundle operations.ReviewBundle
	encoded, _ := os.ReadFile(out)
	if d.DecodePrivate(encoded, &bundle) != nil || bundle.Proposal.Raw != unknown.Raw || bundle.Proposal.Status != "REVIEW_REQUIRED" || bundle.Proposal.Raw.FirstPublicAt != nil {
		t.Fatal("unknown original promoted or rewritten")
	}
	plan.MethodVersion = "paragraph-literal-2"
	writePrivateJSON(t, input, plan)
	if err = operations.ExportReviewBundleFile(ctx, store, request.Binding, root, input, filepath.Join(root, "runtime", "fraction.json"), 200000); err != nil {
		t.Fatal(err)
	}
	fractionRaw, _ := os.ReadFile(filepath.Join(root, "runtime", "fraction.json"))
	var fraction operations.ReviewBundle
	if d.DecodePrivate(fractionRaw, &fraction) != nil || fraction.Proposal.MethodVersion != "paragraph-literal-2" || fraction.BundleID == bundle.BundleID || operations.ValidateReviewBundle(fraction) != nil {
		t.Fatal("export method not preserved")
	}
	found := false
	for _, a := range fraction.Proposal.Anchors {
		if a.Kind == "NUMBER_LEXEME" && a.Text == "3-3/4" {
			found = true
		}
	}
	if !found {
		t.Fatal("fraction missing from database export")
	}
	plan.MethodVersion = ""
	writePrivateJSON(t, input, plan)
	store.Close()
	store, err = pg.OpenPipeline(ctx, dsns["INGEST"], request.Binding, "INGEST", 200000)
	if err != nil {
		t.Fatal(err)
	}
	if err = operations.ExportReviewBundleFile(ctx, store, request.Binding, root, input, filepath.Join(root, "runtime", "restart.json"), 200000); err != nil {
		t.Fatal(err)
	}
	restarted, _ := os.ReadFile(filepath.Join(root, "runtime", "restart.json"))
	if string(restarted) != string(encoded) {
		t.Fatal("restart refreshed identity or time")
	}
	for _, change := range []func(*operations.ExportReviewRequest){
		func(p *operations.ExportReviewRequest) { p.Binding.InstanceID = "other-instance" },
		func(p *operations.ExportReviewRequest) { p.Binding.Environment = "LIVE" },
		func(p *operations.ExportReviewRequest) { p.EvidenceID = "missing-evidence" },
	} {
		bad := plan
		change(&bad)
		writePrivateJSON(t, input, bad)
		if operations.ExportReviewBundleFile(ctx, store, request.Binding, root, input, filepath.Join(root, "runtime", "forbidden.json"), 200000) == nil {
			t.Fatal("unbound/missing export succeeded")
		}
	}
	other := request.Binding
	other.InstanceID = "other-instance"
	if _, err = pg.OpenPipeline(ctx, dsns["INGEST"], other, "INGEST", 200000); err == nil {
		t.Fatal("ungranted instance opened")
	}
	if _, err = pg.OpenPipeline(ctx, server.AdminDSN, request.Binding, "INGEST", 200000); err == nil {
		t.Fatal("administrator used as ingest")
	}
	writePrivateJSON(t, input, plan)
	for _, kind := range []string{"RESEARCH", "TRADING"} {
		peer, e := pg.OpenPipeline(ctx, dsns[kind], request.Binding, kind, 200000)
		if e != nil {
			t.Fatal(e)
		}
		e = operations.ExportReviewBundleFile(ctx, peer, request.Binding, root, input, filepath.Join(root, "runtime", kind+".json"), 200000)
		peer.Close()
		if e == nil {
			t.Fatal("analysis role exported raw")
		}
	}
	// Run the actual CLI with the already supported derived INGEST profile.
	// It must not load assets/fixtures, contact framework, or need publication
	// administrator credentials, model credentials, Python or Node.
	settings := config.PipelineSettings{Environment: "SIM", InstanceID: request.Binding.InstanceID, ObjectID: "fixture-object", Stage: "R0", AssetsFile: filepath.Join(root, "unavailable-assets.json"), FixtureInputFile: filepath.Join(root, "unavailable-fixture.json"), PollSeconds: 1, TimeoutSeconds: 10, LeaseSeconds: 5, TaskTTLSeconds: 60, MaxInputBytes: 200000, MaxOutboxes: 1, MaxFrameworkPages: 1, ResearchBucket: "fixture-budget", TradingBucket: "fixture-budget", QuestionSetVersion: "fixture-questions", PromptVersion: "fixture-prompt", RubricVersion: "fixture-rubric", CalibrationVersion: "fixture-calibration", ModelVersion: "fixture-model"}
	profile := config.WorkerProfile{SchemaVersion: 1, Role: "INGEST", Mode: "mock", Settings: settings, Access: config.WorkerAccess{DatabaseURL: dsns["INGEST"], FrameworkURL: "http://127.0.0.1:1", FrameworkToken: "fixture-unavailable"}}
	profilePath := filepath.Join(root, "ingest.toml")
	tomlBytes, err := toml.Marshal(profile)
	if err != nil || os.WriteFile(profilePath, tomlBytes, 0600) != nil {
		t.Fatal("profile write", err)
	}
	bin := filepath.Join(root, "instance-cli")
	build := exec.Command("go", "build", "-o", bin, "../../../src/factorforge/applications/soxl_jev/entrypoints/instance-cli")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %v %s", err, output)
	}
	cliOutput := filepath.Join(root, "runtime", "cli.json")
	command := func() *exec.Cmd {
		cmd := exec.Command(bin, "--action", "export-evidence", "--config", profilePath, "--proposal-input", input, "--proposal-output", cliOutput, "--max-proposal-bytes", "200000")
		cmd.Dir = root
		cmd.Env = []string{"PATH=/nonexistent"}
		return cmd
	}
	if output, err := command().CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "INSTANCE_OPERATOR_OPERATION_RECORDED" {
		t.Fatalf("native export %v %s", err, output)
	}
	cliBytes, _ := os.ReadFile(cliOutput)
	if string(cliBytes) != string(encoded) {
		t.Fatal("native CLI export differs")
	}
	if output, err := command().CombinedOutput(); err == nil || !strings.Contains(string(output), "PROPOSAL_OUTPUT_EXISTS") {
		t.Fatal("CLI restart overwrote export")
	}
	if snapshot() != before {
		t.Fatal("export mutated database/queues/budget")
	}
	saved, err := store.Evidence(ctx, unknown.Raw.EvidenceID)
	if err != nil || saved == nil || d.Digest(saved) != d.Digest(unknown) {
		t.Fatal("archived failure changed", err)
	}
	t.Logf("real_postgres_tables_unchanged=true; native_export=true; raw_complete=%t; first_public=UNKNOWN", saved.Complete)
}
