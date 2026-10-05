package main

import (
	"encoding/json"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/adapters/binance"
	a "github.com/AceNanako0721/Factorforge/src/factorforge/trading/application"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
	r "github.com/AceNanako0721/Factorforge/src/factorforge/trading/entrypoints/runtime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *acceptance) submit(market bool) (string, error) {
	quotes, err := s.refresh()
	if err != nil {
		return "", err
	}
	math := decimal.NewMath(28)
	price := math.Step(math.Mul(quotes["BID"], amount("0.99")), s.spec.PriceTick, decimal.Floor)
	entry := price
	if market {
		entry = quotes["ASK"]
	}
	if quotes["MARK"].Cmp(entry) < 0 {
		entry = quotes["MARK"]
	}
	stop := math.Step(math.Mul(entry, amount("0.995")), s.spec.PriceTick, decimal.Floor)
	if math.Err() != nil {
		return "", failure("TESTNET_ARITHMETIC_FAILED")
	}
	plan := &d.ProtectionPlan{TriggerKind: "MARK", TriggerPrice: stop, CoveredQuantity: s.quantity, ExitOrderType: "MARKET", MaxSlippageBps: amount("10"), SpecVersion: s.spec.Version}
	request := d.OrderRequest{OwnerID: "p1-owner", InstrumentKey: s.spec.Key, Side: "BUY", Quantity: s.quantity, OrderType: "LIMIT", LimitPrice: &price, TimeInForce: "POST_ONLY", PositionSide: "BOTH", SpecVersion: s.spec.Version, ProtectionPlan: plan}
	if market {
		request.OrderType = "MARKET"
		request.LimitPrice = nil
		request.TimeInForce = "GTC"
	}
	command, err := s.command()
	if err != nil {
		return "", err
	}
	reply, err := s.http.Call(s.ctx, "POST", apiPrefix+"/orders", nil, d.SubmitOrder{Command: command, Order: request})
	if err != nil {
		return "", err
	}
	var result struct {
		ResourceID string `json:"resource_id"`
	}
	if json.Unmarshal(reply, &result) != nil || result.ResourceID == "" {
		return "", failure("TESTNET_API_SUBMIT_FAILED")
	}
	_, err = s.executor.Tick(s.ctx, s.key)
	return result.ResourceID, err
}
func (s *acceptance) cancel(id string) error {
	command, err := s.command()
	if err != nil {
		return err
	}
	if _, err = s.service.CancelOrder(s.ctx, s.principal, command, id); err != nil {
		return err
	}
	for range 5 {
		if _, err = s.executor.Tick(s.ctx, s.key); err != nil {
			return err
		}
		run, err := s.store.Read(s.ctx, s.key)
		if err != nil {
			return err
		}
		if item := run.Orders.Value(id); item != nil && item.State == "CANCELED" {
			return nil
		}
	}
	return failure("TESTNET_CANCEL_UNCONFIRMED")
}
func (s *acceptance) ordinary() error {
	id, err := s.submit(false)
	if err != nil {
		return err
	}
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	if err = s.check("ordinary_submit_acknowledged", run.Orders.Value(id).State == "ACKNOWLEDGED"); err != nil {
		return err
	}
	if err = s.cancel(id); err != nil {
		return err
	}
	if err = s.check("ordinary_cancel_confirmed", true); err != nil {
		return err
	}
	before, err := s.stats()
	if err != nil {
		return err
	}
	if _, err = s.broker.Call(s.ctx, "drop_response"); err != nil {
		return err
	}
	id, err = s.submit(false)
	if err != nil {
		return err
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	if err = s.check("actual_response_loss_persisted_unknown", run.Orders.Value(id).State == "UNKNOWN"); err != nil {
		return err
	}
	if _, err = s.executor.Tick(s.ctx, s.key); err != nil {
		return err
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	after, err := s.stats()
	if err != nil {
		return err
	}
	previous, _ := strconv.Atoi(str(before["write_attempts"]))
	current, _ := strconv.Atoi(str(after["write_attempts"]))
	if err = s.check("original_id_query_no_duplicate_post", run.Orders.Value(id).State == "ACKNOWLEDGED" && current == previous+1); err != nil {
		return err
	}
	if err = s.cancel(id); err != nil {
		return err
	}
	return s.reconcile(true)
}
func (s *acceptance) openProtected() (*d.Protection, error) {
	id, err := s.submit(true)
	if err != nil {
		return nil, err
	}
	for range 12 {
		if _, err = s.refresh(); err != nil {
			return nil, err
		}
		if _, err = a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.05"), false); err != nil {
			return nil, err
		}
		if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
			return nil, err
		}
		run, err := s.store.Read(s.ctx, s.key)
		if err != nil {
			return nil, err
		}
		position := run.Positions.Value(s.spec.Key.Code())
		if run.Orders.Value(id).State == "FILLED" && position != nil && position.ProtectionState == "ACTIVE_VERIFIED" {
			items := run.Protections.Values()
			for i := len(items) - 1; i >= 0; i-- {
				if items[i].State == "ACTIVE_VERIFIED" {
					return items[i], nil
				}
			}
		}
		if !r.Pause(s.ctx, 300*time.Millisecond) {
			return nil, s.ctx.Err()
		}
	}
	return nil, failure("TESTNET_PHYSICAL_PROTECTION_UNCONFIRMED")
}
func (s *acceptance) replace(old *d.Protection, trigger decimal.Value) (string, error) {
	command, err := s.command()
	if err != nil {
		return "", err
	}
	plan := old.Plan
	plan.TriggerPrice = trigger
	reply, err := s.service.MaintainProtection(s.ctx, s.principal, command, s.spec.Key, plan, &old.ProtectionID)
	if err != nil {
		return "", err
	}
	id, ok := reply["resource_id"].(string)
	if !ok {
		return "", failure("TESTNET_RESPONSE_INVALID")
	}
	return id, nil
}
func (s *acceptance) protectiveExit() error {
	old, err := s.openProtected()
	if err != nil {
		return err
	}
	if err = s.check("actual_market_fill_and_physical_stop", true); err != nil {
		return err
	}
	quotes, err := s.refresh()
	if err != nil {
		return err
	}
	math := decimal.NewMath(28)
	trigger := math.Step(math.Mul(quotes["MARK"], amount("0.9975")), s.spec.PriceTick, decimal.Floor)
	id, err := s.replace(old, trigger)
	if err != nil {
		return err
	}
	var current *d.Aggregate
	for range 8 {
		if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
			return err
		}
		current, err = s.store.Read(s.ctx, s.key)
		if err != nil {
			return err
		}
		if current.Protections.Value(id).State == "ACTIVE_VERIFIED" && current.Protections.Value(old.ProtectionID).State == "CLOSED" {
			break
		}
		if !r.Pause(s.ctx, 200*time.Millisecond) {
			return s.ctx.Err()
		}
	}
	stats, err := s.stats()
	if err != nil {
		return err
	}
	s.report["overlap_probe"] = map[string]any{"new": current.Protections.Value(id).State, "old": current.Protections.Value(old.ProtectionID).State, "transport": stats}
	if err = s.check("physical_overlap_replace", current.Protections.Value(id).State == "ACTIVE_VERIFIED" && current.Protections.Value(old.ProtectionID).State == "CLOSED"); err != nil {
		return err
	}
	active := current.Protections.Value(id)
	for range 6 {
		quotes, err = s.refresh()
		if err != nil {
			return err
		}
		trigger = math.Sub(math.Step(quotes["MARK"], s.spec.PriceTick, decimal.Floor), math.Mul(amount("2"), s.spec.PriceTick))
		if trigger.Cmp(active.Plan.TriggerPrice) <= 0 {
			break
		}
		id, err = s.replace(active, trigger)
		if err != nil {
			return err
		}
		if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
			return err
		}
		current, err = s.store.Read(s.ctx, s.key)
		if err != nil {
			return err
		}
		item := current.Protections.Value(id)
		if item.State == "ACTIVE_VERIFIED" || item.State != "CLOSED" {
			break
		}
	}
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if _, err = s.refresh(); err != nil {
			return err
		}
		issues, err := a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.05"), false)
		if err != nil {
			return err
		}
		for range 3 {
			if !externalIssues(issues) {
				break
			}
			if !r.Pause(s.ctx, 500*time.Millisecond) {
				return s.ctx.Err()
			}
			issues, err = a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.05"), true)
			if err != nil {
				return err
			}
		}
		if externalIssues(issues) {
			return failure("TESTNET_EXTERNAL_TRADING_INTERFERENCE")
		}
		if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
			return err
		}
		current, err = s.store.Read(s.ctx, s.key)
		if err != nil {
			return err
		}
		if position := current.Positions.Value(s.spec.Key.Code()); position != nil && position.Quantity.Sign() == 0 {
			exit := false
			for _, order := range current.Orders.Values() {
				exit = exit || (order.SourceProtectionID != nil && order.State == "FILLED")
			}
			if err = s.check("exchange_stop_trigger_and_exit_fill", exit); err != nil {
				return err
			}
			if err = s.reconcile(false); err != nil {
				return err
			}
			current, err = s.store.Read(s.ctx, s.key)
			if err != nil {
				return err
			}
			account, err := s.broker.GetAccount(s.ctx, current)
			if err != nil {
				return err
			}
			cash, err := parse(account["cash"])
			if err != nil {
				return err
			}
			return s.check("fill_fee_pnl_wallet_reconciliation", math.Sub(current.Cash, cash).Abs().Cmp(amount("0.0000001")) <= 0)
		}
		if !r.Pause(s.ctx, time.Second) {
			return s.ctx.Err()
		}
	}
	return failure("TESTNET_STOP_TRIGGER_TIMEOUT")
}
func externalIssues(issues []string) bool {
	for _, issue := range issues {
		if strings.HasPrefix(issue, "EXTERNAL_FILL_UNALLOCATED:") {
			return true
		}
	}
	return false
}
func (s *acceptance) retireSigner() (int, error) {
	s.stopAPI()
	pid := s.broker.PID()
	s.gateway.Revoke(s.token)
	if _, err := raw[map[string]any](s.ctx, s.broker, "/fapi/v1/accountConfig", binance.Params{}); err == nil {
		return pid, failure("OLD_EGRESS_STILL_ACCESSIBLE")
	}
	if err := s.check("old_executor_egress_revoked", true); err != nil {
		return pid, err
	}
	s.broker.Close()
	s.broker = nil
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return pid, s.check("old_executor_process_exited", os.IsNotExist(err))
}
func (s *acceptance) replaceSigner(pid int) error {
	command, err := s.command()
	if err != nil {
		return err
	}
	if _, err = s.service.FenceExecutor(s.ctx, s.principal, d.FenceExecutor{Command: command, Epoch: s.epoch, EvidenceRef: "actual revoked gateway and confirmed process exit pid=" + strconv.Itoa(pid)}); err != nil {
		return err
	}
	s.broker, s.token, s.manifest, err = s.newSigner(false)
	if err != nil {
		return err
	}
	if err = s.bindWorkers(); err != nil {
		return err
	}
	return s.startAPI()
}
func (s *acceptance) isolationDrill() error {
	epoch := s.epoch
	if _, err := raw[map[string]any](s.ctx, s.broker, "/fapi/v1/accountConfig", binance.Params{}); err != nil {
		return err
	}
	pid, err := s.retireSigner()
	if err != nil {
		return err
	}
	if err = s.replaceSigner(pid); err != nil {
		return err
	}
	if err = s.reconcile(false); err != nil {
		return err
	}
	return s.check("replacement_executor_new_epoch", s.epoch > epoch)
}
func (s *acceptance) recoveryDrill() error {
	if err := s.reconcile(true); err != nil {
		return err
	}
	if _, err := s.openProtected(); err != nil {
		return err
	}
	before, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	pid, err := s.retireSigner()
	if err != nil {
		return err
	}
	manual, token, manifest, err := s.newSigner(true)
	if err != nil {
		return err
	}
	var fills []map[string]any
	var account map[string]any
	var id string
	err = func() error {
		defer manual.Close()
		defer s.gateway.Revoke(token)
		data, err := manual.Call(s.ctx, "manual_reduce", binance.Params{"symbol": s.symbol, "side": "SELL", "type": "MARKET", "positionSide": "BOTH", "quantity": s.quantity.String(), "reduceOnly": "true", "newClientOrderId": manifest.ManualID})
		if err != nil {
			return err
		}
		var order map[string]json.RawMessage
		if json.Unmarshal(data, &order) != nil {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		if json.Unmarshal(order["orderId"], &id) != nil {
			id = string(order["orderId"])
		}
		if id == "" {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		proven := false
		for range 12 {
			receipts, err := raw[[]map[string]any](s.ctx, manual, "/fapi/v1/userTrades", binance.Params{"symbol": s.symbol, "orderId": id})
			if err != nil {
				return err
			}
			fills = nil
			for _, fill := range receipts {
				if str(fill["orderId"]) == id {
					fills = append(fills, fill)
				}
			}
			account, err = raw[map[string]any](s.ctx, manual, "/fapi/v3/account", binance.Params{})
			if err != nil {
				return err
			}
			positions, err := raw[[]map[string]any](s.ctx, manual, "/fapi/v3/positionRisk", binance.Params{})
			if err != nil {
				return err
			}
			isFlat, err := flat(positions)
			if err != nil {
				return err
			}
			proven = len(fills) > 0 && isFlat
			if proven {
				break
			}
			if !r.Pause(s.ctx, 500*time.Millisecond) {
				return s.ctx.Err()
			}
		}
		if err = s.check("official_rest_manual_emergency_exit", proven); err != nil {
			return err
		}
		s.report["manual_receipt"] = map[string]any{"order_id": id, "fills": fills}
		return nil
	}()
	if err != nil {
		return err
	}
	if err = s.replaceSigner(pid); err != nil {
		return err
	}
	if _, err = s.refresh(); err != nil {
		return err
	}
	issues, err := a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.0000001"), true)
	if err != nil {
		return err
	}
	if err = s.check("restart_detects_external_receipts", externalIssues(issues)); err != nil {
		return err
	}
	command, err := s.command()
	if err != nil {
		return err
	}
	if _, err = s.service.RunAction(s.ctx, s.principal, command, "resume"); err == nil {
		return failure("PREMATURE_RESUME_ACCEPTED")
	}
	if err = s.check("premature_resume_rejected", true); err != nil {
		return err
	}
	current, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	timestamp := int64(1<<63 - 1)
	fillIDs := []string{}
	for _, fill := range fills {
		n, err := strconv.ParseInt(str(fill["time"]), 10, 64)
		if err != nil {
			return failure("TESTNET_RESPONSE_INVALID")
		}
		if n < timestamp {
			timestamp = n
		}
		fillIDs = append(fillIDs, str(fill["id"]))
	}
	cash, err := parse(account["totalWalletBalance"])
	if err != nil {
		return err
	}
	math := decimal.NewMath(28)
	fact := d.ExternalFact{ExternalID: "manual-" + id, Kind: "MANUAL", InstrumentKey: s.spec.Key, HappenedAt: time.UnixMilli(timestamp).UTC(), ReceivedAt: current.Clock, BeforeQuantity: before.Positions.Value(s.spec.Key.Code()).Quantity, AfterQuantity: amount("0"), CashDelta: math.Sub(cash, before.Cash), Currency: "USDT", RuleVersion: s.spec.Version, EvidenceRef: "binance-userTrades:" + id, ExternalFillIDs: fillIDs}
	command, err = s.command()
	if err != nil {
		return err
	}
	if _, err = s.service.ImportExternal(s.ctx, s.principal, command, fact); err != nil {
		return err
	}
	current, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	if err = s.check("external_fact_increments_owner_epoch", current.OwnerEpochs.Value(s.spec.Key.Code()) > before.OwnerEpochs.Value(s.spec.Key.Code())); err != nil {
		return err
	}
	outstanding, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v1/openAlgoOrders", binance.Params{})
	if err != nil {
		return err
	}
	for _, item := range outstanding {
		identity := str(item["clientAlgoId"])
		if current.Protections.Value(identity) == nil {
			return failure("TESTNET_EXTERNAL_TRADING_INTERFERENCE")
		}
		if item["algoStatus"] == "NEW" {
			command, err = s.command()
			if err != nil {
				return err
			}
			if _, err = s.service.CancelProtection(s.ctx, s.principal, command, identity); err != nil {
				return err
			}
			if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
				return err
			}
		}
	}
	command, err = s.command()
	if err != nil {
		return err
	}
	current, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	if _, err = s.service.ResolveExternal(s.ctx, s.principal, d.ResolveExternal{Command: command, InstrumentKey: s.spec.Key, OwnerID: "p1-owner", OwnerEpoch: current.OwnerEpochs.Value(s.spec.Key.Code()), EvidenceRef: "binance-userTrades:" + id}); err != nil {
		return err
	}
	if err = s.reconcile(true); err != nil {
		return err
	}
	if err = s.check("external_fact_recovery_without_duplicate_cash", true); err != nil {
		return err
	}
	return s.check("replacement_executor_new_epoch", s.epoch > before.LeaseEpoch)
}
func (s *acceptance) storageDrill() error {
	if _, err := s.refresh(); err != nil {
		return err
	}
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	before, err := s.stats()
	if err != nil {
		return err
	}
	if err = s.adminExec("CREATE FUNCTION trading_live.fail_acceptance_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'acceptance audit unavailable'; END; $$; CREATE TRIGGER fail_acceptance BEFORE INSERT ON trading_live.audit_event FOR EACH ROW EXECUTE FUNCTION trading_live.fail_acceptance_audit()"); err != nil {
		return err
	}
	err = func() error {
		defer s.adminExec("DROP TRIGGER fail_acceptance ON trading_live.audit_event")
		command, err := s.command()
		if err != nil {
			return err
		}
		_, err = s.service.RunAction(s.ctx, s.principal, command, "stop")
		if stableCode(err) != "STORE_UNAVAILABLE" {
			return failure("AUDIT_FAILURE_ACCEPTED")
		}
		current, readErr := s.store.Read(s.ctx, s.key)
		if readErr != nil {
			return readErr
		}
		return s.check("real_audit_storage_failure_rolls_back", current.Version == run.Version)
	}()
	if err != nil {
		return err
	}
	current, err := s.stats()
	if err != nil {
		return err
	}
	if err = s.check("audit_failure_no_outbound_write", str(before["write_attempts"]) == str(current["write_attempts"])); err != nil {
		return err
	}
	if err = s.adminExec("ALTER ROLE ff_test_live NOLOGIN"); err != nil {
		return err
	}
	err = func() error {
		defer s.adminExec("ALTER ROLE ff_test_live LOGIN")
		_, err := s.store.Read(s.ctx, s.key)
		if err = s.check("real_database_access_failure", stableCode(err) == "STORE_UNAVAILABLE"); err != nil {
			return err
		}
		if len(run.Orders.Values()) > 0 {
			_, _, err = s.broker.SubmitOrder(s.ctx, run, run.Orders.Values()[0])
		} else {
			_, err = s.broker.Call(s.ctx, "fence_probe")
		}
		if err = s.check("database_failure_stops_signer", stableCode(err) == "STORE_UNAVAILABLE"); err != nil {
			return err
		}
		current, err := s.stats()
		if err != nil {
			return err
		}
		return s.check("database_failure_no_outbound_write", str(before["write_attempts"]) == str(current["write_attempts"]))
	}()
	if err != nil {
		return err
	}
	return s.reconcile(false)
}
func (s *acceptance) cleanup() error {
	// An unproven position difference leaves physical coverage intact. No ledger
	// edits, account-wide cancellations or guessed exit quantities are allowed.
	defer func() {
		s.stopAPI()
		if s.broker != nil {
			s.broker.Close()
			s.broker = nil
		}
		if s.gateway != nil {
			s.gateway.Close()
		}
		if s.store != nil {
			s.store.Close()
		}
		if s.server != nil {
			s.server.Close()
		}
		if s.market != nil {
			s.market.Client.CloseIdleConnections()
		}
		s.log.Close()
	}()
	if s.broker == nil || s.store == nil || s.spec == nil {
		return failure("TESTNET_CLEANUP_NOT_PROVEN")
	}
	run, err := s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	defer func() {
		if snapshot, err := s.store.Read(s.ctx, s.key); err == nil {
			privateJSON(filepath.Join(s.directory, "owned-snapshot.json"), snapshot)
		}
	}()
	positions, err := s.broker.GetPositions(s.ctx, run)
	if err != nil {
		return err
	}
	for _, position := range positions {
		if position.InstrumentKey != s.spec.Key || position.Quantity.Sign() == 0 {
			continue
		}
		if !s.authorized {
			return failure("TESTNET_CLEANUP_REQUIRES_ORDER_AUTHORIZATION")
		}
		if _, err = s.refresh(); err != nil {
			return err
		}
		side := "BUY"
		if position.Quantity.Sign() > 0 {
			side = "SELL"
		}
		request := d.OrderRequest{OwnerID: "p1-owner", InstrumentKey: s.spec.Key, Side: side, OrderType: "MARKET", Quantity: position.Quantity.Abs(), ReduceOnly: true, TimeInForce: "GTC", PositionSide: "BOTH", SpecVersion: s.spec.Version}
		command, err := s.command()
		if err != nil {
			return err
		}
		result, err := s.service.SubmitOrder(s.ctx, s.principal, command, request, nil)
		if err != nil {
			return err
		}
		identity := result["resource_id"].(string)
		for range 4 {
			if _, err = s.executor.Tick(s.ctx, s.key); err != nil {
				return err
			}
			run, err = s.store.Read(s.ctx, s.key)
			if err != nil {
				return err
			}
			if run.Orders.Value(identity).State == "FILLED" {
				break
			}
		}
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	for _, order := range run.Orders.Values() {
		if !d.Terminal(order.State) {
			if err = s.cancel(order.OrderID); err != nil {
				return err
			}
		}
	}
	if _, err = s.refresh(); err != nil {
		return err
	}
	if _, err = a.Synchronize(s.ctx, s.store, s.key, s.broker, amount("0.0000001"), true); err != nil {
		return err
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	for _, item := range run.Protections.Values() {
		if item.State != "CLOSED" {
			command, err := s.command()
			if err != nil {
				return err
			}
			if _, err = s.service.CancelProtection(s.ctx, s.principal, command, item.ProtectionID); err != nil {
				return err
			}
			if _, err = s.protection.Tick(s.ctx, s.key); err != nil {
				return err
			}
		}
	}
	ordinary, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v1/openOrders", binance.Params{})
	if err != nil {
		return err
	}
	conditional, err := raw[[]map[string]any](s.ctx, s.broker, "/fapi/v1/openAlgoOrders", binance.Params{})
	if err != nil {
		return err
	}
	run, err = s.store.Read(s.ctx, s.key)
	if err != nil {
		return err
	}
	positions, err = s.broker.GetPositions(s.ctx, run)
	if err != nil {
		return err
	}
	isFlat := true
	for _, p := range positions {
		isFlat = isFlat && p.Quantity.Sign() == 0
	}
	return s.check("final_flat_no_ordinary_or_conditional_orders", len(ordinary) == 0 && len(conditional) == 0 && isFlat)
}
