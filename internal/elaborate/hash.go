package elaborate

import (
	"crypto/sha256"
	"fmt"
)

// shortHash returns the first 8 bytes of the SHA-256 of s, hex-encoded.
// Used to fingerprint the issue body for cache invalidation — a long hash
// would bloat the cache key without buying additional collision resistance
// at this scope.
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h[:8])
}
