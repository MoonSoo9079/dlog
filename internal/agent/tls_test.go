package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientAcceptsSelfSignedServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	plain := &http.Client{Timeout: 2 * time.Second}
	if _, err := plain.Get(srv.URL); err == nil {
		t.Fatal("default client accepted a self-signed certificate")
	}
	client, err := newHTTPClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Timeout = 2 * time.Second
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	leafSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
		},
	}
	go leafSrv.ServeTLS(ln, "", "")
	t.Cleanup(func() { leafSrv.Close() })
	leafURL := "https://" + ln.Addr().String()
	leafClient, err := newHTTPClient(leafURL)
	if err != nil {
		t.Fatal(err)
	}
	leafClient.Timeout = 2 * time.Second
	resp, err = leafClient.Get(leafURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("leaf status %d", resp.StatusCode)
	}
}

func TestVerifyServerCerts(t *testing.T) {
	roots := x509.NewCertPool()
	ok, err := mintCert(t, nil, []net.IP{net.ParseIP("127.0.0.1")}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyServerCerts([]*x509.Certificate{ok}, "127.0.0.1", roots); err != nil {
		t.Fatal(err)
	}
	wrong, err := mintCert(t, []string{"logs.example"}, nil, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyServerCerts([]*x509.Certificate{wrong}, "127.0.0.1", roots); err == nil {
		t.Fatal("wrong name was accepted")
	}
	expired, err := mintCert(t, nil, []net.IP{net.ParseIP("127.0.0.1")}, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyServerCerts([]*x509.Certificate{expired}, "127.0.0.1", roots); err == nil {
		t.Fatal("expired certificate was accepted")
	}

	ca, caKey, err := newCA(t)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := signedLeaf(t, ca, caKey, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyServerCerts([]*x509.Certificate{leaf, ca}, "127.0.0.1", roots); err == nil {
		t.Fatal("private CA was accepted")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if err := verifyServerCerts([]*x509.Certificate{leaf, ca}, "127.0.0.1", pool); err != nil {
		t.Fatal(err)
	}
}

func mintCert(t *testing.T, dns []string, ips []net.IP, notBefore, notAfter time.Time) (*x509.Certificate, error) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "dlog-test"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

func newCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dlog-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return cert, priv, nil
}

func signedLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, ips []net.IP) (*x509.Certificate, error) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "dlog-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &priv.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}
