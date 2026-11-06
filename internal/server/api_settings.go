package server

import (
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/cli"
	"dlog/internal/protocol"
)

func (s *Server) handleBrand(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.settingsSnapshot()
	name := strings.TrimSpace(cfg.BrandName)
	if name == "" {
		name = "Dlog"
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "tagline": cfg.BrandTagline})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	s.writeSettings(w)
}

func (s *Server) writeSettings(w http.ResponseWriter) {
	pending, err := s.store.ListPending()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	cfg := s.store.settingsSnapshot()
	writeJSON(w, http.StatusOK, struct {
		Settings
		Pending []PendingHost `json:"pending"`
	}{Settings: cfg, Pending: pending})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BrandName      *string `json:"brand_name"`
		BrandTagline   *string `json:"brand_tagline"`
		AutoRegister   *bool   `json:"auto_register"`
		DefaultGroupID *string `json:"default_group_id"`
		Retention      *string `json:"retention"`
		Session        *string `json:"session"`
		Offline        *string `json:"offline"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if body.BrandName == nil && body.BrandTagline == nil && body.AutoRegister == nil && body.DefaultGroupID == nil && body.Retention == nil && body.Session == nil && body.Offline == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	cfg := s.store.settingsSnapshot()
	if body.BrandName != nil {
		name := strings.TrimSpace(*body.BrandName)
		if name != "" && !validBrandName(name) {
			writeError(w, http.StatusBadRequest, "invalid name")
			return
		}
		cfg.BrandName = name
	}
	if body.BrandTagline != nil {
		tag := strings.TrimSpace(*body.BrandTagline)
		if !validTagline(tag) {
			writeError(w, http.StatusBadRequest, "invalid tagline")
			return
		}
		cfg.BrandTagline = tag
	}
	if body.AutoRegister != nil {
		cfg.AutoRegister = *body.AutoRegister
	}
	if body.DefaultGroupID != nil {
		id := strings.TrimSpace(*body.DefaultGroupID)
		if id != "" {
			if _, err := s.store.Group(id); err != nil {
				writeStoreError(w, err)
				return
			}
		}
		cfg.DefaultGroupID = id
	}
	if body.Retention != nil {
		d, err := cli.Duration(strings.TrimSpace(*body.Retention), -1)
		if err != nil || d < 0 || d > maxRetention {
			writeError(w, http.StatusBadRequest, "invalid duration")
			return
		}
		cfg.RetentionMS = d.Milliseconds()
	}
	if body.Session != nil {
		d, err := cli.Duration(strings.TrimSpace(*body.Session), -1)
		if err != nil || d < minSession || d > maxSession {
			writeError(w, http.StatusBadRequest, "invalid session")
			return
		}
		cfg.SessionMS = d.Milliseconds()
	}
	if body.Offline != nil {
		d, err := cli.Duration(strings.TrimSpace(*body.Offline), -1)
		if err != nil || d < minOffline || d > maxOffline {
			writeError(w, http.StatusBadRequest, "invalid offline window")
			return
		}
		cfg.OfflineMS = d.Milliseconds()
	}
	if err := s.store.SaveSettings(cfg); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeSettings(w)
}

func (s *Server) handleApprovePending(w http.ResponseWriter, r *http.Request) {
	hostname := strings.TrimSpace(r.PathValue("hostname"))
	if !protocol.ValidHostname(hostname) {
		writeError(w, http.StatusBadRequest, "invalid hostname")
		return
	}
	host, err := s.store.ApprovePending(hostname)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("pending host approved", "host", host.Hostname, "id", host.ID)
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) handleDismissPending(w http.ResponseWriter, r *http.Request) {
	hostname := strings.TrimSpace(r.PathValue("hostname"))
	if !protocol.ValidHostname(hostname) {
		writeError(w, http.StatusBadRequest, "invalid hostname")
		return
	}
	if err := s.store.DismissPending(hostname); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCertificate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.certificateView(false))
}

func (s *Server) handleCertificatePEM(w http.ResponseWriter, r *http.Request) {
	cert := s.certificate()
	if cert == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="agent.crt"`)
	_, _ = w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

func (s *Server) handleReplaceCertificate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"`
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	certPath, keyPath := s.certFiles()
	if certPath == "" || keyPath == "" {
		writeError(w, http.StatusBadRequest, "certificate storage is not configured")
		return
	}
	var key *agentcred.Key
	var err error
	switch body.Action {
	case "generate":
		key, err = agentcred.Generate()
	case "replace":
		key, err = agentcred.ParsePair([]byte(body.CertPEM), []byte(body.KeyPEM))
	default:
		writeError(w, http.StatusBadRequest, "invalid action")
		return
	}
	if err != nil {
		slog.Warn("certificate rejected", "err", err)
		writeError(w, http.StatusBadRequest, "invalid certificate")
		return
	}
	if err := key.Write(certPath, keyPath); err != nil {
		slog.Error("write certificate", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.setCertificate(key.Certificate())
	slog.Info("agent certificate replaced", "sha256", agentcred.Fingerprint(key.Certificate()), "generated", body.Action == "generate")
	view := s.certificateView(true)
	if body.Action == "generate" {
		view.KeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: mustMarshalKey(key)}))
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) certificateView(includeCert bool) certificateView {
	cert := s.certificate()
	certPath, keyPath := s.certFiles()
	view := certificateView{CertPath: certPath, KeyPath: keyPath}
	if cert == nil {
		return view
	}
	view.Fingerprint = agentcred.Fingerprint(cert)
	view.Subject = certSubject(cert)
	view.NotBefore = cert.NotBefore.UTC()
	view.NotAfter = cert.NotAfter.UTC()
	if includeCert {
		view.CertPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	}
	return view
}

type certificateView struct {
	Fingerprint string    `json:"fingerprint"`
	Subject     string    `json:"subject"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	CertPath    string    `json:"cert_path,omitempty"`
	KeyPath     string    `json:"key_path,omitempty"`
	CertPEM     string    `json:"cert_pem,omitempty"`
	KeyPEM      string    `json:"key_pem,omitempty"`
}

func certSubject(cert *x509.Certificate) string {
	if cert.Subject.CommonName != "" {
		return cert.Subject.CommonName
	}
	return cert.Subject.String()
}

func mustMarshalKey(key *agentcred.Key) []byte {
	der, err := key.PrivateDER()
	if err != nil {
		return nil
	}
	return der
}
