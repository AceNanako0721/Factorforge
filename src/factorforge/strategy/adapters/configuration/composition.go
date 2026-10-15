package configuration

import (
	d "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"strings"
)

// InstallCompositionRegistry loads operator-supplied definitions and templates
// from the existing private initial_registry. It supplies no calibrations or
// enablement defaults and is also usable by offline initialization checks.
func (c Config) InstallCompositionRegistry(state *d.StrategyState) error {
	fragment := d.Map(d.NewState(state.InstanceID, state.Environment))
	for _, key := range []string{"factor_series", "templates", "stop_directive_writers"} {
		if v, ok := c.InitialRegistry[key]; ok {
			fragment[key] = v
		}
	}
	raw, err := d.Marshal(fragment)
	if err != nil {
		return err
	}
	var registry d.StrategyState
	if err = d.DecodeJSON(raw, &registry); err != nil {
		return err
	}
	for _, key := range registry.FactorSeries.Keys() {
		series := registry.FactorSeries.Value(key)
		if series == nil || series.Definition.FactorID != key {
			return &d.Error{Code: "FACTOR_REGISTRY_INVALID", Status: 422}
		}
	}
	for _, key := range registry.Templates.Keys() {
		event, window, ok := strings.Cut(key, "/")
		if !ok || event == "" || window == "" || strings.Contains(window, "/") {
			return &d.Error{Code: "SCHEDULED_TEMPLATE_INVALID", Status: 422}
		}
	}
	state.FactorSeries = registry.FactorSeries
	state.Templates = registry.Templates
	state.StopDirectiveWriters = registry.StopDirectiveWriters
	return d.ValidateStopWriters(state)
}
