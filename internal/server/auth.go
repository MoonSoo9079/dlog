package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"dlog/internal/token"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustProxy)
	if s.login.blocked(ip, 10, time.Minute) {
		s.loginFail(w, r, http.StatusTooManyRequests, "too many attempts")
		return
	}
	raw, jsonReq, err := readToken(w, r)
	if err != nil {
		s.loginFail(w, r, http.StatusBadRequest, "bad request")
		return
	}
	if s.admin == "" || !token.Equal(raw, s.admin) {
		s.login.hit(ip)
		s.loginFail(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	sid, exp, err := s.store.CreateSession(sessionTTL)
	if err != nil {
		slog.Error("create session", "err", err)
		s.loginFail(w, r, http.StatusInternalServerError, "internal error")
		return
	}
	s.setSessionCookie(w, r, sid, exp)
	if jsonReq {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") || strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) adminJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.allowAdmin(w, r) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) allowAdmin(w http.ResponseWriter, r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		ok, err := s.store.TouchSession(c.Value, sessionTTL)
		if err == nil && ok {
			s.setSessionCookie(w, r, c.Value, time.Now().Add(sessionTTL))
			return true
		}
	}
	got := bearer(r)
	if got != "" && s.admin != "" && token.Equal(got, s.admin) {
		return true
	}
	return false
}

func (s *Server) hostFromRequest(w http.ResponseWriter, r *http.Request) (Host, bool) {
	host, err := s.store.AuthenticateHost(bearer(r))
	if err != nil {
		if errors.Is(err, ErrDisabled) {
			writeError(w, http.StatusForbidden, "host disabled")
			return Host{}, false
		}
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return Host{}, false
	}
	return host, true
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, exp time.Time) {
	secure := s.secureCookie || r.TLS != nil || (s.trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

func (s *Server) loginFail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, status, msg)
		return
	}
	http.Redirect(w, r, "/login?error=1", http.StatusSeeOther)
}

func readToken(w http.ResponseWriter, r *http.Request) (string, bool, error) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", true, err
		}
		return strings.TrimSpace(body.Token), true, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", false, err
	}
	return strings.TrimSpace(r.FormValue("token")), false, nil
}

type slidingLimit struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *slidingLimit) blocked(key string, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(key, window)
	return len(l.hits[key]) >= limit
}

func (l *slidingLimit) hit(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	l.hits[key] = append(l.hits[key], time.Now())
}

func (l *slidingLimit) prune(key string, window time.Duration) {
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
		return
	}
	now := time.Now()
	old := l.hits[key]
	kept := old[:0]
	for _, t := range old {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	l.hits[key] = kept
}
