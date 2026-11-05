package agentcred

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Sign signs body with the agent private key. The result is the header value.
func Sign(pair tls.Certificate, body []byte) (string, error) {
	priv, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return "", errors.New("agent key must be ECDSA P-256")
	}
	sum := sha256.Sum256(body)
	sig, err := priv.Sign(rand.Reader, sum[:], nil)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks that sigHeader is an ECDSA signature of body by cert.
func Verify(cert *x509.Certificate, body []byte, sigHeader string) error {
	if cert == nil {
		return errors.New("no agent certificate")
	}
	if err := checkCert(cert); err != nil {
		return err
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigHeader)
	if err != nil || len(sig) == 0 {
		return errors.New("invalid signature")
	}
	sum := sha256.Sum256(body)
	pub := cert.PublicKey.(*ecdsa.PublicKey)
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
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

// Fingerprint is the hex SHA-256 of the certificate.
func Fingerprint(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func checkCert(cert *x509.Certificate) error {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return errors.New("agent certificate must use ECDSA P-256")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return errors.New("agent certificate is outside its validity period")
	}
	return nil
}
