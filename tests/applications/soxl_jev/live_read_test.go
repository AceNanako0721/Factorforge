package soxl_jev_test

import (
	"context"
	pg "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/adapters/postgres"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/operations"
	"github.com/jackc/pgx/v5"
	"net/url"
	"testing"
)

func TestInstanceRecordedLIVEPartitionDoesNotGrantExecution(t *testing.T) {
	server, s, _ := pgFixture(t)
	ctx := context.Background()
	s.Binding.Environment = "LIVE"
	s.Health.Stage = "R3"
	s.Jobs[1].QueueKind = "LIVE"
	s.Budgets = append(s.Budgets, d.Budget{QueueKind: "LIVE", Limit: ptr("1"), Consumed: ptr("0"), Unit: ptr("fixture-requests")})
	if err := pg.Publish(ctx, server.AdminDSN, s, nil); err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, sql := range []string{"CREATE ROLE p3_fixture_live LOGIN PASSWORD 'fixture-only-password'", "GRANT factorforge_instance_live_read TO p3_fixture_live"} {
		if _, err = admin.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err = pg.GrantReader(ctx, server.AdminDSN, s.Binding, "p3_fixture_live"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(server.AdminDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("p3_fixture_live", "fixture-only-password")
	store, err := pg.Open(ctx, u.String(), s.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, _, clock, p, policy := fixture()
	p.Binding = s.Binding
	q := operations.QueryService{Store: store, Clock: clock, Policy: policy}
	v, err := q.Read(ctx, p, "analysis-jobs", "", operations.Filter{QueueKind: "LIVE"})
	if err != nil || len(v.(operations.Page).Items) != 1 {
		t.Fatal("LIVE read partition unavailable")
	}
	_, err = q.Read(ctx, p, "analysis-jobs", "", operations.Filter{QueueKind: "SIM"})
	assertCode(t, err, "INSTANCE_SCOPE_FORBIDDEN")
	v, err = q.Read(ctx, p, "budgets", "", operations.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	budgets := v.(operations.Record).Data.([]operations.BudgetView)
	if budgets[1].RecordStatus != "NOT_APPLICABLE" || budgets[1].Consumed != nil || budgets[2].RecordStatus != "RECORDED" {
		t.Fatal("budget partitions mixed")
	}
	v, err = q.Read(ctx, p, "health", "", operations.Filter{})
	if err != nil || v.(operations.Record).Data.(operations.InstanceHealth).LiveReady {
		t.Fatal("read projection granted LIVE admission")
	}
	// Membership in both environments invalidates the already-open login.
	if _, err = admin.Exec(ctx, "GRANT factorforge_instance_sim_read TO p3_fixture_live"); err != nil {
		t.Fatal(err)
	}
	_, err = store.Read(ctx, policy.MaxSnapshotBytes)
	assertCode(t, err, "INSTANCE_DATABASE_ROLE_NOT_ISOLATED")
}
