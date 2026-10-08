package application

import (
	"context"
	"encoding/json"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/ports"
	"reflect"
	"sync"
)

type SelectionRegistry struct {
	mu      sync.RWMutex
	entries map[string]d.Selection
}

func NewRegistry(xs []d.Selection) (*SelectionRegistry, error) {
	r := &SelectionRegistry{entries: map[string]d.Selection{}}
	for _, s := range xs {
		if !s.Valid() || r.entries[s.ID].ID != "" {
			return nil, d.Fail("SELECTION_CONFIGURATION_REQUIRED", 503)
		}
		r.entries[s.ID] = s
	}
	return r, nil
}
func (r *SelectionRegistry) List(u d.User, environment string) []d.Selection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	xs := []d.Selection{}
	for _, id := range u.SelectionIDs {
		if s, ok := r.entries[id]; ok && (environment == "" || environment == s.Environment) {
			xs = append(xs, s)
		}
	}
	return xs
}
func (r *SelectionRegistry) Get(u d.User, id string) (d.Selection, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.entries[id]
	if !ok || !d.Has(u.SelectionIDs, id) {
		return d.Selection{}, d.Fail("SELECTION_FORBIDDEN", 403)
	}
	return s, nil
}
func (r *SelectionRegistry) Revoke(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
}

// Validate independently checks lower bindings. A disconnected P3 never hides
// a verified P1 panel. A mismatch is a hard error, rather than a soft empty panel.
func Validate(ctx context.Context, c ports.ReadClient, s d.Selection, service string, maxPages int) error {
	read := func(resource string) (map[string]any, error) {
		raw, e := c.Get(ctx, s, resource, "", nil)
		if e != nil {
			return nil, e
		}
		return d.Unpack(raw)
	}
	same := func(v any, expected any) bool {
		a, _ := json.Marshal(v)
		b, _ := json.Marshal(expected)
		var x, y any
		json.Unmarshal(a, &x)
		json.Unmarshal(b, &y)
		return reflect.DeepEqual(x, y)
	}
	switch service {
	case "trading":
		v, e := read("trading.health")
		if e != nil {
			return e
		}
		if v["schema_version"] != "trading-2.0" || v["environment"] != s.Environment || v["upper_layers_required"] != false {
			return d.Fail("BINDING_MISMATCH", 409)
		}
		run, e := read("run")
		if e != nil {
			return e
		}
		if !same(run["run_key"], s.TradingRunKey) {
			return d.Fail("BINDING_MISMATCH", 409)
		}
		spec, e := read("instruments")
		if e != nil {
			return e
		}
		items, ok := spec["items"].([]any)
		if !ok {
			return d.Fail("LOWER_RESPONSE_INVALID", 503)
		}
		found := false
		for _, x := range items {
			m, ok := x.(map[string]any)
			if ok && same(m["key"], s.InstrumentKey) {
				found = true
			}
		}
		if !found {
			return d.Fail("BINDING_MISMATCH", 409)
		}
	case "strategy":
		if s.ObjectID == "" {
			return d.Fail("READ_NOT_APPLICABLE", 422)
		}
		health, e := read("strategy.health")
		if e != nil {
			return e
		}
		if health["schema_version"] != "strategy-2.0" || health["environment"] != s.Environment || health["listener"] != "PUBLIC_RESEARCH" {
			return d.Fail("BINDING_MISMATCH", 409)
		}
		cursor := ""
		found := false
		for page := 0; page < maxPages; page++ {
			filter := map[string]string{}
			if cursor != "" {
				filter["cursor"] = cursor
			}
			raw, e := c.Get(ctx, s, "objects", "", filter)
			if e != nil {
				return e
			}
			dir, e := d.Unpack(raw)
			if e != nil {
				return e
			}
			// Published P2 ReadPage has no environment/instance envelope; the
			// authenticated listener and existing object's own DTO verify both.
			rows, ok := dir["items"].([]any)
			if !ok {
				return d.Fail("LOWER_RESPONSE_INVALID", 503)
			}
			for _, x := range rows {
				row, ok := x.(map[string]any)
				if ok && row["object_id"] == s.ObjectID {
					found = true
				}
			}
			if found {
				break
			}
			next, _ := dir["cursor"].(string)
			if next == "" {
				break
			}
			if next == cursor {
				return d.Fail("LOWER_RESPONSE_INVALID", 503)
			}
			cursor = next
		}
		if !found {
			return d.Fail("BINDING_MISMATCH", 409)
		}
		v, e := read("object")
		if e != nil {
			return e
		}
		if v["object_id"] != s.ObjectID || v["instance_id"] != s.InstanceID || v["environment"] != s.Environment || v["owner_id"] != s.OwnerID || !same(v["trading_run_key"], s.TradingRunKey) || !same(v["instrument_key"], s.InstrumentKey) {
			return d.Fail("BINDING_MISMATCH", 409)
		}
	case "instances":
		v, e := read("instance.health")
		if e != nil {
			return e
		}
		if v["schema_version"] != "instance-2.0" || v["environment"] != s.Environment || v["instance_id"] != s.InstanceID {
			return d.Fail("BINDING_MISMATCH", 409)
		}
	default:
		return d.Fail("READ_ROUTE_FORBIDDEN", 403)
	}
	return nil
}
