// Package token creates and checks high-entropy secrets.
// Raw secrets are shown once. The server stores only a SHA-256 hash.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// New returns prefix plus 32 random bytes, encoded so the value is URL-safe.
func New(prefix string) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf)
}

// NewID returns prefix plus 16 random bytes as hex. IDs are identifiers, not secrets.
func NewID(prefix string) string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(buf)
}

// Hash returns the hex SHA-256 of a raw secret.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Equal compares two secrets without leaking their lengths through the comparison itself.
func Equal(a, b string) bool {
	ah := sha256.Sum256([]byte(a))
	bh := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ah[:], bh[:]) == 1
}
