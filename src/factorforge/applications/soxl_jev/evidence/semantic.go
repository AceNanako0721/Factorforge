package evidence

import (
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"strings"
	"unicode/utf8"
)

// SemanticEvent deliberately has no verified fact, time, weight or score fields.
type SemanticEvent struct {
	ParagraphID string `json:"paragraph_id"`
	Quote       string `json:"quote"`
	Occurrence  *int   `json:"occurrence"`
	SubjectID   string `json:"subject_id"`
	ItemID      string `json:"item_id"`
	Assertion   string `json:"assertion"`
	Relation    string `json:"relation"`
}
type SemanticCandidate struct {
	SemanticEvent
	Span ProposalSpan `json:"span"`
}
type SemanticLimits struct{ MaxEvents, MaxQuoteBytes int }

// PrepareSemanticInput sends raw paragraphs and the selected catalog only.
// This public schema is not the private extraction instruction/prompt.
func PrepareSemanticInput(request ProposalRequest) (string, EvidenceProposal, error) {
	p, err := PrepareProposal(request)
	if err != nil {
		return "", p, err
	}
	paragraphs := []struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}{}
	for _, span := range p.Paragraphs {
		paragraphs = append(paragraphs, struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}{span.ID, span.Text})
	}
	input := struct {
		SchemaVersion int             `json:"schema_version"`
		Paragraphs    any             `json:"paragraphs"`
		Subjects      []CatalogEntry  `json:"subjects"`
		Items         []CatalogEntry  `json:"items"`
		ResponseShape json.RawMessage `json:"response_shape"`
	}{1, paragraphs, request.Catalog.Subjects, request.Catalog.Items, json.RawMessage(`{"schema_version":1,"events":[{"paragraph_id":"string","quote":"exact original substring","occurrence":0,"subject_id":"catalog ID or UNKNOWN","item_id":"catalog ID or UNKNOWN","assertion":"AFFIRMED|NEGATED|HYPOTHETICAL|UNCERTAIN","relation":"UNKNOWN|REVISION|RETRACTION"}]}`)}
	raw, err := json.Marshal(input)
	return string(raw), p, err
}

// CompileSemanticCandidates rejects the entire response on any ambiguity.
// Exact quotes establish location only; semantics and coverage require review.
func CompileSemanticCandidates(request ProposalRequest, text string, limits SemanticLimits) ([]SemanticCandidate, error) {
	p, err := PrepareProposal(request)
	if err != nil {
		return nil, err
	}
	var output struct {
		SchemaVersion *int             `json:"schema_version"`
		Events        *[]SemanticEvent `json:"events"`
	}
	if limits.MaxEvents <= 0 || limits.MaxQuoteBytes <= 0 || !utf8.ValidString(text) || d.DecodePrivate([]byte(text), &output) != nil || output.SchemaVersion == nil || *output.SchemaVersion != 1 || output.Events == nil {
		return nil, d.Fail("SEMANTIC_OUTPUT_INVALID", 422)
	}
	if len(*output.Events) > limits.MaxEvents {
		return nil, d.Fail("SEMANTIC_BUDGET_EXCEEDED", 422)
	}
	paragraphs := map[string]ProposalSpan{}
	for _, paragraph := range p.Paragraphs {
		paragraphs[paragraph.ID] = paragraph
	}
	subjects, items := map[string]bool{"UNKNOWN": true}, map[string]bool{"UNKNOWN": true}
	for _, entry := range request.Catalog.Subjects {
		subjects[entry.ID] = true
	}
	for _, entry := range request.Catalog.Items {
		items[entry.ID] = true
	}
	result := []SemanticCandidate{}
	seen := map[string]bool{}
	for _, event := range *output.Events {
		if event.Quote == "" || event.Occurrence == nil || *event.Occurrence < 0 || !subjects[event.SubjectID] || !items[event.ItemID] || !d.Has([]string{"AFFIRMED", "NEGATED", "HYPOTHETICAL", "UNCERTAIN"}, event.Assertion) || !d.Has([]string{"UNKNOWN", "REVISION", "RETRACTION"}, event.Relation) {
			return nil, d.Fail("SEMANTIC_OUTPUT_INVALID", 422)
		}
		if len(event.Quote) > limits.MaxQuoteBytes {
			return nil, d.Fail("SEMANTIC_BUDGET_EXCEEDED", 422)
		}
		paragraph, exists := paragraphs[event.ParagraphID]
		if !exists {
			return nil, d.Fail("SEMANTIC_SPAN_INVALID", 422)
		}
		// Non-overlapping occurrence search is bounded by the original paragraph,
		// even when an untrusted model supplies a huge occurrence number.
		start, cursor := -1, 0
		for n := 0; cursor < len(paragraph.Text); n++ {
			at := strings.Index(paragraph.Text[cursor:], event.Quote)
			if at < 0 {
				break
			}
			start = cursor + at
			if n == *event.Occurrence {
				break
			}
			cursor = start + len(event.Quote)
			start = -1
		}
		if start < 0 {
			return nil, d.Fail("SEMANTIC_SPAN_INVALID", 422)
		}
		key := d.Digest(event)
		if seen[key] {
			return nil, d.Fail("SEMANTIC_OUTPUT_INVALID", 422)
		}
		seen[key] = true
		absolute := paragraph.Start + start
		span := ProposalSpan{ID: fmt.Sprintf("semantic-%06d", len(result)+1), Start: absolute, End: absolute + len(event.Quote), Text: event.Quote, TextHash: d.ContentDigest([]byte(event.Quote))}
		result = append(result, SemanticCandidate{event, span})
	}
	return result, nil
}
