package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
		BrandName             *string `json:"brand_name"`
		BrandTagline          *string `json:"brand_tagline"`
		AutoRegister          *bool   `json:"auto_register"`
		DefaultGroupID        *string `json:"default_group_id"`
		Retention             *string `json:"retention"`
		Session               *string `json:"session"`
		Offline               *string `json:"offline"`
		LDAPEnabled           *bool   `json:"ldap_enabled"`
		LDAPURL               *string `json:"ldap_url"`
		LDAPStartTLS          *bool   `json:"ldap_starttls"`
		LDAPBindDN            *string `json:"ldap_bind_dn"`
		LDAPBindPassword      *string `json:"ldap_bind_password"`
		LDAPClearBindPassword *bool   `json:"ldap_clear_bind_password"`
		LDAPBaseDN            *string `json:"ldap_base_dn"`
		LDAPUserFilter        *string `json:"ldap_user_filter"`
		LDAPUsernameAttr      *string `json:"ldap_username_attr"`
		LDAPAdminGroup        *string `json:"ldap_admin_group"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if body.BrandName == nil && body.BrandTagline == nil && body.AutoRegister == nil && body.DefaultGroupID == nil && body.Retention == nil && body.Session == nil && body.Offline == nil &&
		body.LDAPEnabled == nil && body.LDAPURL == nil && body.LDAPStartTLS == nil && body.LDAPBindDN == nil && body.LDAPBindPassword == nil && body.LDAPClearBindPassword == nil &&
		body.LDAPBaseDN == nil && body.LDAPUserFilter == nil && body.LDAPUsernameAttr == nil && body.LDAPAdminGroup == nil {
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
	if msg := applyDirectory(&cfg, directoryInput{
		LDAPEnabled: body.LDAPEnabled, LDAPURL: body.LDAPURL, LDAPStartTLS: body.LDAPStartTLS,
		LDAPBindDN: body.LDAPBindDN, LDAPBindPassword: body.LDAPBindPassword,
		LDAPClearBindPassword: body.LDAPClearBindPassword, LDAPBaseDN: body.LDAPBaseDN,
		LDAPUserFilter: body.LDAPUserFilter, LDAPUsernameAttr: body.LDAPUsernameAttr,
		LDAPAdminGroup: body.LDAPAdminGroup,
	}); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := normalizeDirectory(&cfg); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.SaveSettings(cfg); err != nil {
		writeStoreError(w, err)
		return
	}
	s.writeSettings(w)
}

type directoryInput struct {
	LDAPEnabled           *bool   `json:"ldap_enabled"`
	LDAPURL               *string `json:"ldap_url"`
	LDAPStartTLS          *bool   `json:"ldap_starttls"`
	LDAPBindDN            *string `json:"ldap_bind_dn"`
	LDAPBindPassword      *string `json:"ldap_bind_password"`
	LDAPClearBindPassword *bool   `json:"ldap_clear_bind_password"`
	LDAPBaseDN            *string `json:"ldap_base_dn"`
	LDAPUserFilter        *string `json:"ldap_user_filter"`
	LDAPUsernameAttr      *string `json:"ldap_username_attr"`
	LDAPAdminGroup        *string `json:"ldap_admin_group"`
}

func applyDirectory(cfg *Settings, in directoryInput) string {
	if in.LDAPBindPassword != nil && *in.LDAPBindPassword != "" && in.LDAPClearBindPassword != nil && *in.LDAPClearBindPassword {
		return "cannot set and clear the bind password"
	}
	if in.LDAPEnabled != nil {
		cfg.LDAPEnabled = *in.LDAPEnabled
	}
	if in.LDAPURL != nil {
		cfg.LDAPURL = *in.LDAPURL
	}
	if in.LDAPStartTLS != nil {
		cfg.LDAPStartTLS = *in.LDAPStartTLS
	}
	if in.LDAPBindDN != nil {
		cfg.LDAPBindDN = *in.LDAPBindDN
	}
	if in.LDAPBaseDN != nil {
		cfg.LDAPBaseDN = *in.LDAPBaseDN
	}
	if in.LDAPUserFilter != nil {
		cfg.LDAPUserFilter = *in.LDAPUserFilter
	}
	if in.LDAPUsernameAttr != nil {
		cfg.LDAPUsernameAttr = *in.LDAPUsernameAttr
	}
	if in.LDAPAdminGroup != nil {
		cfg.LDAPAdminGroup = *in.LDAPAdminGroup
	}
	if in.LDAPClearBindPassword != nil && *in.LDAPClearBindPassword {
		cfg.LDAPBindPassword = ""
	} else if in.LDAPBindPassword != nil && *in.LDAPBindPassword != "" {
		cfg.LDAPBindPassword = *in.LDAPBindPassword
	}
	return ""
}

func (s *Server) handleTestDirectory(w http.ResponseWriter, r *http.Request) {
	var body directoryInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	cfg := s.store.settingsSnapshot()
	if msg := applyDirectory(&cfg, body); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if msg := normalizeDirectory(&cfg); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if cfg.LDAPURL == "" || cfg.LDAPBaseDN == "" {
		writeError(w, http.StatusBadRequest, "directory url and base are required")
		return
	}
	if err := probeDirectory(cfg); err != nil {
		switch {
		case errors.Is(err, errDirectoryConnect):
			writeError(w, http.StatusServiceUnavailable, "could not connect")
		case errors.Is(err, errDirectoryBind):
			writeError(w, http.StatusBadRequest, "bind failed")
		case errors.Is(err, errDirectoryBase):
			writeError(w, http.StatusBadRequest, "base DN was not found")
		case errors.Is(err, errDirectoryGroup):
			writeError(w, http.StatusBadRequest, "admin group was not found")
		default:
			slog.Warn("directory test", "err", err)
			writeError(w, http.StatusServiceUnavailable, "directory unavailable")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

func (s *Server) handlePSK(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.pskView(false))
}

func (s *Server) handleReplacePSK(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		ID     string `json:"psk_id"`
		Key    string `json:"psk_key"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	idPath, keyPath := s.pskFiles()
	if idPath == "" || keyPath == "" {
		writeError(w, http.StatusBadRequest, "psk storage is not configured")
		return
	}
	var key *agentcred.Key
	var err error
	switch body.Action {
	case "generate":
		key, err = agentcred.Generate()
	case "replace":
		key, err = agentcred.Parse(body.ID, body.Key)
	default:
		writeError(w, http.StatusBadRequest, "invalid action")
		return
	}
	if err != nil {
		slog.Warn("psk rejected", "err", err)
		writeError(w, http.StatusBadRequest, "invalid psk")
		return
	}
	if err := key.Write(idPath, keyPath); err != nil {
		slog.Error("write psk", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.setPSK(key)
	slog.Info("agent psk replaced", "id", key.ID(), "sha256", agentcred.Fingerprint(key), "generated", body.Action == "generate")
	writeJSON(w, http.StatusOK, s.pskView(body.Action == "generate"))
}

func (s *Server) pskView(includeKey bool) pskView {
	key := s.psk()
	idPath, keyPath := s.pskFiles()
	view := pskView{IDPath: idPath, KeyPath: keyPath}
	if key == nil {
		return view
	}
	view.ID = key.ID()
	view.Fingerprint = agentcred.Fingerprint(key)
	if includeKey {
		view.Key = key.Text()
	}
	return view
}

type pskView struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	IDPath      string `json:"id_path,omitempty"`
	KeyPath     string `json:"key_path,omitempty"`
	Key         string `json:"psk_key,omitempty"`
}
