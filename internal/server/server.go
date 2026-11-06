package server

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"sync"
	"time"

	"dlog/internal/version"
)

const (
	sessionCookie = "dlog_session"
	maxBody       = 4 << 20
)

type Server struct {
	store        *Store
	certMu       sync.RWMutex
	agentCert    *x509.Certificate
	certPath     string
	keyPath      string
	trustProxy   bool
	secureCookie bool
	login        *slidingLimit
	regLimit     *slidingLimit
}

func New(store *Store, agentCert *x509.Certificate, trustProxy, secureCookie bool) *Server {
	return &Server{
		store:        store,
		agentCert:    agentCert,
		trustProxy:   trustProxy,
		secureCookie: secureCookie,
		login:        &slidingLimit{},
		regLimit:     &slidingLimit{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/brand", s.handleBrand)
	mux.HandleFunc("POST /api/v1/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/logout", s.handleLogout)
	mux.HandleFunc("POST /api/v1/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/v1/heartbeat", s.handleHeartbeat)

	mux.HandleFunc("GET /api/v1/meta", s.operatorJSON(s.handleMeta))
	mux.HandleFunc("GET /api/v1/me", s.operatorJSON(s.handleMe))
	mux.HandleFunc("PATCH /api/v1/me", s.operatorJSON(s.handleUpdateMe))
	mux.HandleFunc("GET /api/v1/hosts", s.operatorJSON(s.handleListHosts))
	mux.HandleFunc("GET /api/v1/containers", s.operatorJSON(s.handleContainers))
	mux.HandleFunc("GET /api/v1/logs", s.operatorJSON(s.handleLogs))

	mux.HandleFunc("GET /api/v1/admin/users", s.adminJSON(s.handleListUsers))
	mux.HandleFunc("POST /api/v1/admin/users", s.adminJSON(s.handleCreateUser))
	mux.HandleFunc("PATCH /api/v1/admin/users/{id}", s.adminJSON(s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/v1/admin/users/{id}", s.adminJSON(s.handleDeleteUser))
	mux.HandleFunc("POST /api/v1/admin/groups", s.adminJSON(s.handleCreateGroup))
	mux.HandleFunc("PATCH /api/v1/admin/groups/{id}", s.adminJSON(s.handleRenameGroup))
	mux.HandleFunc("DELETE /api/v1/admin/groups/{id}", s.adminJSON(s.handleDeleteGroup))
	mux.HandleFunc("PATCH /api/v1/admin/hosts/{id}", s.adminJSON(s.handleRenameHost))
	mux.HandleFunc("PATCH /api/v1/admin/hosts/{id}/group", s.adminJSON(s.handleSetHostGroup))
	mux.HandleFunc("POST /api/v1/admin/hosts/{id}/disable", s.adminJSON(s.handleDisableHost))
	mux.HandleFunc("POST /api/v1/admin/hosts/{id}/enable", s.adminJSON(s.handleEnableHost))
	mux.HandleFunc("DELETE /api/v1/admin/hosts/{id}", s.adminJSON(s.handleDeleteHost))
	mux.HandleFunc("GET /api/v1/admin/settings", s.adminJSON(s.handleGetSettings))
	mux.HandleFunc("PATCH /api/v1/admin/settings", s.adminJSON(s.handleUpdateSettings))
	mux.HandleFunc("POST /api/v1/admin/directory/test", s.adminJSON(s.handleTestDirectory))
	mux.HandleFunc("POST /api/v1/admin/pending/{hostname}/approve", s.adminJSON(s.handleApprovePending))
	mux.HandleFunc("DELETE /api/v1/admin/pending/{hostname}", s.adminJSON(s.handleDismissPending))
	mux.HandleFunc("GET /api/v1/admin/certificate", s.adminJSON(s.handleCertificate))
	mux.HandleFunc("GET /api/v1/admin/certificate.pem", s.adminJSON(s.handleCertificatePEM))
	mux.HandleFunc("POST /api/v1/admin/certificate", s.adminJSON(s.handleReplaceCertificate))

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /admin", s.handleAdmin)
	mux.HandleFunc("GET /account", s.handleAccount)
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("GET /app.css", s.handleCSS)
	mux.HandleFunc("GET /app.js", s.handleJS)
	mux.HandleFunc("GET /bootstrap.min.css", s.handleBootstrapCSS)
	mux.HandleFunc("GET /bootstrap.bundle.min.js", s.handleBootstrapJS)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return securityHeaders(mux)
}

// SetCertFiles records where a replaced agent certificate is written.
func (s *Server) SetCertFiles(certPath, keyPath string) {
	s.certMu.Lock()
	s.certPath = certPath
	s.keyPath = keyPath
	s.certMu.Unlock()
}

func (s *Server) certificate() *x509.Certificate {
	s.certMu.RLock()
	defer s.certMu.RUnlock()
	return s.agentCert
}

func (s *Server) setCertificate(cert *x509.Certificate) {
	s.certMu.Lock()
	s.agentCert = cert
	s.certMu.Unlock()
}

func (s *Server) certFiles() (string, string) {
	s.certMu.RLock()
	defer s.certMu.RUnlock()
	return s.certPath, s.keyPath
}

func (s *Server) sessionLength() time.Duration {
	return s.store.settingsSnapshot().Session()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": version.Version})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// data: is for the SVG icons shipped inside Bootstrap's own stylesheet.
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

// InsecureClient is the healthcheck client used when the server certificate name does not match 127.0.0.1.
func InsecureClient() *http.Client {
	return &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12},
		},
	}
}
