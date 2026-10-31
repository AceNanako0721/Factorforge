package api

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl/operations"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The published response values contain no open provider/worker dictionaries.
type DynamicStatusView = operations.DynamicStatusView
type DynamicJudgmentView = operations.DynamicJudgmentView
type DynamicActionView = operations.DynamicActionView

func readFilter(raw, resource string) (operations.Filter, error) {
	var f operations.Filter
	values, err := url.ParseQuery(raw)
	if err != nil {
		return f, d.Fail("QUERY_PARAMETER_INVALID", 422)
	}
	allowed := []string{}
	switch resource {
	case "dynamic-judgments", "dynamic-actions":
		allowed = []string{"from", "to", "cursor", "limit"}
	}
	for key, items := range values {
		if !d.Has(allowed, key) || len(items) != 1 || items[0] == "" {
			return f, d.Fail("QUERY_PARAMETER_INVALID", 422)
		}
		value := items[0]
		switch key {
		case "cursor":
			f.Cursor = value
		case "limit":
			if strings.HasPrefix(value, "+") {
				return f, d.Fail("QUERY_LIMIT_INVALID", 422)
			}
			n, e := strconv.Atoi(value)
			if e != nil || n < 1 {
				return f, d.Fail("QUERY_LIMIT_INVALID", 422)
			}
			f.Limit = n
		case "from", "to":
			at, e := time.Parse(time.RFC3339Nano, value)
			if e != nil || at.IsZero() || !strings.HasSuffix(value, "Z") {
				return f, d.Fail("QUERY_TIME_INVALID", 422)
			}
			if key == "from" {
				f.From = &at
			} else {
				f.To = &at
			}
		}
	}
	return f, nil
}
