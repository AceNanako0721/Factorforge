package experiments_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type localArgument struct {
	Role   string `json:"role"`
	Tokens []int  `json:"tokens"`
}
type localEvent struct {
	Type         string          `json:"event_type"`
	Tokens       []int           `json:"tokens"`
	Negated      *bool           `json:"negated"`
	Modality     *bool           `json:"modality"`
	Participants []localArgument `json:"participants"`
	Fillers      []localArgument `json:"fillers"`
}
type localPrediction struct {
	Events []localEvent `json:"events"`
}
type localGoldArgument struct {
	Role string `json:"role"`
	Span struct {
		Tokens []int `json:"tokens"`
	} `json:"span"`
}
type localGoldEvent struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Type     string `json:"event_type"`
	Negated  *bool  `json:"negated"`
	Modality *bool  `json:"modality"`
	Span     struct {
		Tokens []int `json:"tokens"`
	} `json:"span"`
	Participants []localGoldArgument `json:"participants"`
	Fillers      []localGoldArgument `json:"fillers"`
}
type localCounts struct {
	Gold      int `json:"gold"`
	Predicted int `json:"predicted"`
	Matched   int `json:"matched"`
	Missing   int `json:"missing"`
	Extra     int `json:"extra"`
}
type localMetrics struct {
	ID             string      `json:"id"`
	Error          string      `json:"error,omitempty"`
	Triggers       localCounts `json:"triggers"`
	Arguments      localCounts `json:"arguments"`
	Attributes     localCounts `json:"attributes"`
	UnscorableGold int         `json:"unscorable_gold"`
	Strict         bool        `json:"strict_document_match"`
}

func localTokenKey(ids []int, n int) (string, error) {
	if len(ids) == 0 || len(ids) > 80 {
		return "", fmt.Errorf("SPAN_LENGTH")
	}
	parts := make([]string, len(ids))
	previous := -1
	for i, id := range ids {
		if id <= previous || id < 0 || id >= n {
			return "", fmt.Errorf("TOKEN_BOUNDS_OR_ORDER")
		}
		previous = id
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ","), nil
}

func localEventKey(kind string, ids []int, n int) (string, error) {
	span, err := localTokenKey(ids, n)
	if err != nil || kind == "" {
		return "", fmt.Errorf("EVENT_SPAN_OR_TYPE")
	}
	return kind + "|" + span, nil
}

