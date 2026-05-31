package fetcher

import "crypto/sha256"
import "fmt"

// DedupKey computes a deduplication key for a feed entry.
// Uses the entry's GUID if present and non-empty.
// Falls back to the SHA-256 hash of the link.
func DedupKey(guid, link string) string {
	if guid != "" {
		return guid
	}
	return sha256Hex(link)
}

// sha256Hex returns the hex-encoded SHA-256 hash of s.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
