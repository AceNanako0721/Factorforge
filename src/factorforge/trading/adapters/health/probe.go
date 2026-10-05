package health

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Probe struct {
	StoragePath string
	ClockOffset func(context.Context) (decimal.Value, error)
	FreeBytes   func(string) (uint64, error)
}

func (p *Probe) Check(ctx context.Context, run *d.Aggregate) ([]string, error) {
	policy := run.Policy.Operational
	if policy == nil {
		return []string{}, nil
	}
	issues := []string{}
	free := p.FreeBytes
	if free == nil {
		free = freeBytes
	}
	available, err := free(p.StoragePath)
	if err == nil && available < uint64(policy.MinDiskBytes) {
		issues = append(issues, "DISK_CAPACITY")
	}
	var offset decimal.Value
	var clockErr error
	if p.ClockOffset == nil {
		clockErr = &d.Error{Code: "CLOCK_PROBE_UNAVAILABLE", Status: 503}
	} else {
		offset, clockErr = p.ClockOffset(ctx)
	}
	if err != nil || clockErr != nil {
		issues = append(issues, "HOST_HEALTH_UNAVAILABLE")
	} else if offset.Abs().Cmp(policy.MaxClockSkewSeconds) > 0 {
		issues = append(issues, "CLOCK_UNVERIFIED_OR_DRIFTED")
	}
	if int64(len(run.Audit)) >= policy.MaxAuditRecords {
		issues = append(issues, "AUDIT_RETENTION_CAPACITY")
	}
	pending := int64(0)
	old := false
	for _, item := range run.Outbox {
		if item.State != "DONE" {
			pending++
			order := run.Orders.Value(item.OrderID)
			old = old || order == nil || run.Clock.Sub(order.CreatedAt) > time.Duration(policy.MaxCommandAgeSeconds)*time.Second
		}
	}
	if pending >= policy.MaxPendingCommands {
		issues = append(issues, "EXECUTION_QUEUE_CAPACITY")
	}
	if old {
		issues = append(issues, "EXECUTION_QUEUE_AGE")
	}
	return issues, nil
}
func ClockProbe(client *http.Client, endpoint string, now func() time.Time) func(context.Context) (decimal.Value, error) {
	return func(ctx context.Context) (decimal.Value, error) {
		failure := func() (decimal.Value, error) {
			return decimal.Value{}, &d.Error{Code: "CLOCK_PROBE_UNAVAILABLE", Status: 503}
		}
		request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if err != nil {
			return failure()
		}
		safe := *client
		safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := safe.Do(request)
		if err != nil {
			return failure()
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return failure()
		}
		var sample struct {
			ServerTime int64 `json:"serverTime"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&sample) != nil || sample.ServerTime == 0 {
			return failure()
		}
		delta, _ := decimal.Parse(strconv.FormatInt(now().UnixMilli()-sample.ServerTime, 10))
		thousand, _ := decimal.Parse("1000")
		m := decimal.NewMath(28)
		offset := m.Div(delta, thousand)
		return offset, m.Err()
	}
}
