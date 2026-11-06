package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type principalKey struct{}

func principalFrom(r *http.Request) Account {
	acct, _ := r.Context().Value(principalKey{}).(Account)
	return acct
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustProxy)
	if s.login.blocked(ip, 10, time.Minute) {
		s.loginFail(w, r, http.StatusTooManyRequests, "too many attempts")
		return
	}
	username, password, jsonReq, err := readLogin(w, r)
	if err != nil {
		s.loginFail(w, r, http.StatusBadRequest, "bad request")
		return
	}
	acct, err := s.store.Authenticate(username, password)
	if err != nil {
		s.login.hit(ip)
		s.loginFail(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	sid, exp, err := s.store.CreateSession(acct.ID, s.sessionLength())
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

func (s *Server) operatorJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := s.principal(w, r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, acct)))
	}
}

func (s *Server) adminJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		acct, ok := s.principal(w, r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if acct.Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, acct)))
	}
}

func (s *Server) principal(w http.ResponseWriter, r *http.Request) (Account, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return Account{}, false
	}
	acct, ok, err := s.store.TouchSession(c.Value, s.sessionLength())
	if err != nil {
		slog.Error("session", "err", err)
		return Account{}, false
	}
	if !ok {
		return Account{}, false
	}
	s.setSessionCookie(w, r, c.Value, time.Now().Add(s.sessionLength()))
	return acct, true
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

func readLogin(w http.ResponseWriter, r *http.Request) (string, string, bool, error) {
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			return "", "", true, err
		}
		return strings.TrimSpace(body.Username), body.Password, true, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", "", false, err
	}
	return strings.TrimSpace(r.FormValue("username")), r.FormValue("password"), false, nil
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
