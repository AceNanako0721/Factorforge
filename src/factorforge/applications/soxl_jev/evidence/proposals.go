package evidence

import (
	"encoding/json"
	"fmt"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Proposals deliberately contain no Claim, completeness or verification marker.
// They are review material, not an implementation of ports.EvidenceExtractor.
type ProposalRequest struct {
	SchemaVersion int             `json:"schema_version"`
	Raw           d.RawEvidence   `json:"raw"`
	Catalog       ProposalCatalog `json:"catalog"`
	Limits        ProposalLimits  `json:"limits"`
}
type ProposalLimits struct {
	MaxInputBytes   int `json:"max_input_bytes"`
	MaxParagraphs   int `json:"max_paragraphs"`
	MaxAnchors      int `json:"max_anchors"`
	MaxMatches      int `json:"max_matches"`
	MaxCatalogTerms int `json:"max_catalog_terms"`
}
type CatalogEntry struct {
	ID    string   `json:"id"`
	Terms []string `json:"terms"`
}
type CatalogEvent struct {
	EventID   string `json:"event_id"`
	FamilyID  string `json:"family_id"`
	SubjectID string `json:"subject_id"`
	ItemID    string `json:"item_id"`
	Period    string `json:"period"`
}
type ProposalCatalog struct {
	Version  string         `json:"version"`
	Subjects []CatalogEntry `json:"subjects"`
	Items    []CatalogEntry `json:"items"`
	Events   []CatalogEvent `json:"events"`
}
type ProposalSpan struct {
	ID       string `json:"id"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Text     string `json:"text"`
	TextHash string `json:"text_hash"`
}
type ProposalAnchor struct {
	ProposalSpan
	ParagraphID string `json:"paragraph_id"`
	Kind        string `json:"kind"`
	DateValid   *bool  `json:"date_valid,omitempty"`
}
type ProposalMatch struct {
	ProposalSpan
	ParagraphID string `json:"paragraph_id"`
	Kind        string `json:"kind"`
	CatalogID   string `json:"catalog_id"`
}
type EventHint struct {
	ParagraphID string       `json:"paragraph_id"`
	EventID     string       `json:"event_id"`
	FamilyID    string       `json:"family_id"`
	Period      ProposalSpan `json:"period"`
	Relation    string       `json:"relation"`
}
type EvidenceProposal struct {
	SchemaVersion  int              `json:"schema_version"`
	ProposalID     string           `json:"proposal_id"`
	MethodVersion  string           `json:"method_version"`
	CatalogVersion string           `json:"catalog_version"`
	RequestHash    string           `json:"request_hash"`
	Raw            d.RawEvidence    `json:"raw"`
	Status         string           `json:"status"`
	Paragraphs     []ProposalSpan   `json:"paragraphs"`
	Anchors        []ProposalAnchor `json:"anchors"`
	Matches        []ProposalMatch  `json:"matches"`
	EventHints     []EventHint      `json:"event_hints"`
	Warnings       []string         `json:"warnings"`
}

var numberLexeme = regexp.MustCompile(`[+-]?[0-9]+(?:[.,][0-9]+)*(?:[eE][+-]?[0-9]+)?`)
var isoDateLexeme = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)

func span(id, content string, start, end int) ProposalSpan {
	text := content[start:end]
	return ProposalSpan{ID: id, Start: start, End: end, Text: text, TextHash: d.ContentDigest([]byte(text))}
}
func asciiWord(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_'
}

// Consume CRLF as one line ending. Regex alternatives could backtrack and
// mistake a single CRLF for an empty line, separating its qualifiers.
func paragraphSpans(content string, max int) ([]ProposalSpan, error) {
	result := []ProposalSpan{}
	begin := -1
	add := func(end int) error {
		if begin < 0 {
			return nil
		}
		part := content[begin:end]
		trimmed := strings.TrimSpace(part)
		if len(result) >= max {
			return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
		}
		at := begin + strings.Index(part, trimmed)
		result = append(result, span(fmt.Sprintf("p%06d", len(result)+1), content, at, at+len(trimmed)))
		begin = -1
		return nil
	}
	for from := 0; from < len(content); {
		end := from
		for end < len(content) && content[end] != '\r' && content[end] != '\n' {
			end++
		}
		if strings.TrimSpace(content[from:end]) == "" {
			if err := add(from); err != nil {
				return nil, err
			}
		} else if begin < 0 {
			begin = from
		}
		next := end
		if next < len(content) {
			next++
			if content[end] == '\r' && next < len(content) && content[next] == '\n' {
				next++
			}
		}
		from = next
	}
	if err := add(len(content)); err != nil {
		return nil, err
	}
	return result, nil
}

// Case variants must be registered. ASCII boundaries prevent substring aliases;
// CJK matching is literal and makes no assertion about semantic entity identity.
func termSpans(text, term string, visit func(int, int) error) error {
	for from := 0; from < len(text); {
		at := strings.Index(text[from:], term)
		if at < 0 {
			break
		}
		start := from + at
		end := start + len(term)
		left := asciiWord(term[0]) && start > 0 && asciiWord(text[start-1])
		right := asciiWord(term[len(term)-1]) && end < len(text) && asciiWord(text[end])
		if !left && !right {
			if err := visit(start, end); err != nil {
				return err
			}
		}
		from = end
	}
	return nil
}

func validateCatalog(c ProposalCatalog, maxTerms int) error {
	if !d.ValidID(c.Version) {
		return d.Fail("PROPOSAL_CATALOG_INVALID", 422)
	}
	n := 0
	sets := []map[string]bool{{}, {}}
	for kind, entries := range [][]CatalogEntry{c.Subjects, c.Items} {
		for _, entry := range entries {
			if !d.ValidID(entry.ID) || sets[kind][entry.ID] || len(entry.Terms) == 0 {
				return d.Fail("PROPOSAL_CATALOG_INVALID", 422)
			}
			sets[kind][entry.ID] = true
			seen := map[string]bool{}
			for _, term := range entry.Terms {
				if !utf8.ValidString(term) || term == "" || term != strings.TrimSpace(term) || seen[term] {
					return d.Fail("PROPOSAL_CATALOG_INVALID", 422)
				}
				seen[term] = true
				n++
				if n > maxTerms {
					return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, event := range c.Events {
		if !d.ValidID(event.EventID) || !d.ValidID(event.FamilyID) || seen[event.EventID] || !sets[0][event.SubjectID] || !sets[1][event.ItemID] || event.Period == "" || event.Period != strings.TrimSpace(event.Period) || !utf8.ValidString(event.Period) {
			return d.Fail("PROPOSAL_CATALOG_INVALID", 422)
		}
		seen[event.EventID] = true
		n++ // Period entries also consume the finite catalog budget.
		if n > maxTerms {
			return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
		}
	}
	return nil
}

func PrepareProposal(request ProposalRequest) (EvidenceProposal, error) {
	empty := EvidenceProposal{}
	l := request.Limits
	r := request.Raw
	if request.SchemaVersion != 1 || l.MaxInputBytes <= 0 || l.MaxParagraphs <= 0 || l.MaxAnchors <= 0 || l.MaxMatches <= 0 || l.MaxCatalogTerms <= 0 || !utf8.ValidString(r.Content) || strings.TrimSpace(r.Content) == "" || !d.ValidID(r.EvidenceID) || !d.ValidID(r.SourceID) || !d.ValidID(r.LicenceRef) || !d.UTC(r.ReceivedAt) || r.ContentHash != d.ContentDigest([]byte(r.Content)) || r.RevisionOf != nil && !d.ValidID(*r.RevisionOf) {
		return empty, d.Fail("PROPOSAL_INPUT_INVALID", 422)
	}
	for _, at := range []*time.Time{r.FirstPublicAt, r.PublishedAt} {
		if at != nil && (!d.UTC(*at) || at.After(r.ReceivedAt)) {
			return empty, d.Fail("PROPOSAL_INPUT_INVALID", 422)
		}
	}
	if len(r.Content) > l.MaxInputBytes {
		return empty, d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
	}
	if err := validateCatalog(request.Catalog, l.MaxCatalogTerms); err != nil {
		return empty, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return empty, d.Fail("PROPOSAL_INPUT_INVALID", 422)
	}
	// Clone timestamp/revision pointers; later caller edits cannot mutate the record.
	var frozen ProposalRequest
	if json.Unmarshal(encoded, &frozen) != nil {
		return empty, d.Fail("PROPOSAL_INPUT_INVALID", 422)
	}
	hash := d.ContentDigest(encoded)
	result := EvidenceProposal{SchemaVersion: 1, ProposalID: "proposal-" + hash, MethodVersion: "paragraph-literal-1", CatalogVersion: request.Catalog.Version, RequestHash: hash, Raw: frozen.Raw, Status: "REVIEW_REQUIRED", Paragraphs: []ProposalSpan{}, Anchors: []ProposalAnchor{}, Matches: []ProposalMatch{}, EventHints: []EventHint{}, Warnings: []string{"UNVERIFIED_ORIGINAL", "SEMANTIC_EXTRACTION_NOT_PERFORMED", "AMOUNTS_AND_RELATIVE_DATES_NOT_NORMALIZED", "EVENT_RELATION_UNKNOWN"}}
	result.Paragraphs, err = paragraphSpans(r.Content, l.MaxParagraphs)
	if err != nil {
		return empty, err
	}
	for _, p := range result.Paragraphs {
		for _, matcher := range []struct {
			kind string
			re   *regexp.Regexp
		}{{"NUMBER_LEXEME", numberLexeme}, {"ISO_DATE", isoDateLexeme}} {
			for _, loc := range matcher.re.FindAllStringIndex(p.Text, -1) {
				if matcher.kind == "ISO_DATE" && (loc[0] > 0 && asciiWord(p.Text[loc[0]-1]) || loc[1] < len(p.Text) && asciiWord(p.Text[loc[1]])) {
					continue
				}
				if len(result.Anchors) >= l.MaxAnchors {
					return empty, d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
				}
				a := ProposalAnchor{ProposalSpan: span(fmt.Sprintf("a%06d", len(result.Anchors)+1), r.Content, p.Start+loc[0], p.Start+loc[1]), ParagraphID: p.ID, Kind: matcher.kind}
				if matcher.kind == "ISO_DATE" {
					_, err := time.Parse("2006-01-02", a.Text)
					valid := err == nil
					a.DateValid = &valid
				}
				result.Anchors = append(result.Anchors, a)
			}
		}
		found := []map[string]bool{{}, {}}
		for kind, entries := range [][]CatalogEntry{request.Catalog.Subjects, request.Catalog.Items} {
			for _, entry := range entries {
				for _, term := range entry.Terms {
					err := termSpans(p.Text, term, func(begin, end int) error {
						if len(result.Matches)+len(result.EventHints) >= l.MaxMatches {
							return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
						}
						found[kind][entry.ID] = true
						result.Matches = append(result.Matches, ProposalMatch{ProposalSpan: span(fmt.Sprintf("m%06d", len(result.Matches)+1), r.Content, p.Start+begin, p.Start+end), ParagraphID: p.ID, Kind: []string{"SUBJECT", "ITEM"}[kind], CatalogID: entry.ID})
						return nil
					})
					if err != nil {
						return empty, err
					}
				}
			}
		}
		for _, event := range request.Catalog.Events {
			if !found[0][event.SubjectID] || !found[1][event.ItemID] {
				continue
			}
			err := termSpans(p.Text, event.Period, func(begin, end int) error {
				if len(result.Matches)+len(result.EventHints) >= l.MaxMatches {
					return d.Fail("PROPOSAL_BUDGET_EXCEEDED", 422)
				}
				result.EventHints = append(result.EventHints, EventHint{ParagraphID: p.ID, EventID: event.EventID, FamilyID: event.FamilyID, Period: span(fmt.Sprintf("h%06d", len(result.EventHints)+1), r.Content, p.Start+begin, p.Start+end), Relation: "UNKNOWN"})
				return nil
			})
			if err != nil {
				return empty, err
			}
		}
	}
	return result, nil
}
