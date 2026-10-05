package binance

import (
	"context"
	"errors"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/ports"
	"strconv"
	"time"
)

type Broker struct {
	Transport Transport
	Verified  map[string]any
	Admitted  bool
}

func (b *Broker) Environment() string     { return "LIVE" }
func (b *Broker) ExecutionAdmitted() bool { return b.Admitted }
func (b *Broker) Capabilities() map[string]any {
	result := map[string]any{"environment": "LIVE", "live_ready": false, "overlapping_protections": false, "atomic_protection_modify": false}
	for k, v := range b.Verified {
		result[k] = v
	}
	return result
}
func (b *Broker) requireWrite() error {
	if b.Verified["ordinary_and_conditional_verified"] != true {
		return reject("LIVE_CAPABILITIES_UNVERIFIED", 423)
	}
	return nil
}
func (b *Broker) decode(ctx context.Context, run *d.Aggregate, order *d.Order, payload any) (*d.Order, []d.Fill, error) {
	raw, err := object(payload)
	if err != nil {
		return nil, nil, &ports.Ambiguous{}
	}
	if text(raw["symbol"]) != order.Request.InstrumentKey.InstrumentID || text(raw["clientOrderId"]) != order.ClientOrderID {
		return nil, nil, &ports.Ambiguous{}
	}
	state := map[string]string{"NEW": "ACKNOWLEDGED", "PARTIALLY_FILLED": "PARTIALLY_FILLED", "FILLED": "FILLED", "CANCELED": "CANCELED", "EXPIRED": "CANCELED", "EXPIRED_IN_MATCH": "CANCELED", "REJECTED": "REJECTED"}[text(raw["status"])]
	id := text(raw["orderId"])
	if state == "" || id == "" {
		return nil, nil, &ports.Ambiguous{}
	}
	quantity := raw["executedQty"]
	if quantity == nil {
		quantity = "0"
	}
	filled, err := parseAmount(quantity)
	if err != nil || filled.Sign() < 0 {
		return nil, nil, &ports.Ambiguous{}
	}
	copy := *order
	copy.ExternalOrderID = &id
	copy.State = state
	copy.FilledQuantity = filled
	fills, err := b.fills(ctx, run, order.Request.InstrumentKey, &id)
	if err != nil {
		return nil, nil, err
	}
	values := make([]d.Fill, len(fills))
	for i, f := range fills {
		values[i] = *f
	}
	return &copy, values, nil
}
func (b *Broker) SubmitOrder(ctx context.Context, run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if err := b.requireWrite(); err != nil {
		return nil, nil, err
	}
	raw, err := b.Transport.Request(ctx, "POST", Ordinary, OrdinaryRequest(order), true)
	if err != nil {
		var problem *d.Error
		if errors.As(err, &problem) && problem.Code == "VENUE_REQUEST_REJECTED" {
			copy := *order
			copy.State = "REJECTED"
			return &copy, []d.Fill{}, nil
		}
		return nil, nil, err
	}
	return b.decode(ctx, run, order, raw)
}
func (b *Broker) QueryOrder(ctx context.Context, run *d.Aggregate, id string) (*d.Order, []d.Fill, error) {
	var order *d.Order
	for _, o := range run.Orders.Values() {
		if o.ClientOrderID == id {
			order = o
			break
		}
	}
	if order == nil {
		return nil, nil, reject("ORDER_NOT_FOUND", 404)
	}
	params := Params{"symbol": order.Request.InstrumentKey.InstrumentID, "origClientOrderId": id}
	if order.SourceProtectionID != nil {
		if order.ExternalOrderID == nil {
			return nil, nil, &ports.Ambiguous{}
		}
		delete(params, "origClientOrderId")
		params["orderId"] = *order.ExternalOrderID
	}
	raw, err := b.Transport.Request(ctx, "GET", Ordinary, params, false)
	if err != nil {
		return nil, nil, err
	}
	if order.SourceProtectionID != nil {
		value, err := object(raw)
		if err != nil || text(value["orderId"]) != *order.ExternalOrderID || text(value["symbol"]) != order.Request.InstrumentKey.InstrumentID {
			return nil, nil, &ports.Ambiguous{}
		}
		value["clientOrderId"] = id
	}
	return b.decode(ctx, run, order, raw)
}
func (b *Broker) CancelOrder(ctx context.Context, run *d.Aggregate, order *d.Order) (*d.Order, []d.Fill, error) {
	if err := b.requireWrite(); err != nil {
		return nil, nil, err
	}
	raw, err := b.Transport.Request(ctx, "DELETE", Ordinary, Params{"symbol": order.Request.InstrumentKey.InstrumentID, "origClientOrderId": order.ClientOrderID}, true)
	if err != nil {
		return nil, nil, err
	}
	return b.decode(ctx, run, order, raw)
}
func (b *Broker) fills(ctx context.Context, run *d.Aggregate, key d.InstrumentKey, id *string) ([]*d.Fill, error) {
	rows := []*d.Fill{}
	now := b.Transport.Clock()
	start := run.VenueFactsCursorAt
	if start == nil {
		start = run.FactsStartAt
	}
	type window struct{ start, end *time.Time }
	windows := []window{{nil, nil}}
	if id == nil && start != nil {
		if now.Sub(*start) > 90*24*time.Hour {
			return nil, reject("VENUE_HISTORY_ARCHIVE_REQUIRED", 423)
		}
		windows = nil
		opening := *start
		for !opening.After(now) {
			closing := opening.Add(7*24*time.Hour - time.Millisecond)
			if closing.After(now) {
				closing = now
			}
			a, z := opening, closing
			windows = append(windows, window{&a, &z})
			opening = closing.Add(time.Millisecond)
		}
	}
	for _, w := range windows {
		params := Params{"symbol": key.InstrumentID, "limit": "1000"}
		if id != nil {
			params["orderId"] = *id
		}
		if w.start != nil {
			params["startTime"] = strconv.FormatInt(w.start.UnixMilli(), 10)
			params["endTime"] = strconv.FormatInt(w.end.UnixMilli(), 10)
		}
		cursor := int64(-1)
		for {
			raw, err := b.Transport.Request(ctx, "GET", "/fapi/v1/userTrades", params, false)
			if err != nil {
				return nil, err
			}
			page, err := array(raw)
			if err != nil {
				return nil, reject("VENUE_FILL_FORMAT_INVALID", 503)
			}
			beyond := false
			for _, value := range page {
				item, err := object(value)
				if err != nil {
					return nil, err
				}
				if id != nil && text(item["orderId"]) != *id {
					continue
				}
				at, err := stamp(item["time"])
				if err != nil {
					return nil, err
				}
				if w.end != nil && at.After(*w.end) {
					beyond = true
					continue
				}
				if (w.start != nil && at.Before(*w.start)) || (run.FactsStartAt != nil && at.Before(*run.FactsStartAt)) {
					continue
				}
				quantity, e1 := parseAmount(item["qty"])
				price, e2 := parseAmount(item["price"])
				fee, e3 := parseAmount(item["commission"])
				received := now
				if at.After(received) {
					received = at
				}
				fill := &d.Fill{ExternalFillID: text(item["id"]), ExternalOrderID: text(item["orderId"]), InstrumentKey: key, Side: text(item["side"]), Quantity: quantity, Price: price, Fee: fee, FeeCurrency: text(item["commissionAsset"]), HappenedAt: at, ReceivedAt: received}
				if e1 != nil || e2 != nil || e3 != nil || d.Validate(fill) != nil {
					return nil, reject("VENUE_FILL_FORMAT_INVALID", 503)
				}
				rows = append(rows, fill)
			}
			if len(page) < 1000 || beyond {
				break
			}
			last, err := object(page[len(page)-1])
			if err != nil {
				return nil, err
			}
			next, err := strconv.ParseInt(text(last["id"]), 10, 64)
			if err != nil || next == int64(^uint64(0)>>1) || next+1 <= cursor {
				return nil, reject("VENUE_PAGINATION_CONFLICT", 503)
			}
			cursor = next + 1
			delete(params, "startTime")
			delete(params, "endTime")
			params["fromId"] = strconv.FormatInt(cursor, 10)
		}
	}
	return rows, nil
}
func (b *Broker) ListFills(ctx context.Context, run *d.Aggregate) ([]*d.Fill, error) {
	rows := []*d.Fill{}
	for _, spec := range run.Specs.Values() {
		fills, err := b.fills(ctx, run, spec.Key, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, fills...)
	}
	return rows, nil
}
func (b *Broker) ListOpenOrders(ctx context.Context, run *d.Aggregate) ([]*d.Order, error) {
	raw, err := b.Transport.Request(ctx, "GET", "/fapi/v1/openOrders", Params{}, false)
	if err != nil {
		return nil, err
	}
	page, err := array(raw)
	if err != nil {
		return nil, err
	}
	result := []*d.Order{}
	for _, value := range page {
		item, err := object(value)
		if err != nil {
			return nil, err
		}
		var local *d.Order
		for _, o := range run.Orders.Values() {
			if o.ClientOrderID == text(item["clientOrderId"]) {
				local = o
				break
			}
		}
		if local == nil {
			for _, o := range run.Orders.Values() {
				if o.SourceProtectionID != nil && o.ExternalOrderID != nil && *o.ExternalOrderID == text(item["orderId"]) && o.Request.InstrumentKey.InstrumentID == text(item["symbol"]) {
					local = o
					break
				}
			}
		}
		if local == nil {
			exit := false
			for _, p := range run.Protections.Values() {
				exit = exit || (p.ExitOrderID != nil && *p.ExitOrderID == text(item["orderId"]) && p.InstrumentKey.InstrumentID == text(item["symbol"]))
			}
			if exit {
				continue
			}
			return nil, reject("EXTERNAL_OPEN_ORDER_UNALLOCATED", 423)
		}
		if local.SourceProtectionID != nil {
			item["clientOrderId"] = local.ClientOrderID
		}
		order, _, err := b.decode(ctx, run, local, item)
		if err != nil {
			return nil, err
		}
		result = append(result, order)
	}
	return result, nil
}
func (b *Broker) GetPositions(ctx context.Context, run *d.Aggregate) ([]*d.Position, error) {
	raw, err := b.Transport.Request(ctx, "GET", "/fapi/v3/positionRisk", Params{}, false)
	if err != nil {
		return nil, err
	}
	page, err := array(raw)
	if err != nil {
		return nil, err
	}
	rows := []*d.Position{}
	for _, value := range page {
		item, err := object(value)
		if err != nil {
			return nil, err
		}
		if text(item["positionSide"]) != "BOTH" {
			return nil, reject("HEDGE_MODE_UNVERIFIED", 423)
		}
		key := d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: text(item["symbol"])}
		amount, err := parseAmount(item["positionAmt"])
		if err != nil {
			return nil, reject("VENUE_POSITION_FORMAT_INVALID", 503)
		}
		owner, ok := run.Owners.Get(key.Code())
		if !ok {
			owner = "UNALLOCATED"
		}
		position := &d.Position{InstrumentKey: key, OwnerID: owner, Quantity: amount, ProtectionState: "CLOSED"}
		if amount.Sign() != 0 {
			entry, err := parseAmount(item["entryPrice"])
			if err != nil {
				return nil, reject("VENUE_POSITION_FORMAT_INVALID", 503)
			}
			position.AverageEntry = &entry
			position.ProtectionState = "UNKNOWN"
		}
		if d.Validate(position) != nil {
			return nil, reject("VENUE_POSITION_FORMAT_INVALID", 503)
		}
		rows = append(rows, position)
	}
	return rows, nil
}
func RequireAccountConfiguration(ctx context.Context, t Transport) error {
	raw, err := t.Request(ctx, "GET", "/fapi/v1/accountConfig", Params{}, false)
	if err != nil {
		return err
	}
	item, err := object(raw)
	if err != nil || item["canTrade"] != true {
		return reject("VENUE_ACCOUNT_CANNOT_TRADE", 423)
	}
	if item["dualSidePosition"] != false || item["multiAssetsMargin"] != false {
		return reject("TARGET_ACCOUNT_MODE_UNVERIFIED", 423)
	}
	return nil
}
func (b *Broker) GetAccount(ctx context.Context, run *d.Aggregate) (map[string]any, error) {
	raw, err := b.Transport.Request(ctx, "GET", "/fapi/v3/account", Params{}, false)
	if err != nil {
		return nil, err
	}
	if err = RequireAccountConfiguration(ctx, b.Transport); err != nil {
		return nil, err
	}
	item, err := object(raw)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"observed_at": b.Transport.Clock()}
	for field, key := range map[string]string{"equity": "totalMarginBalance", "available_margin": "availableBalance", "cash": "totalWalletBalance"} {
		amount, err := parseAmount(item[key])
		if err != nil {
			return nil, reject("VENUE_ACCOUNT_FORMAT_INVALID", 503)
		}
		result[field] = amount
	}
	return result, nil
}
func (b *Broker) GetIncome(ctx context.Context, run *d.Aggregate) ([]*d.Income, error) {
	kinds := map[string]string{"FUNDING_FEE": "FUNDING", "TRANSFER": "TRANSFER", "INSURANCE_CLEAR": "SETTLEMENT", "DELIVERED_SETTELMENT": "SETTLEMENT"}
	start := run.Clock
	if run.FactsStartAt != nil {
		start = *run.FactsStartAt
	} else {
		for _, o := range run.Orders.Values() {
			if o.CreatedAt.Before(start) {
				start = o.CreatedAt
			}
		}
	}
	if run.VenueFactsCursorAt != nil {
		start = *run.VenueFactsCursorAt
	}
	rows := []*d.Income{}
	for page := 1; ; page++ {
		raw, err := b.Transport.Request(ctx, "GET", "/fapi/v1/income", Params{"startTime": strconv.FormatInt(start.UnixMilli(), 10), "endTime": strconv.FormatInt(b.Transport.Clock().UnixMilli(), 10), "page": strconv.Itoa(page), "limit": "1000"}, false)
		if err != nil {
			return nil, err
		}
		items, err := array(raw)
		if err != nil {
			return nil, err
		}
		for _, value := range items {
			item, err := object(value)
			if err != nil {
				return nil, err
			}
			kind := text(item["incomeType"])
			if kind == "COMMISSION" || kind == "REALIZED_PNL" {
				continue
			}
			mapped, ok := kinds[kind]
			if !ok {
				return nil, reject("VENUE_INCOME_TYPE_UNVERIFIED", 423)
			}
			amount, e1 := parseAmount(item["income"])
			at, e2 := stamp(item["time"])
			evidence := "binance-income:" + text(item["tranId"])
			income := &d.Income{ExternalID: kind + "-" + text(item["tranId"]), Kind: mapped, Amount: amount, Currency: text(item["asset"]), HappenedAt: at, EvidenceRef: &evidence}
			if symbol := text(item["symbol"]); symbol != "" {
				income.InstrumentKey = &d.InstrumentKey{Venue: "BINANCE", Product: "LINEAR_PERPETUAL", InstrumentID: symbol}
			}
			if e1 != nil || e2 != nil || d.Validate(income) != nil {
				return nil, reject("VENUE_INCOME_FORMAT_INVALID", 503)
			}
			rows = append(rows, income)
		}
		if len(items) < 1000 {
			break
		}
	}
	return rows, nil
}
func (b *Broker) findProtection(run *d.Aggregate, id string) (*d.Protection, error) {
	if p := run.Protections.Value(id); p != nil {
		return p, nil
	}
	var found *d.Protection
	for _, p := range run.Protections.Values() {
		if p.ExternalID != nil && *p.ExternalID == id {
			if found != nil {
				return nil, reject("PROTECTION_NOT_FOUND_OR_AMBIGUOUS", 404)
			}
			found = p
		}
	}
	if found == nil {
		return nil, reject("PROTECTION_NOT_FOUND_OR_AMBIGUOUS", 404)
	}
	return found, nil
}
func (b *Broker) SubmitProtection(ctx context.Context, run *d.Aggregate, p *d.Protection) (*d.Protection, error) {
	if err := b.requireWrite(); err != nil {
		return nil, err
	}
	position := run.Positions.Value(p.InstrumentKey.Code())
	if position == nil || position.Quantity.Sign() == 0 {
		return nil, reject("PROTECTION_POSITION_REQUIRED", 423)
	}
	side := "BUY"
	if position.Quantity.Sign() > 0 {
		side = "SELL"
	}
	params, err := ProtectionRequest(p.InstrumentKey, p.Plan, side, p.ProtectionID)
	if err != nil {
		return nil, err
	}
	raw, err := b.Transport.Request(ctx, "POST", Conditional, params, true)
	if err != nil {
		return nil, err
	}
	item, err := object(raw)
	if err != nil || text(item["algoId"]) == "" {
		return nil, &ports.Ambiguous{}
	}
	copy := *p
	id := text(item["algoId"])
	copy.ExternalID = &id
	copy.State = "PENDING"
	return &copy, nil
}
func (b *Broker) QueryProtection(ctx context.Context, run *d.Aggregate, id string) (*d.Protection, error) {
	p, err := b.findProtection(run, id)
	if err != nil {
		return nil, err
	}
	raw, err := b.Transport.Request(ctx, "GET", Conditional, Params{"clientAlgoId": p.ProtectionID}, false)
	if err != nil {
		return nil, err
	}
	item, err := object(raw)
	if err != nil {
		return nil, &ports.Ambiguous{}
	}
	working := "CONTRACT_PRICE"
	if p.Plan.TriggerKind == "MARK" {
		working = "MARK_PRICE"
	}
	quantity, e1 := parseAmount(item["quantity"])
	trigger, e2 := parseAmount(item["triggerPrice"])
	external := text(item["algoId"])
	position := run.Positions.Value(p.InstrumentKey.Code())
	sideOkay := true
	if position != nil && position.Quantity.Sign() != 0 {
		side := "BUY"
		if position.Quantity.Sign() > 0 {
			side = "SELL"
		}
		sideOkay = text(item["side"]) == side
	}
	if text(item["clientAlgoId"]) != p.ProtectionID || text(item["symbol"]) != p.InstrumentKey.InstrumentID || text(item["algoType"]) != "CONDITIONAL" || text(item["orderType"]) != "STOP_MARKET" || text(item["positionSide"]) != "BOTH" || text(item["workingType"]) != working || !sideOkay || text(item["reduceOnly"]) != "true" || e1 != nil || e2 != nil || quantity.Cmp(p.Plan.CoveredQuantity) != 0 || trigger.Cmp(p.Plan.TriggerPrice) != 0 || external == "" || (p.ExternalID != nil && *p.ExternalID != external) {
		return nil, &ports.Ambiguous{}
	}
	copy := *p
	copy.ExternalID = &external
	copy.State = map[string]string{"NEW": "ACTIVE_VERIFIED", "TRIGGERED": "TRIGGERED", "FINISHED": "CLOSED", "CANCELED": "CLOSED", "REJECTED": "CLOSED", "EXPIRED": "CLOSED"}[text(item["algoStatus"])]
	if copy.State == "" {
		copy.State = "UNKNOWN"
	}
	copy.ExitOrderID = nil
	if exit := text(item["actualOrderId"]); exit != "" && exit != "0" {
		copy.ExitOrderID = &exit
	}
	at := b.Transport.Clock()
	copy.VerifiedAt = &at
	return &copy, nil
}
func (b *Broker) CancelProtection(ctx context.Context, run *d.Aggregate, id string) (*d.Protection, error) {
	if err := b.requireWrite(); err != nil {
		return nil, err
	}
	p, err := b.findProtection(run, id)
	if err != nil {
		return nil, err
	}
	if _, err = b.Transport.Request(ctx, "DELETE", Conditional, Params{"clientAlgoId": p.ProtectionID}, true); err != nil {
		return nil, err
	}
	copy := *p
	copy.State = "CANCEL_PENDING"
	return &copy, nil
}
