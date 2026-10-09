package evidence

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/soxl_jev/domain"
	"golang.org/x/net/html"
)

type DocumentViewLimits struct {
	MaxTokens       int `json:"max_tokens"`
	MaxTokenBytes   int `json:"max_token_bytes"`
	MaxDisplayBytes int `json:"max_display_bytes"`
}

type DocumentToken struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	RawHash string `json:"raw_hash"`
	Text    string `json:"text"`
	Tag     string `json:"tag"`
}

// DocumentView is a reading aid. Its decoded Text is never an original Claim
// span, a browser visibility judgment, or an evidence completeness marker.
type DocumentView struct {
	MethodVersion string          `json:"method_version"`
	MediaType     string          `json:"media_type"`
	RawHash       string          `json:"raw_hash"`
	RawBytes      int             `json:"raw_bytes"`
	Tokens        []DocumentToken `json:"tokens"`
	Warnings      []string        `json:"warnings"`
}

func BuildDocumentView(raw d.RawEvidence, media string, limits DocumentViewLimits) (DocumentView, error) {
	empty := DocumentView{}
	if limits.MaxTokens <= 0 || limits.MaxTokenBytes <= 0 || limits.MaxDisplayBytes <= 0 ||
		!utf8.ValidString(raw.Content) || len(raw.Content) == math.MaxInt || strings.TrimSpace(raw.Content) == "" ||
		raw.ContentHash != d.ContentDigest([]byte(raw.Content)) || !d.Has([]string{"text/plain", "text/html"}, media) {
		return empty, d.Fail("DOCUMENT_VIEW_INPUT_INVALID", 422)
	}
	view := DocumentView{MethodVersion: "raw-token-view-1", MediaType: media, RawHash: raw.ContentHash, RawBytes: len(raw.Content), Tokens: []DocumentToken{}, Warnings: []string{"DISPLAY_TEXT_NOT_EVIDENCE"}}
	offset, displayBytes := 0, 0
	normalized, trailing := false, false
	add := func(kind, text, tag string, original []byte) error {
		if len(view.Tokens) >= limits.MaxTokens || len(original) > limits.MaxTokenBytes || len(text) > limits.MaxDisplayBytes-displayBytes {
			return d.Fail("DOCUMENT_VIEW_BUDGET_EXCEEDED", 422)
		}
		if len(original) == 0 || len(original) > len(raw.Content)-offset || !utf8.Valid(original) || !utf8.ValidString(text) || !bytes.Equal(original, []byte(raw.Content[offset:offset+len(original)])) {
			return d.Fail("DOCUMENT_VIEW_MAPPING_INVALID", 422)
		}
		view.Tokens = append(view.Tokens, DocumentToken{ID: fmt.Sprintf("t%06d", len(view.Tokens)+1), Kind: kind, Start: offset, End: offset + len(original), RawHash: d.ContentDigest(original), Text: text, Tag: tag})
		offset += len(original)
		displayBytes += len(text)
		return nil
	}
	if media == "text/plain" {
		if err := add("TEXT", raw.Content, "", []byte(raw.Content)); err != nil {
			return empty, err
		}
	} else {
		view.Warnings = append(view.Warnings, "HTML_TOKEN_VIEW_ONLY")
		z := html.NewTokenizer(strings.NewReader(raw.Content))
		// The tokenizer peeks into subsequent tags and rejects on reaching its
		// buffer limit. Bound it by the already limited complete input; enforce
		// the caller's exact raw-token limit separately on emitted tokens.
		z.SetMaxBuf(len(raw.Content) + 1)
		for {
			kind := z.Next()
			// Token normalizes/mutates the tokenizer's buffer; snapshot Raw first.
			original := bytes.Clone(z.Raw())
			if kind == html.ErrorToken {
				if z.Err() == html.ErrBufferExceeded {
					return empty, d.Fail("DOCUMENT_VIEW_BUDGET_EXCEEDED", 422)
				}
				if z.Err() != io.EOF {
					return empty, d.Fail("DOCUMENT_VIEW_MAPPING_INVALID", 422)
				}
				if len(original) > 0 {
					if err := add("TRAILING", "", "", original); err != nil {
						return empty, err
					}
					trailing = true
				}
				break
			}
			token := z.Token()
			label, text, tag := "", "", ""
			switch kind {
			case html.TextToken:
				label, text = "TEXT", token.Data
				normalized = normalized || text != string(original)
			case html.StartTagToken:
				label, tag = "START_TAG", token.Data
			case html.EndTagToken:
				label, tag = "END_TAG", token.Data
			case html.SelfClosingTagToken:
				label, tag = "SELF_CLOSING_TAG", token.Data
			case html.CommentToken:
				label = "COMMENT"
			case html.DoctypeToken:
				label = "DOCTYPE"
			default:
				return empty, d.Fail("DOCUMENT_VIEW_MAPPING_INVALID", 422)
			}
			if err := add(label, text, tag, original); err != nil {
				return empty, err
			}
		}
	}
	if offset != len(raw.Content) {
		return empty, d.Fail("DOCUMENT_VIEW_MAPPING_INVALID", 422)
	}
	if normalized {
		view.Warnings = append(view.Warnings, "DISPLAY_TEXT_NORMALIZED")
	}
	if trailing {
		view.Warnings = append(view.Warnings, "HTML_TRAILING_BYTES")
	}
	return view, nil
}
