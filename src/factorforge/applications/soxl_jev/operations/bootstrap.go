package operations

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/ports"
	dto "github.com/AceNanako0721/Factorforge/src/factorforge/strategy/api/dto"
	dec "github.com/AceNanako0721/Factorforge/src/factorforge/trading/api/dto"
)

type BootstrapFramework interface {
	ports.FrameworkClient
	Object(context.Context, string) (*dto.ObservedObject, error)
}
type Bootstrap struct {
	Trading       ports.TradingReadClient
	Framework     BootstrapFramework
	Request       dto.CreateObject
	Stage         string
	NotionalCap   dec.Decimal
	PolicyVersion string
	FixtureOnly   bool
}

func (b Bootstrap) Run(ctx context.Context) (dto.ObservedObject, error) {
	var empty dto.ObservedObject
	object := b.Request.Object
	if b.Trading == nil || b.Framework == nil || b.Framework.ResearchOnly() || b.Framework.Binding() != (d.Binding{InstanceID: object.InstanceID, Environment: object.Environment}) || !d.ValidID(object.ObjectID) || !d.ValidID(b.PolicyVersion) {
		return empty, d.Fail("INSTANCE_BOOTSTRAP_CONFIGURATION_REQUIRED", 503)
	}
	if object.Environment != "SIM" || !d.Has([]string{"R0", "R1", "R2"}, b.Stage) {
		return empty, d.Fail("LIVE_ADMISSION_REQUIRED", 423)
	}
	cap, _ := dec.ParseDecimal("4000")
	if b.NotionalCap.Sign() <= 0 || b.NotionalCap.Cmp(cap) > 0 {
		return empty, d.Fail("INSTANCE_NOTIONAL_CAP_INVALID", 422)
	}
	if !b.FixtureOnly && (object.InstrumentKey.Venue != "BINANCE" || object.InstrumentKey.Product != "LINEAR_PERPETUAL" || object.InstrumentKey.InstrumentID != "SOXLUSDT") {
		return empty, d.Fail("INSTANCE_PRODUCT_BINDING_INVALID", 403)
	}
	snapshot, err := b.Trading.BindingSnapshot(ctx)
	if err != nil {
		return empty, err
	}
	if snapshot.Run.Environment != object.TradingRunKey.Environment || snapshot.Run.AccountID != object.TradingRunKey.AccountID || snapshot.Run.RunID != object.TradingRunKey.RunID || snapshot.PolicyVersion != b.PolicyVersion || snapshot.ExecutionMode != "SIM" {
		return empty, d.Fail("INSTANCE_TRADING_BINDING_MISMATCH", 403)
	}
	matched := false
	for _, spec := range snapshot.Instruments {
		if spec.Key.Venue == object.InstrumentKey.Venue && spec.Key.Product == object.InstrumentKey.Product && spec.Key.InstrumentID == object.InstrumentKey.InstrumentID {
			if spec.Halted || !d.ValidID(spec.Version) || spec.ContractMultiplier.Sign() <= 0 || spec.QuantityStep.Sign() <= 0 || spec.PriceTick.Sign() <= 0 {
				return empty, d.Fail("INSTANCE_INSTRUMENT_UNAVAILABLE", 423)
			}
			matched = true
		}
	}
	if !matched {
		return empty, d.Fail("INSTANCE_INSTRUMENT_NOT_RECORDED", 404)
	}
	existing, err := b.Framework.Object(ctx, object.ObjectID)
	if err != nil {
		return empty, err
	}
	if existing != nil {
		if existing.InstanceID != object.InstanceID || existing.Environment != object.Environment || existing.TradingRunKey != object.TradingRunKey || existing.InstrumentKey != object.InstrumentKey || existing.OwnerID != object.OwnerID {
			return empty, d.Fail("INSTANCE_EXISTING_BINDING_MISMATCH", 409)
		}
		return *existing, nil
	}
	version, err := b.Framework.Version(ctx, "")
	if err != nil {
		return empty, err
	}
	request := b.Request
	key := "bootstrap-" + d.Digest([]any{object.InstanceID, object.Environment, object.ObjectID, object.TradingRunKey, object.InstrumentKey, object.OwnerID, request.Policy.Version, request.Parameters.Version})
	request.Command = dto.Command{SchemaVersion: "strategy-2.0", RequestID: key, IdempotencyKey: key, ExpectedVersion: version, Reason: "INSTANCE_BOOTSTRAP_BOUND_OBJECT"}
	return b.Framework.CreateObject(ctx, request)
}
