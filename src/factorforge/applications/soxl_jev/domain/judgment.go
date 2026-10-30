package domain

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
)

// JudgmentOutput is the strict model answer (S3-024). The model never sees a
// price; item identifiers must come from the presented input.
type JudgmentOutput struct {
	Direction           string   `json:"direction"`
	Confidence          int      `json:"confidence"`
	HorizonHours        int      `json:"horizon_hours"`
	Thesis              string   `json:"thesis"`
	KeyItemIDs          []string `json:"key_item_ids"`
	CounterPoints       string   `json:"counter_points"`
	Invalidation        string   `json:"invalidation"`
	PreviousInvalidated bool     `json:"previous_invalidated"`
	InvalidationItemIDs []string `json:"invalidation_item_ids"`
}

// Judgment is the recorded result of one run, successful or not.
type Judgment struct {
	JudgmentID string            `json:"judgment_id"`
	At         time.Time         `json:"at"`
	OK         bool              `json:"ok"`
	Reason     string            `json:"reason,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Model      string            `json:"model,omitempty"`
	PromptHash string            `json:"prompt_hash,omitempty"`
	InputHash  string            `json:"input_hash"`
	InputIDs   []string          `json:"input_ids"`
	Output     *JudgmentOutput   `json:"output,omitempty"`
	Errors     []string          `json:"errors,omitempty"`
	Raw        string            `json:"raw,omitempty"`
	Usage      map[string]string `json:"usage,omitempty"`
}

// ShortID is the identifier presented to the model.
func ShortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

var jsonObject = regexp.MustCompile(`(?s)\{.*\}`)

// ParseJudgment extracts and validates one JSON answer. Short identifiers are
// mapped back to full item identifiers; an unknown identifier fails the run.
func ParseJudgment(text string, inputIDs []string) (JudgmentOutput, error) {
	var out JudgmentOutput
	m := jsonObject.FindString(text)
	if m == "" || json.Unmarshal([]byte(m), &out) != nil {
		return out, Fail("JUDGMENT_PARSE_FAILED", 422)
	}
	full := map[string]string{}
	for _, id := range inputIDs {
		full[ShortID(id)] = id
		full[id] = id
	}
	resolve := func(ids []string) ([]string, bool) {
		r := []string{}
		for _, v := range ids {
			id, ok := full[strings.TrimSpace(strings.Trim(v, "[]"))]
			if !ok {
				return nil, false
			}
			if !Has(r, id) {
				r = append(r, id)
			}
		}
		sort.Strings(r)
		return r, true
	}
	var ok bool
	if out.KeyItemIDs, ok = resolve(out.KeyItemIDs); !ok {
		return out, Fail("JUDGMENT_UNKNOWN_ITEM", 422)
	}
	if out.InvalidationItemIDs, ok = resolve(out.InvalidationItemIDs); !ok {
		return out, Fail("JUDGMENT_UNKNOWN_ITEM", 422)
	}
	directional := out.Direction == "LONG" || out.Direction == "SHORT"
	if !directional && out.Direction != "FLAT" || out.Confidence < 1 || out.Confidence > 5 || out.HorizonHours < 1 || out.HorizonHours > 720 || strings.TrimSpace(out.Thesis) == "" {
		return out, Fail("JUDGMENT_OUT_OF_RANGE", 422)
	}
	if directional && (len(out.KeyItemIDs) == 0 || strings.TrimSpace(out.Invalidation) == "") {
		return out, Fail("JUDGMENT_INCOMPLETE", 422)
	}
	if out.PreviousInvalidated && len(out.InvalidationItemIDs) == 0 {
		return out, Fail("JUDGMENT_INCOMPLETE", 422)
	}
	return out, nil
}

// DirectionSign maps LONG/SHORT/FLAT to 1/-1/0.
func DirectionSign(direction string) int {
	switch direction {
	case "LONG":
		return 1
	case "SHORT":
		return -1
	}
	return 0
}

// ActiveJudgment is the direction currently held in the sentiment ledger.
type ActiveJudgment struct {
	Direction  string    `json:"direction"`
	Confidence int       `json:"confidence"`
	EventIDs   []string  `json:"event_ids"`
	ChainID    string    `json:"chain_id"`
	Since      time.Time `json:"since"`
}

// InjectionState is the deterministic memory of S3-025 /.
type InjectionState struct {
	Seen          []string                 `json:"seen"`
	Active        *ActiveJudgment          `json:"active,omitempty"`
	LastDirection string                   `json:"last_direction,omitempty"`
	Sequence      int                      `json:"sequence"`
	Events        map[string]InjectedEvent `json:"events,omitempty"`
	LastJudgment  string                   `json:"last_judgment,omitempty"`
}

// InjectedEvent remembers what was written so revisions and retractions
// reuse the identical claims and the latest score version.
type InjectedEvent struct {
	Event        json.RawMessage `json:"event"`
	ScoreID      string          `json:"score_id"`
	ScoreVersion int             `json:"score_version"`
	HorizonHours int             `json:"horizon_hours"`
	Retracted    bool            `json:"retracted,omitempty"`
}

// InjectionAction is one framework write derived from a judgment.
type InjectionAction struct {
	Kind         string   `json:"kind"` // INJECT, INVALIDATE, REVISE
	Direction    string   `json:"direction,omitempty"`
	Confidence   int      `json:"confidence,omitempty"`
	HorizonHours int      `json:"horizon_hours,omitempty"`
	EventID      string   `json:"event_id,omitempty"`
	ItemIDs      []string `json:"item_ids,omitempty"`
	Targets      []string `json:"targets,omitempty"`
	Reason       string   `json:"reason"`
	ChainID      string   `json:"chain_id,omitempty"`
}

// DecideInjection returns the next state and actions. The caller persists the
// new state only after every action was accepted, so a failed write retries.
func DecideInjection(state InjectionState, j Judgment) (InjectionState, []InjectionAction) {
	next := InjectionState{Seen: append([]string(nil), state.Seen...), LastDirection: state.LastDirection, Sequence: state.Sequence, Events: map[string]InjectedEvent{}, LastJudgment: j.JudgmentID}
	for k, v := range state.Events {
		next.Events[k] = v
	}
	if state.Active != nil {
		copy := *state.Active
		copy.EventIDs = append([]string(nil), state.Active.EventIDs...)
		next.Active = &copy
	}
	if !j.OK || j.Output == nil {
		return next, nil
	}
	o := *j.Output
	newIDs := []string{}
	for _, id := range o.KeyItemIDs {
		if !Has(state.Seen, id) {
			newIDs = append(newIDs, id)
		}
	}
	finish := func() {
		for _, id := range o.KeyItemIDs {
			if !Has(next.Seen, id) {
				next.Seen = append(next.Seen, id)
			}
		}
		sort.Strings(next.Seen)
		next.LastDirection = o.Direction
	}
	actions := []InjectionAction{}
	inject := func(reason string, chain string) {
		next.Sequence++
		ids := newIDs
		if len(ids) == 0 {
			ids = o.KeyItemIDs
		}
		event := "judgment-" + j.JudgmentID + "-" + itoa(next.Sequence)
		if chain == "" {
			chain = "chain-" + j.JudgmentID
		}
		actions = append(actions, InjectionAction{Kind: "INJECT", Direction: o.Direction, Confidence: o.Confidence, HorizonHours: o.HorizonHours, EventID: event, ItemIDs: ids, Reason: reason, ChainID: chain})
		if next.Active == nil || next.Active.Direction != o.Direction {
			next.Active = &ActiveJudgment{Direction: o.Direction, Confidence: o.Confidence, ChainID: chain, Since: j.At}
		}
		next.Active.EventIDs = append(next.Active.EventIDs, event)
		if o.Confidence > next.Active.Confidence {
			next.Active.Confidence = o.Confidence
		}
	}
	invalidate := func(reason string) {
		if next.Active != nil && len(next.Active.EventIDs) > 0 {
			actions = append(actions, InjectionAction{Kind: "INVALIDATE", Targets: append([]string(nil), next.Active.EventIDs...), Direction: next.Active.Direction, Reason: reason, ChainID: next.Active.ChainID})
		}
		next.Active = nil
	}
	directional := DirectionSign(o.Direction) != 0
	confirmed := state.LastDirection == o.Direction
	if next.Active != nil && o.PreviousInvalidated && len(o.InvalidationItemIDs) > 0 {
		invalidate("INVALIDATED")
	}
	switch {
	case !directional:
		// FLAT is not an opposite view; existing contributions keep decaying.
	case next.Active == nil:
		// First appearance injects at once; after an invalidation in this run a
		// new direction still needs two consecutive judgments.
		if state.Active == nil {
			inject("FIRST_DIRECTION", "")
		} else if state.Active.Direction != o.Direction && confirmed {
			inject("DIRECTION_FLIP", "")
		}
	case next.Active.Direction != o.Direction:
		if confirmed {
			invalidate("DIRECTION_FLIP")
			inject("DIRECTION_FLIP", "")
		}
	case len(newIDs) > 0 && o.Confidence >= next.Active.Confidence:
		inject("NEW_ITEMS", next.Active.ChainID)
	case o.Confidence <= next.Active.Confidence-2:
		actions = append(actions, InjectionAction{Kind: "REVISE", Direction: o.Direction, Confidence: o.Confidence, Targets: append([]string(nil), next.Active.EventIDs...), Reason: "CONFIDENCE_DOWN", ChainID: next.Active.ChainID})
		next.Active.Confidence = o.Confidence
	}
	finish()
	return next, actions
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	s := ""
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}

// SourcedJudgment is one judgment read from another instance's read API in
// follower mode (S3-034) with the news items it cites. Items carry
// only what the read API publishes (no summary or link); ReceivedAt is the
// judgment time, the latest moment the source can have held them.
type SourcedJudgment struct {
	Judgment Judgment
	Items    []NewsItem
}
