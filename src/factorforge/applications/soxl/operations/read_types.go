package operations

import (
	"time"
)

type ReadPolicy struct {
	DefaultLimit, MaxLimit, MaxRecords, MaxStateBytes int
	CursorAge                                         time.Duration
	CursorKey                                         []byte
}

func (p ReadPolicy) Valid() bool {
	return p.DefaultLimit > 0 && p.MaxLimit >= p.DefaultLimit && p.MaxRecords >= p.MaxLimit && p.MaxStateBytes > 0 && p.CursorAge > 0 && len(p.CursorKey) >= 32
}

type Filter struct {
	Cursor string     `json:"cursor"`
	Limit  int        `json:"limit"`
	From   *time.Time `json:"from"`
	To     *time.Time `json:"to"`
}
type Metadata struct {
	SchemaVersion   string     `json:"schema_version"`
	Environment     string     `json:"environment"`
	InstanceID      string     `json:"instance_id"`
	SourceVersion   string     `json:"source_version"`
	ObservedAt      *time.Time `json:"observed_at"`
	SnapshotVersion string     `json:"snapshot_version"`
}
type Record struct {
	Metadata
	Data any `json:"data"`
}
type Page struct {
	Metadata
	Items  []any   `json:"items"`
	Cursor *string `json:"cursor"`
}

func latest(values ...*time.Time) *time.Time {
	var result *time.Time
	for _, v := range values {
		if v != nil && (result == nil || v.After(*result)) {
			copy := *v
			result = &copy
		}
	}
	return result
}
