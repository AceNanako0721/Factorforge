package binance

import (
	"context"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"strings"
)

func (b *Broker) ListOpenConditionals(ctx context.Context, run *d.Aggregate, key d.InstrumentKey) ([]d.ConditionalOrderFact, error) {
	raw, err := b.Transport.Request(ctx, "GET", "/fapi/v1/openAlgoOrders", Params{"symbol": key.InstrumentID}, false)
	if err != nil {
		return nil, err
	}
	rows, err := array(raw)
	if err != nil {
		return nil, err
	}
	// The venue endpoint returns the open set without pagination. A bounded result
	// is required; refuse truncation rather than claim an empty/complete set.
	if len(rows) > 1000 {
		return nil, reject("VENUE_CONDITIONAL_LIST_LIMIT", 423)
	}
	result := []d.ConditionalOrderFact{}
	for _, row := range rows {
		v, err := object(row)
		if err != nil {
			return nil, err
		}
		if text(v["symbol"]) != key.InstrumentID {
			return nil, reject("VENUE_CONDITIONAL_FORMAT_INVALID", 503)
		}
		f := d.ConditionalOrderFact{ExternalID: text(v["algoId"]), Instrument: key, Side: text(v["side"]), OrderType: text(v["orderType"]), ReduceOnly: text(v["reduceOnly"]) == "true", PositionSide: text(v["positionSide"]), State: text(v["algoStatus"]), ObservedAt: b.Transport.Clock()}
		if f.ExternalID == "" {
			return nil, reject("VENUE_CONDITIONAL_FORMAT_INVALID", 503)
		}
		if c := text(v["clientAlgoId"]); c != "" {
			f.ClientID = &c
		}
		if kind := text(v["workingType"]); kind == "MARK_PRICE" {
			k := "MARK"
			f.TriggerKind = &k
		} else if kind == "CONTRACT_PRICE" {
			k := "LAST"
			f.TriggerKind = &k
		}
		if q, e := parseAmount(v["quantity"]); e == nil {
			f.Quantity = &q
		}
		if p, e := parseAmount(v["triggerPrice"]); e == nil {
			f.TriggerPrice = &p
		}
		for _, p := range run.Protections.Values() {
			if p.InstrumentKey == key && f.ClientID != nil && *f.ClientID == d.ConditionalClientID(run.RunKey, p) {
				id := p.ProtectionID
				f.LocalProtectionID = &id
				if p.Role != "" {
					r := p.Role
					f.Role = &r
				}
			}
		}
		if d.OwnConditional(run, f) && f.Role == nil && f.ClientID != nil {
			r := ""
			if strings.HasPrefix(*f.ClientID, "ff-i-") {
				r = "INSURANCE"
			}
			if strings.HasPrefix(*f.ClientID, "ff-s-") {
				r = "STRATEGY"
			}
			if r != "" {
				f.Role = &r
			}
		}
		result = append(result, f)
	}
	return result, nil
}
func (b *Broker) CancelConditionalByID(ctx context.Context, run *d.Aggregate, key d.InstrumentKey, id string) error {
	if err := b.requireWrite(); err != nil {
		return err
	}
	// Only a previously persisted, run-owned discovery may cross the write port.
	task := run.ConditionalCleanups.Value(key.Code())
	owned := false
	if task != nil {
		for _, f := range task.Facts {
			if f.ExternalID == id && f.Instrument == key && d.OwnConditional(run, f) {
				owned = true
			}
		}
	}
	if !owned {
		return reject("CONDITIONAL_OWNERSHIP_REQUIRED", 403)
	}
	_, err := b.Transport.Request(ctx, "DELETE", Conditional, Params{"algoId": id}, true)
	return err
}
