package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Digest is the hex SHA-256 of the JSON encoding of value.
func Digest(value any) string {
	raw, _ := json.Marshal(value)
	return ContentDigest(raw)
}
func ContentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func UTC(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }
