package application

import (
	"context"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain"
	"sort"
	"strings"
)

func (s *Service) RegisterSpec(ctx context.Context, p d.Principal, command CommandInput, spec d.InstrumentSpec) (Response, error) {
	return s.Command(ctx, p, command, "SPEC_REGISTER", spec, "market:write", func(e *d.Engine, r Response) error {
		c := command.Metadata()
		run := e.Run
		if spec.ValidFrom.After(c.ExpiresAtUTC) {
			return problem("SPEC_NOT_EFFECTIVE")
		}
		if spec.ValidFrom.After(run.Clock) {
			day, err := d.RiskDay(spec.ValidFrom, run.Policy.RiskDayZone)
			if err != nil {
				return err
			}
			if day != run.RiskDay {
				run.DayStartEquity = e.Equity()
				run.DayExternalFlow = zero
				run.RiskDay = day
			}
			run.Clock = spec.ValidFrom
		}
		if spec.QuoteCurrency != spec.SettlementCurrency {
			return problem("ACCOUNTING_CAPABILITY_UNVERIFIED", 423)
		}
		e.Convert(spec.ContractMultiplier, spec.SettlementCurrency)
		for i, tier := range spec.MarginTiers {
			if tier[0].Sign() <= 0 || tier[1].Sign() <= 0 || tier[1].Cmp(one) >= 0 {
				return problem("MARGIN_TIERS_INVALID")
			}
			if i > 0 && (spec.MarginTiers[i-1][0].Cmp(tier[0]) >= 0 || spec.MarginTiers[i-1][1].Cmp(tier[1]) > 0) {
				return problem("MARGIN_TIERS_INVALID")
			}
		}
		code := spec.Key.Code()
		old := run.Specs.Value(code)
		for _, past := range run.RuleHistory {
			if past.Key == spec.Key && past.Version == spec.Version && !d.EqualModel(past, spec) {
				return problem("HISTORICAL_SPEC_VERSION_CONFLICT", 409)
			}
		}
		open := false
		for _, order := range run.Orders.Values() {
			if order.Request.InstrumentKey == spec.Key && !d.Terminal(order.State) {
				open = true
			}
		}
		if old != nil {
			position := run.Positions.Value(code)
			if old.ContractMultiplier.Cmp(spec.ContractMultiplier) != 0 && ((position != nil && position.Quantity.Sign() != 0) || open) {
				return problem("ACCOUNTING_CHANGE_REQUIRES_SETTLEMENT", 423)
			}
			if old.Version == spec.Version && !d.EqualModel(*old, spec) {
				return problem("SPEC_VERSION_CONFLICT", 409)
			}
			if old.Version != spec.Version && open {
				run.State = "RECOVERY_CHECK"
				run.RecoveryIssues = append(run.RecoveryIssues, "RULES_CHANGED_WITH_OPEN_ORDERS")
			}
			if !d.EqualModel(*old, spec) {
				run.RuleHistory = append(run.RuleHistory, *old)
			}
		}
		run.Specs.Set(code, &spec)
		r["resource_id"] = code
		r["state"] = "REGISTERED"
		return nil
	})
}
func (s *Service) MaintainProtection(ctx context.Context, p d.Principal, command CommandInput, key d.InstrumentKey, plan d.ProtectionPlan, replaceID *string) (Response, error) {
	return s.Command(ctx, p, command, "PROTECTION_SET", Response{"key": key, "plan": plan, "replace_id": replaceID}, "protection:write", func(e *d.Engine, r Response) error {
		protection := e.VerifyProtection(key, plan, replaceID)
		if e.Err() != nil {
			return e.Err()
		}
		r["resource_id"] = protection.ProtectionID
		r["state"] = protection.State
		return nil
	})
}
func (s *Service) CancelProtection(ctx context.Context, p d.Principal, command CommandInput, id string) (Response, error) {
	return s.Command(ctx, p, command, "PROTECTION_CANCEL", Response{"id": id}, "protection:write", func(e *d.Engine, r Response) error {
		run := e.Run
		protection := run.Protections.Value(id)
		if protection == nil {
			return problem("PROTECTION_NOT_FOUND", 404)
		}
		position := run.Positions.Value(protection.InstrumentKey.Code())
		alternative := false
		for _, other := range run.Protections.Values() {
			if other.ProtectionID != id && other.InstrumentKey == protection.InstrumentKey && other.State == "ACTIVE_VERIFIED" && position != nil && other.Plan.CoveredQuantity.Cmp(e.Math.Abs(position.Quantity)) >= 0 {
				alternative = true
			}
		}
		if position != nil && position.Quantity.Sign() != 0 && !alternative {
			return problem("PROTECTION_STILL_REQUIRED", 423)
		}
		protection.State = "CLOSED"
		if run.RunKey.Environment == "LIVE" {
			protection.State = "CANCEL_PENDING"
			protection.CancelRequested = true
		}
		r["resource_id"] = id
		r["state"] = protection.State
		return nil
	})
}
func (s *Service) RecordIncome(ctx context.Context, p d.Principal, command CommandInput, income d.Income) (Response, error) {
	return s.Command(ctx, p, command, "INCOME_IMPORT", income, "income:write", func(e *d.Engine, r Response) error {
		e.ApplyIncome(income)
		e.AssessLossGates()
		ApplyBreachAction(e)
		r["resource_id"] = income.ExternalID
		r["state"] = "RECORDED"
		return e.Err()
	})
}
func Reconcile(e *d.Engine) error {
	run := e.Run
	issues := []string{}
	for _, o := range run.Orders.Values() {
		if o.State == "UNKNOWN" || o.State == "DISPATCHING" {
			issues = append(issues, "UNKNOWN_ORDER")
			break
		}
	}
	for _, code := range run.Positions.Keys() {
		position := run.Positions.Value(code)
		if position.Quantity.Sign() == 0 {
			continue
		}
		covered := false
		for _, p := range run.Protections.Values() {
			if p.InstrumentKey.Code() == code && p.State == "ACTIVE_VERIFIED" && p.Plan.CoveredQuantity.Cmp(e.Math.Abs(position.Quantity)) == 0 {
				covered = true
			}
		}
		if !covered {
			issues = append(issues, "PROTECTION_NOT_VERIFIED:"+code)
		}
	}
	check := d.NewEngine(run)
	check.Equity()
	if err := check.Err(); err != nil {
		if _, ok := err.(*d.Error); !ok {
			return err
		}
		issues = append(issues, "ACCOUNT_DATA_UNKNOWN")
	}
	for _, issue := range run.RecoveryIssues {
		if strings.HasPrefix(issue, "EXTERNAL_") {
			issues = append(issues, issue)
		}
	}
	sort.Strings(issues)
	run.RecoveryIssues = unique(issues)
	run.State = "RECOVERY_CHECK"
	return e.Err()
}
func unique(values []string) []string {
	result := []string{}
	for _, v := range values {
		if len(result) == 0 || result[len(result)-1] != v {
			result = append(result, v)
		}
	}
	return result
}
func (s *Service) RunAction(ctx context.Context, p d.Principal, command CommandInput, action string) (Response, error) {
	return s.Command(ctx, p, command, "RUN_"+strings.ToUpper(action), Response{}, "run:"+action, func(e *d.Engine, r Response) error {
		c := command.Metadata()
		run := e.Run
		switch action {
		case "stop":
			run.State = "STOPPED"
			for _, order := range run.Orders.Values() {
				if !order.Request.ReduceOnly && !d.Terminal(order.State) {
					order.State = "CANCEL_PENDING"
					run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: ResourceID(run, "cmd", order.OrderID+":stop-cancel"), Kind: "CANCEL", OrderID: order.OrderID, PrincipalID: p.PrincipalID, ExpiresAt: c.ExpiresAtUTC, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
				}
			}
		case "reconcile":
			if err := Reconcile(e); err != nil {
				return err
			}
		case "resume":
			if run.RunKey.Environment == "LIVE" && (run.VenueReconciledVersion == nil || *run.VenueReconciledVersion != c.ExpectedVersion) {
				return problem("VENUE_RECOVERY_REQUIRED", 423)
			}
			if len(run.RecoveryIssues) > 0 || len(run.RiskLocks) > 0 {
				return problem("RECOVERY_NOT_VERIFIED", 423)
			}
			for _, o := range run.Orders.Values() {
				if o.State == "UNKNOWN" {
					return problem("RECOVERY_NOT_VERIFIED", 423)
				}
			}
			for _, position := range run.Positions.Values() {
				if position.Quantity.Sign() != 0 && position.ProtectionState != "ACTIVE_VERIFIED" {
					return problem("POSITION_UNPROTECTED", 423)
				}
			}
			if run.State != "RECOVERY_CHECK" {
				return problem("RECONCILE_REQUIRED", 423)
			}
			run.State = "NORMAL"
		default:
			return problem("RUN_ACTION_UNSUPPORTED")
		}
		merge(r, RunView(run))
		return nil
	})
}
func (s *Service) ImportExternal(ctx context.Context, p d.Principal, command CommandInput, fact d.ExternalFact) (Response, error) {
	return s.Command(ctx, p, command, "EXTERNAL_FACT_IMPORT", fact, "external:import", func(e *d.Engine, r Response) error {
		e.ImportExternal(fact)
		r["resource_id"] = fact.ExternalID
		r["state"] = e.Run.State
		return e.Err()
	})
}
func (s *Service) RegisterFX(ctx context.Context, p d.Principal, command CommandInput, rate d.FxRate) (Response, error) {
	return s.Command(ctx, p, command, "FX_REGISTER", rate, "market:write", func(e *d.Engine, r Response) error {
		run := e.Run
		if rate.ObservedAt.After(rate.AvailableAt) || rate.AvailableAt.After(run.Clock) {
			return problem("FX_TIME_INVALID")
		}
		if old := run.FXRates.Value(rate.Currency); old != nil && old.ObservedAt.After(rate.ObservedAt) {
			return problem("FX_OUT_OF_ORDER", 409)
		}
		run.FXRates.Set(rate.Currency, &rate)
		e.RevalueCash()
		r["resource_id"] = rate.Currency
		r["state"] = "REGISTERED"
		return e.Err()
	})
}
func (s *Service) ResolveExternal(ctx context.Context, p d.Principal, request d.ResolveExternal) (Response, error) {
	return s.Command(ctx, p, request, "EXTERNAL_OWNERSHIP_RESOLVE", request, "external:resolve", func(e *d.Engine, r Response) error {
		run := e.Run
		code := request.InstrumentKey.Code()
		if request.OwnerEpoch != run.OwnerEpochs.Value(code) || run.State != "RECOVERY_CHECK" {
			return problem("EXTERNAL_RECOVERY_VERSION_CONFLICT", 409)
		}
		for _, order := range run.Orders.Values() {
			if order.Request.InstrumentKey.Code() == code && !d.Terminal(order.State) {
				return problem("EXTERNAL_OPEN_ORDERS_UNRESOLVED", 423)
			}
		}
		position := run.Positions.Value(code)
		if position != nil && position.Quantity.Sign() != 0 && position.ProtectionState != "ACTIVE_VERIFIED" {
			return problem("POSITION_UNPROTECTED", 423)
		}
		run.Owners.Set(code, request.OwnerID)
		if position != nil {
			position.OwnerID = request.OwnerID
		}
		run.VenueReconciledVersion = nil
		issues := []string{}
		for _, issue := range run.RecoveryIssues {
			if issue != "EXTERNAL_OWNERSHIP:"+code && issue != "EXTERNAL_POSITION_DIFFERENCE:"+code {
				issues = append(issues, issue)
			}
		}
		run.RecoveryIssues = issues
		r["resource_id"] = code
		r["state"] = run.State
		return nil
	})
}
func (s *Service) FenceExecutor(ctx context.Context, p d.Principal, request d.FenceExecutor) (Response, error) {
	return s.Command(ctx, p, request, "EXECUTOR_ISOLATED", request, "executor:fence", func(e *d.Engine, r Response) error {
		if err := e.Run.IsolateExecutor(request.Epoch, request.EvidenceRef); err != nil {
			return err
		}
		r["resource_id"] = e.Run.RunKey.AccountID
		r["state"] = e.Run.State
		return nil
	})
}
func ApplyBreachAction(e *d.Engine) {
	run := e.Run
	if len(run.RiskLocks) == 0 || run.Policy.BreachAction == "KEEP_PROTECTION" {
		return
	}
	for _, target := range run.Targets.Values() {
		target.State = "BLOCKED"
		target.Reasons = []string{"ENFORCED_ACCOUNT_EXIT"}
	}
	for _, order := range run.Orders.Values() {
		if d.Terminal(order.State) || order.Request.ReduceOnly || order.State == "CANCEL_PENDING" {
			continue
		}
		order.State = "CANCEL_PENDING"
		id := "risk-cancel-" + RequestHash(Response{"order": order.OrderID, "locks": run.RiskLocks})[:24]
		run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: id, Kind: "CANCEL", OrderID: order.OrderID, PrincipalID: "risk-engine", ExpiresAt: run.Clock, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
	}
	codes := run.Positions.Keys()
	sort.Strings(codes)
	for _, code := range codes {
		p := run.Positions.Value(code)
		if p.Quantity.Sign() == 0 {
			continue
		}
		active := false
		for _, order := range run.Orders.Values() {
			if order.Request.InstrumentKey.Code() == code && !d.Terminal(order.State) {
				active = true
			}
		}
		if active {
			continue
		}
		spec := run.Specs.Value(code)
		if spec == nil || !d.IsTradable(spec, run.Clock) {
			run.Alerts = append(run.Alerts, d.Alert("EXIT_WAIT_TRADABLE", run.Clock, "instrument", code))
			continue
		}
		id := "risk-exit-" + RequestHash(Response{"run": run.RunKey, "code": code, "quantity": p.Quantity, "version": run.Version})[:24]
		side := "SELL"
		if p.Quantity.Sign() < 0 {
			side = "BUY"
		}
		request := d.OrderRequest{OwnerID: p.OwnerID, InstrumentKey: p.InstrumentKey, Side: side, OrderType: "MARKET", Quantity: e.Math.Abs(p.Quantity), ReduceOnly: true, TimeInForce: "GTC", PositionSide: "BOTH", SpecVersion: spec.Version}
		run.Orders.Set(id, &d.Order{OrderID: id, ClientOrderID: id, Request: request, State: "RESERVED", CreatedAt: run.Clock})
		run.Outbox = append(run.Outbox, d.OutboxItem{CommandID: id, Kind: "SUBMIT", OrderID: id, PrincipalID: "risk-engine", ExpiresAt: run.Clock, ExecutorEpoch: run.ExecutorEpoch, State: "PENDING"})
		run.EmergencyOrders.Set(code, id)
	}
}
