package domain

import "time"

// Generation is untrusted model text plus transport provenance, never a fact.
type Generation struct {
	Provider    string    `json:"provider"`
	AccountID   int64     `json:"account_id"`
	Model       string    `json:"model"`
	PromptHash  string    `json:"prompt_hash"`
	Text        string    `json:"text"`
	CompletedAt time.Time `json:"completed_at"`
}
