package agentcred

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"unicode"
)

// Ensure loads the key at idPath, or generates a new one when neither file
// exists yet. The key file is written only on generation.
func Ensure(idPath, keyPath string) (key *Key, generated bool, err error) {
	_, idErr := os.Stat(idPath)
	_, keyErr := os.Stat(keyPath)
	switch {
	case idErr == nil && keyErr == nil:
		k, err := Load(idPath, keyPath)
		return k, false, err
	case idErr != nil && !errors.Is(idErr, os.ErrNotExist):
		return nil, false, idErr
	case keyErr != nil && !errors.Is(keyErr, os.ErrNotExist):
		return nil, false, keyErr
	case errors.Is(idErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
		k, err := Generate()
		if err != nil {
			return nil, false, err
		}
		if err := k.Write(idPath, keyPath); err != nil {
			return nil, false, err
		}
		return k, true, nil
	default:
		return nil, false, errors.New("psk id and key must both exist or both be missing")
	}
}

// Load reads the id and key files an agent signs with.
func Load(idPath, keyPath string) (*Key, error) {
	id, err := os.ReadFile(idPath)
	if err != nil {
		return nil, err
	}
	secret, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	return Parse(string(id), string(secret))
}

// Parse reads an id and a 32-byte key encoded as unpadded base64url.
func Parse(id, key string) (*Key, error) {
	id = strings.TrimSpace(id)
	key = strings.TrimSpace(key)
	if !validID(id) {
		return nil, errors.New("psk id must be 1-64 characters of letters, digits, '.', '_', or '-'")
	}
	if len(key) == 0 || len(key) > 128 {
		return nil, errors.New("psk key must be 32 bytes, base64url")
	}
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != keySize {
		return nil, errors.New("psk key must be 32 bytes, base64url")
	}
	secret := make([]byte, keySize)
	copy(secret, raw)
	return &Key{id: id, secret: secret}, nil
}

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for i, r := range id {
		switch {
		case r > unicode.MaxASCII:
			return false
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
