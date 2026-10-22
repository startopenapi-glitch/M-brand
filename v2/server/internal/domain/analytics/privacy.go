package analytics

import (
	"crypto/sha256"
	"encoding/hex"
)

// VisitorHash is stable per anonymous visitor and never contains the raw id.
func VisitorHash(visitor string) string {
	if visitor == "" {
		return ""
	}
	h := sha256.Sum256([]byte("0brand:" + visitor))
	return hex.EncodeToString(h[:])
}
