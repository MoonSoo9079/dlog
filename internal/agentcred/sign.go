package agentcred

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Sign returns the X-Dlog-Signature value for body.
func Sign(k *Key, body []byte) (string, error) {
	if k == nil || len(k.secret) != keySize {
		return "", errors.New("psk key is required")
	}
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks that id is this key's id and sigHeader is the HMAC-SHA256 of body.
func Verify(k *Key, id, sigHeader string, body []byte) error {
	if k == nil || len(k.secret) != keySize {
		return errors.New("no agent key")
	}
	mac := hmac.New(sha256.New, k.secret)
	mac.Write(body)
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sigHeader)
	if err != nil {
		got = nil
	}
	if !hmac.Equal(got, expected) || subtle.ConstantTimeCompare([]byte(id), []byte(k.id)) != 1 {
		return errors.New("invalid signature")
	}
	return nil
}

// Fresh reports whether t is within Window of now.
func Fresh(t, now time.Time) bool {
	if t.IsZero() {
		return false
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	return d <= Window
}

// Fingerprint is the hex SHA-256 of the key. It identifies a key without revealing it.
func Fingerprint(k *Key) string {
	if k == nil || len(k.secret) == 0 {
		return ""
	}
	sum := sha256.Sum256(k.secret)
	return hex.EncodeToString(sum[:])
}
