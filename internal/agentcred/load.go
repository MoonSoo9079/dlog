package agentcred

import (
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
)

// Ensure loads the certificate at certPath, or generates a new keypair when
// that file does not exist yet. The private key is written only on generation.
func Ensure(certPath, keyPath string) (cert *x509.Certificate, generated bool, err error) {
	if _, err := os.Stat(certPath); err == nil {
		c, err := LoadCert(certPath)
		return c, false, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	k, err := Generate()
	if err != nil {
		return nil, false, err
	}
	if err := k.Write(certPath, keyPath); err != nil {
		return nil, false, err
	}
	return k.cert, true, nil
}

// LoadCert reads a PEM certificate and requires ECDSA P-256.
func LoadCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("agent certificate file does not contain a PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := checkCert(cert); err != nil {
		return nil, err
	}
	return cert, nil
}

// LoadPair reads the certificate and private key an agent signs with.
func LoadPair(certPath, keyPath string) (tls.Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	if len(pair.Certificate) == 0 {
		return tls.Certificate{}, errors.New("agent certificate is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := checkCert(leaf); err != nil {
		return tls.Certificate{}, err
	}
	if _, ok := pair.PrivateKey.(*ecdsa.PrivateKey); !ok {
		return tls.Certificate{}, errors.New("agent key must be ECDSA P-256")
	}
	pair.Leaf = leaf
	return pair, nil
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
