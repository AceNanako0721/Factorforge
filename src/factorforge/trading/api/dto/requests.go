package dto

import d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"

type CreateRun = d.CreateRun
type SubmitOrder = d.SubmitOrder
type RegisterSpec = d.RegisterSpec
type SetProtection = d.SetProtection
type RecordIncome = d.RecordIncome
type ReplayFrame = d.ReplayFrame
type AdvanceReplay = d.AdvanceReplay
type TargetRequest = d.TargetRequest
type ImportExternal = d.ImportExternal
type RegisterFX = d.RegisterFX
type ResolveExternal = d.ResolveExternal
type FenceExecutor = d.FenceExecutor
type MarketSnapshot = d.MarketSnapshot

// Read-only value bindings for higher HTTP clients; no execution or store API.
type RunKey = d.RunKey
type InstrumentKey = d.InstrumentKey
type InstrumentSpec = d.InstrumentSpec
