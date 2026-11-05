// Package agentcred is the shared certificate every agent uses to register.
// The server trusts one certificate. Each agent holds that certificate and its
// private key, and signs the registration request. The key is the same on
// every agent; the hostname in the signed body is what distinguishes hosts.
package agentcred

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	// Header is the HTTP header that carries the request signature.
	Header = "X-Dlog-Signature"
	// Window is how far a signed registration timestamp may drift from the server clock.
	Window = 5 * time.Minute
)

// Key is a generated agent certificate and its private key.
type Key struct {
	cert *x509.Certificate
	priv *ecdsa.PrivateKey
}

// Generate creates a self-signed ECDSA P-256 certificate valid for ten years.
func Generate() (*Key, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "dlog-agent"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Key{cert: cert, priv: priv}, nil
}

// Certificate is the public certificate the server trusts.
func (k *Key) Certificate() *x509.Certificate {
	return k.cert
}

// Pair is the certificate and private key an agent uses to sign requests.
func (k *Key) Pair() tls.Certificate {
	return tls.Certificate{
		Certificate: [][]byte{k.cert.Raw},
		PrivateKey:  k.priv,
		Leaf:        k.cert,
	}
}

// Write stores the certificate and the private key. The key file is mode 0600.
func (k *Key) Write(certPath, keyPath string) error {
	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil {
		return err
	}
	if filepath.Dir(keyPath) != filepath.Dir(certPath) {
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
			return err
		}
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: k.cert.Raw})
	keyDER, err := x509.MarshalECPrivateKey(k.priv)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeNew(certPath, certPEM, 0o644); err != nil {
		return err
	}
	return writeNew(keyPath, keyPEM, 0o600)
}
