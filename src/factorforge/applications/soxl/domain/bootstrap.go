package domain

import (
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

type TradingBindingSnapshot struct {
	Run              dto.RunKey           `json:"run_key"`
	State            string               `json:"state"`
	ExecutionMode    string               `json:"execution_mode"`
	PolicyVersion    string               `json:"policy_version"`
	AggregateVersion int                  `json:"aggregate_version"`
	Instruments      []dto.InstrumentSpec `json:"instruments"`
}
