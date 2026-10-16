// Package dto exposes the existing HTTP value contract to application clients.
// It exposes no store, admission service, clock, worker or StrategyState.
package dto

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"io"
)

type PolicyWindow = d.PolicyWindow
type TimeWindowPlan = d.TimeWindowPlan
type WindowCommand = d.WindowCommand

type Command = d.Command
type CreateObject = d.CreateObject
type ObservedObject = d.ObservedObject
type RunBinding = d.RunBinding
type InstrumentBinding = d.InstrumentBinding
type Policy = d.Policy
type Composition = d.Composition
type BaselineFactor = d.BaselineFactor
type AssessmentFactor = d.AssessmentFactor
type LayerCap = d.LayerCap
type AdjustmentRule = d.AdjustmentRule
type RebalanceRule = d.RebalanceRule
type FactorDefinition = d.FactorDefinition
type FactorObservation = d.FactorObservation
type FactorObservationCommand = d.FactorObservationCommand
type FactorSeries = d.FactorSeries
type ScheduledEvent = d.ScheduledEvent
type ScheduledEventCommand = d.ScheduledEventCommand
type ScheduledTemplate = d.ScheduledTemplate
type DecisionComponents = d.DecisionComponents
type LayerDay = d.LayerDay
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

type StopDirectiveCommand = d.StopDirectiveCommand
type StopDirective = d.StopDirective
type StopDirectiveReceipt = d.StopDirectiveReceipt

// v2.9.0 (S2-033～042) value contract.
type SizingRule = d.SizingRule
type ReentryRule = d.ReentryRule
type ChainLossRule = d.ChainLossRule
type PreEventRule = d.PreEventRule
type TransferRule = d.TransferRule
type ExitRequest = d.ExitRequest
type ExitRequestCommand = d.ExitRequestCommand
type ExitRequestRecord = d.ExitRequestRecord
type BaselineTransfer = d.BaselineTransfer
type BaselineTransferCommand = d.BaselineTransferCommand
type BaselineTransferRecord = d.BaselineTransferRecord
type TransferObservation = d.TransferObservation
type RiskUnitState = d.RiskUnitState
type ReentryState = d.ReentryState
type ChainState = d.ChainState
type BatchRecord = d.BatchRecord
type EffectiveStrategyStop = d.EffectiveStrategyStop
