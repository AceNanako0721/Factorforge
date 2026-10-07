// Package dto exposes the existing HTTP value contract to application clients.
// It exposes no store, admission service, clock, worker or StrategyState.
package dto

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"io"
)

type Command = d.Command
type CreateObject = d.CreateObject
type ObservedObject = d.ObservedObject
type RunBinding = d.RunBinding
type InstrumentBinding = d.InstrumentBinding
type Policy = d.Policy
type ParameterSnapshot = d.ParameterSnapshot
type EventCommand = d.EventCommand
type Event = d.Event
type EvidenceRef = d.EvidenceRef
type Claim = d.Claim
type ScoreVector = d.ScoreVector
type ScoreSubmission = d.ScoreSubmission
type ScoreCommand = d.ScoreCommand
type AdmissionReceipt = d.AdmissionReceipt

// Decode uses the frozen wire schema, including required/null/decimal fields.
func Decode(reader io.Reader, target any) error { return d.Decode(reader, target) }
