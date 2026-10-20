package domain

// AuditRecord keeps the durable sequence separate from legacy row payloads.
type AuditRecord struct {
	Sequence int
	Payload  map[string]any
}

// CloneState deep-copies the aggregate except the append-only sentiment
// ledger, whose entries are never modified once appended: the copy gets its
// own slice of the same entries, so SameLedgerPrefix can compare them by value.
func CloneState(s *StrategyState) *StrategyState {
	shallow := *s
	shallow.Ledger = nil
	copy := Clone(&shallow)
	copy.AuditCount = s.AuditCount
	if s.Ledger != nil {
		copy.Ledger = append(make([]LedgerEntry, 0, len(s.Ledger)), s.Ledger...)
	}
	return copy
}

// SameLedgerPrefix reports whether ledger starts with prefix unchanged.
// Entries copied by CloneState compare equal directly; any other entry falls
// back to its canonical digest.
func SameLedgerPrefix(prefix, ledger []LedgerEntry) bool {
	if len(ledger) < len(prefix) {
		return false
	}
	for i := range prefix {
		if prefix[i] != ledger[i] && Digest(prefix[i]) != Digest(ledger[i]) {
			return false
		}
	}
	return true
}

// AppendAudit buffers only the rows added by the current transaction.
func (s *StrategyState) AppendAudit(row map[string]any) {
	row["sequence"] = s.AuditCount + len(s.Audit) + 1
	s.Audit = append(s.Audit, row)
}

// CheckAuditAppend rejects changes to the loaded count or buffered prefix.
// Missing sequence fields are accepted for legacy seeds; durable numbering is
// still assigned by the store, never by a caller-controlled historical index.
func CheckAuditAppend(before, after *StrategyState) error {
	if before.AuditCount != after.AuditCount || len(after.Audit) < len(before.Audit) || Digest(after.Audit[:len(before.Audit)]) != Digest(before.Audit) {
		return &Error{Code: "APPEND_ONLY_MUTATION_FORBIDDEN", Status: 503}
	}
	for i, row := range after.Audit {
		if sequence, exists := row["sequence"]; exists && Text(sequence) != Text(after.AuditCount+i+1) {
			return &Error{Code: "APPEND_ONLY_MUTATION_FORBIDDEN", Status: 503}
		}
	}
	return nil
}
