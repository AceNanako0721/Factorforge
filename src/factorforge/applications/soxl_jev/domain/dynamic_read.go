package domain

import (
	"encoding/json"
	"time"
)

// DynamicRecordKey orders dynamic records newest first for keyset pagination
// of the read API.
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
