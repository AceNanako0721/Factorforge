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

// StopEventFilter picks STOP_EVENT rows by payload kind (S3-032): action
// records leave out the initial plans and hourly price states, plan records
// read only those.
type StopEventFilter int

const (
	StopEventsActions StopEventFilter = iota
	StopEventsPlans
)

// DynamicRecord is one stored record with its key.
type DynamicRecord struct {
	DynamicRecordKey
	Payload json.RawMessage
}
