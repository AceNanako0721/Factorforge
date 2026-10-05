package trading_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	trading "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

func TestDecimalWireAndImmutability(t *testing.T) {
	for _, input := range []string{`1.23`, `null`, `true`, `"NaN"`, `"Infinity"`, `"-Infinity"`, `""`, `"secret-invalid-value"`} {
		var v decimal.Value
		err := json.Unmarshal([]byte(input), &v)
		if err == nil {
			t.Fatalf("accepted %s", input)
		}
		if strings.Contains(err.Error(), "secret-invalid-value") {
			t.Fatal("numeric error leaks input")
		}
	}
	for _, text := range []string{"0", "-0.000", "1.2300", "1E-30", "123456789012345678901234567890.12"} {
		v, err := decimal.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var copy decimal.Value
		if json.Unmarshal(data, &copy) != nil || copy.Cmp(v) != 0 {
			t.Fatal("wire value changed")
		}
	}
	original, _ := decimal.Parse("123.45")
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			m := decimal.NewMath(28)
			one, _ := decimal.Parse("1")
			m.Add(original, one)
			original.Neg()
			original.Abs()
			if m.Err() != nil {
				t.Error(m.Err())
			}
		}()
	}
	wait.Wait()
	if original.String() != "123.45" {
		t.Fatal("value was mutated")
	}
}
func TestArithmeticFailsWithoutManufacturingFiniteResult(t *testing.T) {
	one, _ := decimal.Parse("1")
	m := decimal.NewMath(28)
	m.Div(one, decimal.Value{})
	m.Add(one, one)
	if m.Err() == nil {
		t.Fatal("division failure was lost")
	}
}
func TestExecutorFencingAndOutbox(t *testing.T) {
	at := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	lease := trading.ExecutorLease{Outbox: []trading.ExecutorCommand{{State: "PENDING"}, {State: "DONE", ExecutorEpoch: 99}}}
	if _, err := lease.Acquire("worker-a", at, 0); err == nil {
		t.Fatal("missing lease policy accepted")
	}
	epoch, err := lease.Acquire("worker-a", at, time.Minute)
	if err != nil || epoch != 1 {
		t.Fatal(epoch, err)
	}
	if _, err = lease.Acquire("worker-b", at, time.Minute); err == nil {
		t.Fatal("two executors accepted")
	}
	if _, err = lease.Acquire("worker-b", at.Add(time.Minute), time.Minute); err == nil {
		t.Fatal("expiration used as isolation proof")
	}
	if err = lease.Isolate(0, "fixture"); err == nil {
		t.Fatal("old epoch accepted")
	}
	if err = lease.Isolate(epoch, ""); err == nil {
		t.Fatal("missing isolation accepted")
	}
	if err = lease.Isolate(epoch, "fixture-isolation"); err != nil {
		t.Fatal(err)
	}
	epoch, err = lease.Acquire("worker-b", at.Add(time.Minute), time.Minute)
	if err != nil || epoch != 2 || lease.RunState != "RECOVERY_CHECK" {
		t.Fatal(epoch, err)
	}
	if lease.Outbox[0].ExecutorEpoch != 2 || lease.Outbox[1].ExecutorEpoch != 99 {
		t.Fatal("pending/done outbox epochs changed incorrectly")
	}
	if lease.Assert("worker-a", 1, at.Add(time.Minute)) == nil {
		t.Fatal("old executor retained authority")
	}
	if err = lease.Assert("worker-b", 2, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if lease.Assert("worker-b", 2, at.Add(2*time.Minute)) == nil {
		t.Fatal("expired executor retained authority")
	}
}
