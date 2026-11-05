package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/version"
)

func (a *Agent) postAuthed(ctx context.Context, path string, payload []byte, dest any) error {
	err := a.post(ctx, path, a.state.Token(), payload, "", dest)
	if !errors.Is(err, errAuth) {
		return err
	}
	if regErr := a.register(ctx); regErr != nil {
		return regErr
	}
	return a.post(ctx, path, a.state.Token(), payload, "", dest)
}

func (a *Agent) postSigned(ctx context.Context, path string, payload []byte, sig string, dest any) error {
	return a.post(ctx, path, "", payload, sig, dest)
}

func (a *Agent) post(ctx context.Context, path, secret string, payload []byte, sig string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(path), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "dlog-agent/"+version.Version)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	if sig != "" {
		req.Header.Set(agentcred.Header, sig)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		if dest != nil && len(bytes.TrimSpace(b)) > 0 {
			if err := json.Unmarshal(b, dest); err != nil {
				return err
			}
		}
		return nil
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", errDrop, bytesTrim(b))
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", errAuth, bytesTrim(b))
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", errDisabled, bytesTrim(b))
	default:
		return fmt.Errorf("server %s: %s", resp.Status, bytesTrim(b))
	}
}

func (a *Agent) endpoint(path string) string {
	return strings.TrimRight(a.cfg.ServerURL, "/") + path
}

func newHTTPClient(caFile string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in %s", caFile)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Transport: transport}, nil
}
