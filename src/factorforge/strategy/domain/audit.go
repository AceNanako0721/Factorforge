package domain

// AuditRecord keeps the durable sequence separate from legacy row payloads.
type AuditRecord struct {
	Sequence int
	Payload  map[string]any
}

func CloneState(s *StrategyState) *StrategyState {
	copy := Clone(s)
	copy.AuditCount = s.AuditCount
	return copy
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
