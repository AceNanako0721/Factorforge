package agent_test

import (
	"testing"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/operations/agent/domain"
)

// T6-05: only registered transitions; closing needs the developer and the
// owner's words.
func TestIssueTransitions(t *testing.T) {
	allowed := []struct{ from, to, actor string }{
		{d.Open, d.Watch, d.Manager}, {d.Watch, d.Open, d.Manager},
		{d.Open, d.Fixing, d.Developer}, {d.Fixing, d.Verifying, d.Developer},
		{d.Verifying, d.Fixing, d.Manager}, {d.Verifying, d.Closed, d.Developer}, {d.Watch, d.Closed, d.Developer},
	}
	for _, c := range allowed {
		if !d.CanMove(c.from, c.to, c.actor) {
			t.Fatal("should allow", c)
		}
	}
	forbidden := []struct{ from, to, actor string }{
		{d.Open, d.Closed, d.Manager}, {d.Verifying, d.Closed, d.Manager}, {d.Open, d.Fixing, d.Manager},
		{d.Fixing, d.Verifying, d.Manager}, {d.Closed, d.Open, d.Developer}, {d.Closed, d.Open, d.Manager},
		{d.Open, d.Verifying, d.Developer},
	}
	for _, c := range forbidden {
		if d.CanMove(c.from, c.to, c.actor) {
			t.Fatal("should forbid", c)
		}
	}
	i := d.Issue{ID: "I-0001", State: d.Verifying}
	if _, err := d.Move(i, d.Closed, d.Developer, "fixed", "", t0); d.Code(err) != "OWNER_WORDS_REQUIRED" {
		t.Fatal("close without owner words", err)
	}
	e, err := d.Move(i, d.Closed, d.Developer, "fixed and verified", "可以关", t0)
	if err != nil || e.From != d.Verifying || e.To != d.Closed || e.Owner != "可以关" {
		t.Fatal("close", e, err)
	}
	if _, err := d.Move(i, d.Fixing, d.Manager, "", "", t0); d.Code(err) != "NOTE_REQUIRED" {
		t.Fatal("note required", err)
	}
}
