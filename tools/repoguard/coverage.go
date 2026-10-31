package repoguard

// Coverage registers the configuration types explained by each design book.
var Coverage = []FieldCoverage{
	{Design: "01_交易系统设计书.md", File: "src/factorforge/trading/domain/models.go", Types: []string{"AccountPolicy", "LossGate", "StressScenario", "OperationalPolicy", "SimConfig", "ProtectionPlan", "InstrumentSpec"}, Tag: "json"},
	{Design: "01_交易系统设计书.md", File: "src/factorforge/trading/domain/readiness.go", Types: []string{"Readiness"}, Tag: "json"},
	{Design: "01_交易系统设计书.md", File: "src/factorforge/trading/adapters/configuration/config.go", Types: []string{"Config"}, Tag: "toml"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/records.go", Types: []string{"Policy", "ParameterSnapshot"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/composition.go", Types: []string{"BaselineFactor", "AssessmentFactor", "LayerCap", "AdjustmentRule", "RebalanceRule", "Composition", "FactorDefinition"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/numerical_types.go", Types: []string{"Level"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/dynamic_stop.go", Types: []string{"SizingRule", "ReentryRule", "ChainLossRule", "PreEventRule", "TransferRule"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/scheduled.go", Types: []string{"ScheduledTemplate", "TemplatePrior"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/stop_directive.go", Types: []string{"StopDirectiveWriterBinding"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/domain/time_intervals.go", Types: []string{"TimeWindowPlan", "PolicyWindow"}, Tag: "json"},
	{Design: "02_策略化框架设计书.md", File: "src/factorforge/strategy/adapters/configuration/config.go", Types: []string{"Config"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/config/dynamic_stop.go", Types: []string{"DynamicProfile"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/monitoring/news_feeds.go", Types: []string{"NewsFeed"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/config/read_api.go", Types: []string{"ReadAPI"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/config/calendar.go", Types: []string{"CalendarConfig"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/config/calendar.go", Types: []string{"ScheduleRegistration", "CalendarAssets"}, Tag: "json"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/config/storage_admin.go", Types: []string{"StorageAdmin"}, Tag: "toml"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/domain/source.go", Types: []string{"SourceRegistration"}, Tag: "json"},
	{Design: "03_SOXLUSDT应用实例设计书.md", File: "src/factorforge/applications/soxl/operations/calendar.go", Types: []string{"Calendar", "MarketSession"}, Tag: "json"},
	{Design: "04_Web管理台设计书.md", File: "src/factorforge/operations/console/config/config.go", Types: []string{"Profile", "Binding", "LiveMarket"}, Tag: "toml"},
	{Design: "04_Web管理台设计书.md", File: "src/factorforge/operations/console/domain/models.go", Types: []string{"User"}, Tag: "toml"},
	{Design: "04_Web管理台设计书.md", File: "src/factorforge/operations/console/adapters/http_read.go", Types: []string{"Service", "Services"}, Tag: "toml"},
}
