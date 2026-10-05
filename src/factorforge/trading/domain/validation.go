package domain

import (
	"reflect"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/AceNanako0721/Factorforge/src/factorforge/trading/domain/decimal"
)

// These are model constraints, not calibrated production policy. Wire presence,
// null/default handling and unknown-field admission remain the API's duty.
type fieldRule struct {
	name, kind     string
	values         []string
	gt, ge, lt, le *int64
	min, max       int64
}

var modelID = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var decimalType = reflect.TypeFor[decimal.Value]()
var timeType = reflect.TypeFor[time.Time]()

func invalidModel() error      { return &Error{Code: "MODEL_INVALID", Status: 422} }
func Validate(value any) error { return validateModel(reflect.ValueOf(value)) }
func validateModel(v reflect.Value) error {
	if !v.IsValid() {
		return invalidModel()
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return invalidModel()
		}
		return validateModel(v.Elem())
	}
	if v.Type() == decimalType || v.Type() == timeType {
		return nil
	}
	if children, ok := v.Interface().(interface{ validationChildren() []any }); ok {
		for _, child := range children.validationChildren() {
			if err := Validate(child); err != nil {
				return err
			}
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validateModel(v.Index(i)); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		for _, rule := range modelRules[v.Type().Name()] {
			field := v.FieldByName(rule.name)
			if field.Kind() == reflect.Pointer {
				if field.IsNil() {
					continue
				}
				field = field.Elem()
			}
			if rule.kind == "id" && (utf8.RuneCountInString(field.String()) < 1 || utf8.RuneCountInString(field.String()) > 128 || !modelID.MatchString(field.String())) {
				return invalidModel()
			}
			if rule.kind == "enum" && !Has(rule.values, field.String()) {
				return invalidModel()
			}
			if rule.kind == "str" {
				n := int64(utf8.RuneCountInString(field.String()))
				if (rule.min > 0 && n < rule.min) || (rule.max > 0 && n > rule.max) {
					return invalidModel()
				}
			}
			if rule.kind == "number" {
				number := zero
				if field.Type() == decimalType {
					number = field.Interface().(decimal.Value)
				} else {
					number, _ = decimal.Parse(formatInteger(field.Int()))
				}
				for _, bound := range []struct {
					limit *int64
					kind  string
				}{{rule.gt, "gt"}, {rule.ge, "ge"}, {rule.lt, "lt"}, {rule.le, "le"}} {
					if bound.limit == nil {
						continue
					}
					limit, _ := decimal.Parse(formatInteger(*bound.limit))
					cmp := number.Cmp(limit)
					if (bound.kind == "gt" && cmp <= 0) || (bound.kind == "ge" && cmp < 0) || (bound.kind == "lt" && cmp >= 0) || (bound.kind == "le" && cmp > 0) {
						return invalidModel()
					}
				}
			}
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanInterface() {
				continue
			}
			if field.Kind() == reflect.Pointer && field.IsNil() {
				continue
			}
			if err := validateModel(field); err != nil {
				return err
			}
		}
		return validateBusiness(v.Interface())
	}
	return nil
}
func validateBusiness(value any) error {
	switch v := value.(type) {
	case ExternalFact:
		for _, id := range v.ExternalFillIDs {
			if len(id) < 1 || len(id) > 128 || !modelID.MatchString(id) {
				return invalidModel()
			}
		}
	case Session:
		if !v.ClosesAt.After(v.OpensAt) {
			return invalidModel()
		}
	case MarketPoint:
		if v.AvailableAt.Before(v.ObservedAt) || v.AvailableAt.Before(v.ReceivedAt) {
			return invalidModel()
		}
	case MarketTrade:
		if v.AvailableAt.Before(v.ObservedAt) || v.AvailableAt.Before(v.ReceivedAt) {
			return invalidModel()
		}
	case Candle:
		if v.High.Cmp(decimal.Max(v.Open, v.Close, v.Low)) < 0 || v.Low.Cmp(decimal.Min(v.Open, v.Close)) > 0 || !v.CloseAt.After(v.OpenAt) || (v.Final && v.AvailableAt.Before(v.CloseAt)) {
			return invalidModel()
		}
	case LossGate:
		if v.Amount == nil && v.Fraction == nil && v.Count == nil {
			return invalidModel()
		}
	case AccountPolicy:
		if _, err := time.LoadLocation(v.RiskDayZone); err != nil {
			return invalidModel()
		}
		for _, gate := range []LossGate{v.DailyLoss, v.Drawdown} {
			if gate.Count != nil || (gate.Amount == nil && gate.Fraction == nil) {
				return invalidModel()
			}
		}
		if v.ConsecutiveLoss.Count == nil || v.ConsecutiveLoss.Amount != nil || v.ConsecutiveLoss.Fraction != nil {
			return invalidModel()
		}
		for _, limit := range v.GroupNotionalLimits.Values() {
			if limit.Sign() <= 0 {
				return invalidModel()
			}
		}
	case SimConfig:
		if v.Leverage.Cmp(one) > 0 && v.LiquidationFeeRate == nil {
			return invalidModel()
		}
	case OrderRequest:
		if (v.OrderType == "LIMIT" && v.LimitPrice == nil) || (v.OrderType == "MARKET" && v.LimitPrice != nil) {
			return invalidModel()
		}
	}
	return nil
}
