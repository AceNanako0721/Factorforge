package domain

import (
	"encoding/json"
	"time"
)

// DynamicRecordKey orders dynamic records newest first for keyset pagination
// of the v2.10.0 read API (P3 design 29.1).
type DynamicRecordKey struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	ID   string    `json:"id"`
}

// DynamicRecord is one stored record with its key.
type DynamicRecord struct {
	DynamicRecordKey
	Payload json.RawMessage
}