func localValidatePrediction(prediction localPrediction, n int, types, roles map[string]int) error {
	if prediction.Events == nil || len(prediction.Events) > 30 {
		return fmt.Errorf("EVENT_LIST")
	}
	for _, event := range prediction.Events {
		if types[event.Type] == 0 || event.Negated == nil || event.Modality == nil || event.Participants == nil || event.Fillers == nil {
			return fmt.Errorf("EVENT_FIELDS")
		}
		if _, err := localEventKey(event.Type, event.Tokens, n); err != nil {
			return err
		}
		for _, args := range [][]localArgument{event.Participants, event.Fillers} {
			if len(args) > 20 {
				return fmt.Errorf("ARGUMENT_BOUND")
			}
			for _, arg := range args {
				if roles[arg.Role] == 0 {
					return fmt.Errorf("ARGUMENT_ROLE")
				}
				if _, err := localTokenKey(arg.Tokens, n); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func localMultisetCounts(gold, predicted map[string]int) localCounts {
	c := localCounts{}
	for key, n := range gold {
		c.Gold += n
		c.Matched += min(n, predicted[key])
	}
	for _, n := range predicted {
		c.Predicted += n
	}
	c.Missing = c.Gold - c.Matched
	c.Extra = c.Predicted - c.Matched
	return c
}

// Parent event/type/span and argument kind are part of every relation key.
// Duplicate annotations stay a multiset: they never collapse into a set.
func localEvaluateRow(row localCorpusRow, prediction localPrediction, problem string) (localMetrics, map[string]any) {
	metrics := localMetrics{ID: row.ID, Error: problem}
	gold := []map[string]int{{}, {}, {}}
	pred := []map[string]int{{}, {}, {}}
	for index, raw := range row.Annotations {
		var event localGoldEvent
		if json.Unmarshal(raw, &event) != nil {
			metrics.UnscorableGold++
			gold[0][fmt.Sprintf("UNSCORABLE:%d", index)]++
			continue
		}
		if event.Kind != "event" {
			continue
		}
		key, err := localEventKey(event.Type, event.Span.Tokens, len(row.Tokens))
		if err != nil {
			metrics.UnscorableGold++
			key = fmt.Sprintf("UNSCORABLE:%d", index)
		}
		gold[0][key]++
		if event.Negated == nil || event.Modality == nil {
			metrics.UnscorableGold++
			gold[2][key+"|UNKNOWN_ATTRIBUTES"]++
		} else {
			gold[2][fmt.Sprintf("%s|%t|%t", key, *event.Negated, *event.Modality)]++
		}
		for kind, args := range map[string][]localGoldArgument{"participant": event.Participants, "filler": event.Fillers} {
			for j, arg := range args {
				span, e := localTokenKey(arg.Span.Tokens, len(row.Tokens))
				if e != nil || arg.Role == "" {
					metrics.UnscorableGold++
					span = fmt.Sprintf("UNSCORABLE:%d:%d", index, j)
				}
				gold[1][key+"|"+kind+"|"+arg.Role+"|"+span]++
			}
		}
	}
	// Invalid or truncated output is rejected as a whole, not repaired by taking
	// a favourable prefix. Its complete gold denominator remains visible.
	if problem == "" {
		for _, event := range prediction.Events {
			key, _ := localEventKey(event.Type, event.Tokens, len(row.Tokens))
			pred[0][key]++
			pred[2][fmt.Sprintf("%s|%t|%t", key, *event.Negated, *event.Modality)]++
			for kind, args := range map[string][]localArgument{"participant": event.Participants, "filler": event.Fillers} {
				for _, arg := range args {
					span, _ := localTokenKey(arg.Tokens, len(row.Tokens))
					pred[1][key+"|"+kind+"|"+arg.Role+"|"+span]++
				}
			}
		}
	}
	metrics.Triggers = localMultisetCounts(gold[0], pred[0])
	metrics.Arguments = localMultisetCounts(gold[1], pred[1])
	metrics.Attributes = localMultisetCounts(gold[2], pred[2])
	metrics.Strict = problem == "" && metrics.UnscorableGold == 0 && metrics.Triggers.Missing+metrics.Triggers.Extra+metrics.Arguments.Missing+metrics.Arguments.Extra+metrics.Attributes.Missing+metrics.Attributes.Extra == 0
	return metrics, map[string]any{"gold": gold, "predicted": pred}
}

func TestEvaluateFrozenLocalExtractionBatch(t *testing.T) {
	lab := localExtractorLab(t)
	var labels struct {
		Rows []localCorpusRow `json:"rows"`
	}
	financialRead(t, lab, "labels.json", &labels)
	var schema struct {
		Events struct {
			Types map[string]int `json:"event_type_counts"`
		} `json:"events"`
		Arguments struct {
			Roles map[string]int `json:"role_counts"`
		} `json:"event_arguments"`
	}
	financialRead(t, lab, "metadata-label_schema.json", &schema)
	var seal localRequestSeal
	financialRead(t, lab, "requests-seal.json", &seal)
	if len(labels.Rows) != 4 || len(seal.InputHashes) != 4 {
		t.Fatal("LOCAL_LABEL_SEAL")
	}
	metrics := []localMetrics{}
	strict := 0
	for i, row := range labels.Rows {
		var input localDocumentInput
		financialRead(t, lab, fmt.Sprintf("input-%02d.json", i+1), &input)
		if input.ID != row.ID || input.Text != row.Text || strings.Join(input.Tokens, "\x00") != strings.Join(row.Tokens, "\x00") {
			t.Fatal("LOCAL_LABEL_ALIGNMENT")
		}
		var receipt localCallReceipt
		financialRead(t, lab, fmt.Sprintf("receipt-%02d.json", i+1), &receipt)
		raw, err := os.ReadFile(filepath.Join(lab, fmt.Sprintf("response-%02d.json", i+1)))
		if err != nil {
			t.Fatal("LOCAL_RESPONSE_REQUIRED")
		}
		if receipt.RequestHash != seal.RequestHashes[i] || receipt.ResponseHash != localRawHash(raw) {
			t.Fatal("LOCAL_DELIVERY_PROVENANCE")
		}
		var envelope struct {
			Model   string `json:"model"`
			Choices []struct {
				Finish  string `json:"finish_reason"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		problem := receipt.Error
		prediction := localPrediction{}
		if problem == "" {
			if receipt.HTTP != 200 || json.Unmarshal(raw, &envelope) != nil || len(envelope.Choices) != 1 || envelope.Model != "qwen3-4b-q4km-lab" {
				problem = "ENVELOPE_INVALID"
			} else if envelope.Choices[0].Finish != "stop" {
				problem = "OUTPUT_NOT_COMPLETE"
			} else if localStrictDecode([]byte(envelope.Choices[0].Message.Content), &prediction) != nil {
				problem = "OUTPUT_SHAPE_INVALID"
			} else if localValidatePrediction(prediction, len(row.Tokens), schema.Events.Types, schema.Arguments.Roles) != nil {
				problem = "OUTPUT_SPANS_OR_ONTOLOGY_INVALID"
			}
		}
		m, detail := localEvaluateRow(row, prediction, problem)
		metrics = append(metrics, m)
		if m.Strict {
			strict++
		}
		frozenRateWrite(t, lab, fmt.Sprintf("evaluation-detail-%02d.json", i+1), detail)
		t.Logf("%s: trigger %d/%d gold, %d predictions; argument %d/%d gold, %d predictions; %s", m.ID, m.Triggers.Matched, m.Triggers.Gold, m.Triggers.Predicted, m.Arguments.Matched, m.Arguments.Gold, m.Arguments.Predicted, m.Error)
	}
	frozenRateWrite(t, lab, "evaluation.json", map[string]any{"metrics": metrics, "strict_documents": strict, "documents": 4, "independent_expert_annotations": true, "known_training_contamination": false, "unseen_to_model_proven": false, "selected_documents_now_consumed_development": true, "full_sentiment_coreference_evaluation": false, "production_installed": false, "orders": 0})
}

func localRawHash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func TestLocalMetricsPreserveParentDiscontinuityAndMultiplicity(t *testing.T) {
	if k, e := localTokenKey([]int{0, 2, 4}, 5); e != nil || k != "0,2,4" {
		t.Fatal("discontinuous span lost")
	}
	for _, ids := range [][]int{{}, {1, 1}, {2, 1}, {-1}, {5}} {
		if _, e := localTokenKey(ids, 5); e == nil {
			t.Fatal("invalid span accepted")
		}
	}
	good := map[string]int{"Revenue|1|participant|Company|0": 2}
	wrong := map[string]int{"Profit/Loss|1|participant|Company|0": 2}
	if localMultisetCounts(good, wrong).Matched != 0 {
		t.Fatal("wrong parent accepted")
	}
	if c := localMultisetCounts(good, map[string]int{"Revenue|1|participant|Company|0": 1}); c.Matched != 1 || c.Missing != 1 {
		t.Fatal("gold duplicates collapsed")
	}
	f := false
	raw := json.RawMessage(`{"id":"synthetic-e","kind":"event","event_type":"Revenue","negated":false,"modality":false,"span":{"tokens":[1]},"participants":[{"role":"Company","span":{"tokens":[0]}}],"fillers":[]}`)
	row := localCorpusRow{ID: "synthetic", Tokens: []string{"Example", "grew"}, Annotations: []json.RawMessage{raw}}
	p := localPrediction{Events: []localEvent{{Type: "Revenue", Tokens: []int{1}, Negated: &f, Modality: &f, Participants: []localArgument{{Role: "Company", Tokens: []int{0}}}, Fillers: []localArgument{}}}}
	m, _ := localEvaluateRow(row, p, "")
	if !m.Strict || m.Arguments.Matched != 1 {
		t.Fatal("matching example rejected")
	}
	m, _ = localEvaluateRow(row, p, "OUTPUT_NOT_COMPLETE")
	if m.Strict || m.Triggers.Matched != 0 || m.Triggers.Missing != 1 || m.Arguments.Missing != 1 {
		t.Fatal("truncated output gained credit")
	}
	types := map[string]int{"Revenue": 1}
	roles := map[string]int{"Company": 1}
	if localValidatePrediction(p, 2, types, roles) != nil {
		t.Fatal("valid frame rejected")
	}
	p.Events[0].Modality = nil
	if localValidatePrediction(p, 2, types, roles) == nil {
		t.Fatal("missing modality accepted")
	}
}
