package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrKeyRequired is returned by Validate when the caller supplied a blank
// key — never accept one, since a blank key would let two unrelated
// transactions collide on ledger_transfers.idempotency_key's UNIQUE
// constraint (spec §4.3) and be silently treated as duplicates of each other.
var ErrKeyRequired = errors.New("idempotency key is required")

// Validate rejects a blank key so every caller enforces the same rule
// instead of re-deriving it per handler.
func Validate(key string) error {
	if strings.TrimSpace(key) == "" {
		return ErrKeyRequired
	}
	return nil
}

// Derive returns a deterministic idempotency key for a server-initiated
// transaction (e.g. a nightly late-fee run, or a payment gateway webhook
// keyed on its own gateway_reference) by hashing its identifying parts
// together, so a retried delivery of the same logical event always produces
// the same key instead of double-posting (spec §4.3).
func Derive(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])
}
