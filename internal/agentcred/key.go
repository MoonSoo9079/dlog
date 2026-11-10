// Package agentcred is the shared pre-shared key every agent uses to register.
// The server trusts one id and key. Each agent holds that same pair and signs
// the registration body with HMAC-SHA256. The hostname in the signed body is
// what distinguishes hosts.
package agentcred

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	// Header is the HTTP header that carries the request MAC.
	Header = "X-Dlog-Signature"
	// IDHeader is the HTTP header that carries the pre-shared key id.
	IDHeader = "X-Dlog-PSK-Id"
	// Window is how far a signed registration timestamp may drift from the server clock.
	Window = 5 * time.Minute

	keySize = 32
	idSize  = 16
)

// Key is the shared id and secret an agent uses to sign registration.
type Key struct {
	id     string
	secret []byte
}

// Generate creates a random id and a 32-byte key.
func Generate() (*Key, error) {
	idBytes := make([]byte, idSize)
	secret := make([]byte, keySize)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return &Key{id: hex.EncodeToString(idBytes), secret: secret}, nil
}

// ID is the public identifier sent with each signed registration.
func (k *Key) ID() string {
	if k == nil {
		return ""
	}
	return k.id
}

// Text is the key as unpadded base64url, the contents of the key file.
func (k *Key) Text() string {
	if k == nil || len(k.secret) != keySize {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(k.secret)
}

// Write stores the id and the key. The key file is mode 0600.
func (k *Key) Write(idPath, keyPath string) error {
	if k == nil || k.id == "" || len(k.secret) != keySize {
		return errors.New("psk is incomplete")
	}
	if err := os.MkdirAll(filepath.Dir(idPath), 0o750); err != nil {
		return err
	}
	if filepath.Dir(keyPath) != filepath.Dir(idPath) {
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
			return err
		}
	}
	if err := writeNew(idPath, []byte(k.id+"\n"), 0o644); err != nil {
		return err
	}
	return writeNew(keyPath, []byte(k.Text()+"\n"), 0o600)
}
