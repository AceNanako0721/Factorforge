package strategy_test

import (
	config "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/adapters/configuration"
	sd "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/domain"
	"testing"
)

func TestCompositionInitialRegistryHasNoInferredCalibration(t *testing.T) {
	s, _, _, _, _ := compositionBase(t)
	c := config.Config{InitialRegistry: map[string]any{"factor_series": sd.Map(s.FactorSeries), "templates": map[string]any{"earnings/post": sd.Map(templateFixture())}}}
	fresh := sd.NewState(s.InstanceID, s.Environment)
	if err := c.InstallCompositionRegistry(fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.FactorSeries.Len() != 1 || fresh.Templates.Len() != 1 {
		t.Fatal("composition registry ignored")
	}
	c.InitialRegistry = map[string]any{}
	empty := sd.NewState(s.InstanceID, s.Environment)
	if err := c.InstallCompositionRegistry(empty); err != nil {
		t.Fatal(err)
	}
	if empty.FactorSeries.Len() != 0 || empty.Templates.Len() != 0 {
		t.Fatal("defaults invented")
	}
	c.InitialRegistry = map[string]any{"factor_series": map[string]any{"wrong-key": sd.Map(s.FactorSeries.Value("daily"))}}
	assertStrategyCode(t, c.InstallCompositionRegistry(empty), "FACTOR_REGISTRY_INVALID")
	c.InitialRegistry = map[string]any{"templates": map[string]any{"invalid-key": sd.Map(templateFixture())}}
	assertStrategyCode(t, c.InstallCompositionRegistry(empty), "SCHEDULED_TEMPLATE_INVALID")
	c.InitialRegistry = map[string]any{"factor_series": "invalid"}
	assertStrategyCode(t, c.InstallCompositionRegistry(empty), "INVALID_REQUEST")
}
