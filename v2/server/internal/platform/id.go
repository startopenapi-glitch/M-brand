// Package platform contains process-wide primitives with no domain knowledge.
package platform

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID returns n lowercase hex characters. Callers own uniqueness checks.
func NewID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// RandHex returns 2*n lowercase hex characters.
func RandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
