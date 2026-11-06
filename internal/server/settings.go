package server

import (
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	defaultRetention = 168 * time.Hour
	defaultSession   = 12 * time.Hour
	minSession       = time.Hour
	maxSession       = 30 * 24 * time.Hour
	minOffline       = 30 * time.Second
	maxOffline       = 15 * time.Minute
	maxRetention     = 10 * 365 * 24 * time.Hour

	defaultLDAPFilter = "(uid=%s)"
	defaultLDAPAttr   = "uid"
	maxBindPassword   = 1024
)

// Settings is the operator configuration stored in the database.
// Retention, session length, and the offline window survive a restart.
// The server's -retention flag is used only until a value is saved.
type Settings struct {
	BrandName        string `json:"brand_name"`
	BrandTagline     string `json:"brand_tagline"`
	AutoRegister     bool   `json:"auto_register"`
	DefaultGroupID   string `json:"default_group_id"`
	RetentionMS      int64  `json:"retention_ms"`
	SessionMS        int64  `json:"session_ms"`
	OfflineMS        int64  `json:"offline_ms"`
	LDAPEnabled      bool   `json:"ldap_enabled"`
	LDAPURL          string `json:"ldap_url"`
	LDAPStartTLS     bool   `json:"ldap_starttls"`
	LDAPBindDN       string `json:"ldap_bind_dn"`
	LDAPBindPassword string `json:"-"`
	LDAPBindSet      bool   `json:"ldap_bind_password_set"`
	LDAPBaseDN       string `json:"ldap_base_dn"`
	LDAPUserFilter   string `json:"ldap_user_filter"`
	LDAPUsernameAttr string `json:"ldap_username_attr"`
	LDAPAdminGroup   string `json:"ldap_admin_group"`
}

func (s Settings) Retention() time.Duration {
	if s.RetentionMS <= 0 {
		return 0
	}
	return time.Duration(s.RetentionMS) * time.Millisecond
}

func (s Settings) Session() time.Duration {
	d := time.Duration(s.SessionMS) * time.Millisecond
	if d < minSession || d > maxSession {
		return defaultSession
	}
	return d
}

func (s Settings) Offline() time.Duration {
	d := time.Duration(s.OfflineMS) * time.Millisecond
	if d < minOffline || d > maxOffline {
		return OnlineWindow
	}
	return d
}

func defaultSettings() Settings {
	return Settings{
		AutoRegister:     true,
		RetentionMS:      defaultRetention.Milliseconds(),
		SessionMS:        defaultSession.Milliseconds(),
		OfflineMS:        OnlineWindow.Milliseconds(),
		LDAPUserFilter:   defaultLDAPFilter,
		LDAPUsernameAttr: defaultLDAPAttr,
	}
}

var brandNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)

func validBrandName(s string) bool {
	return brandNameRE.MatchString(s)
}

func validTagline(s string) bool {
	if s == "" {
		return true
	}
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' || unicode.Is(unicode.Cc, r) {
			return false
		}
		n++
		if n > 80 {
			return false
		}
	}
	return true
}

var ldapAttrRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,31}$`)

func (cfg *Settings) fillDirectoryDefaults() {
	if strings.TrimSpace(cfg.LDAPUserFilter) == "" {
		cfg.LDAPUserFilter = defaultLDAPFilter
	}
	if strings.TrimSpace(cfg.LDAPUsernameAttr) == "" {
		cfg.LDAPUsernameAttr = defaultLDAPAttr
	}
	cfg.LDAPBindSet = cfg.LDAPBindPassword != ""
}

// normalizeDirectory trims directory fields, fills empty filter and attribute
// defaults, and reports the first problem. An empty string means the settings
// can be saved. Non-empty fields are checked even when the directory is off.
func normalizeDirectory(cfg *Settings) string {
	cfg.LDAPURL = strings.TrimSpace(cfg.LDAPURL)
	cfg.LDAPBindDN = strings.TrimSpace(cfg.LDAPBindDN)
	cfg.LDAPBaseDN = strings.TrimSpace(cfg.LDAPBaseDN)
	cfg.LDAPUserFilter = strings.TrimSpace(cfg.LDAPUserFilter)
	cfg.LDAPUsernameAttr = strings.TrimSpace(cfg.LDAPUsernameAttr)
	cfg.LDAPAdminGroup = strings.TrimSpace(cfg.LDAPAdminGroup)
	cfg.fillDirectoryDefaults()
	if len(cfg.LDAPBindPassword) > maxBindPassword {
		return "invalid bind password"
	}
	if cfg.LDAPURL != "" && !validLDAPURL(cfg.LDAPURL) {
		return "invalid directory url"
	}
	if cfg.LDAPStartTLS && !ldapScheme(cfg.LDAPURL) {
		return "starttls requires an ldap url"
	}
	if !validUserFilter(cfg.LDAPUserFilter) {
		return "invalid directory filter"
	}
	if !ldapAttrRE.MatchString(cfg.LDAPUsernameAttr) {
		return "invalid directory attribute"
	}
	if !validDirectoryDN(cfg.LDAPBindDN) || !validDirectoryDN(cfg.LDAPBaseDN) || !validDirectoryDN(cfg.LDAPAdminGroup) {
		return "invalid directory name"
	}
	if cfg.LDAPEnabled && (cfg.LDAPURL == "" || cfg.LDAPBaseDN == "") {
		return "directory url and base are required"
	}
	return ""
}

func ldapScheme(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "ldap"
}

func validLDAPURL(raw string) bool {
	if len(raw) > 512 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "ldap" && u.Scheme != "ldaps" {
		return false
	}
	return u.Hostname() != ""
}

func validUserFilter(s string) bool {
	if len(s) == 0 || len(s) > 512 || strings.Count(s, "%s") != 1 {
		return false
	}
	return !strings.Contains(strings.Replace(s, "%s", "", 1), "%")
}

func validDirectoryDN(s string) bool {
	if s == "" {
		return true
	}
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' || unicode.Is(unicode.Cc, r) {
			return false
		}
		n++
		if n > 512 {
			return false
		}
	}
	return n >= 1
}

// Retention is how long log lines are kept. Zero keeps them.
func (s *Store) Retention() time.Duration {
	return s.settingsSnapshot().Retention()
}

func (s *Store) settingsSnapshot() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.settingsLoaded {
		return defaultSettings()
	}
	return s.settings
}

func (s *Store) loadSettings() error {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		raw[key] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}
	cfg := defaultSettings()
	if v, ok := raw["brand_name"]; ok {
		cfg.BrandName = v
	}
	if v, ok := raw["brand_tagline"]; ok {
		cfg.BrandTagline = v
	}
	if v, ok := raw["auto_register"]; ok {
		cfg.AutoRegister = v != "0"
	}
	if v, ok := raw["default_group_id"]; ok {
		cfg.DefaultGroupID = v
	}
	if v, ok := raw["retention_ms"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 && time.Duration(n)*time.Millisecond <= maxRetention {
			cfg.RetentionMS = n
		}
	}
	if v, ok := raw["session_ms"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			d := time.Duration(n) * time.Millisecond
			if d >= minSession && d <= maxSession {
				cfg.SessionMS = n
			}
		}
	}
	if v, ok := raw["offline_ms"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			d := time.Duration(n) * time.Millisecond
			if d >= minOffline && d <= maxOffline {
				cfg.OfflineMS = n
			}
		}
	}
	if v, ok := raw["ldap_enabled"]; ok {
		cfg.LDAPEnabled = v == "1"
	}
	if v, ok := raw["ldap_url"]; ok {
		cfg.LDAPURL = v
	}
	if v, ok := raw["ldap_starttls"]; ok {
		cfg.LDAPStartTLS = v == "1"
	}
	if v, ok := raw["ldap_bind_dn"]; ok {
		cfg.LDAPBindDN = v
	}
	if v, ok := raw["ldap_bind_password"]; ok {
		cfg.LDAPBindPassword = v
	}
	if v, ok := raw["ldap_base_dn"]; ok {
		cfg.LDAPBaseDN = v
	}
	if v, ok := raw["ldap_user_filter"]; ok {
		cfg.LDAPUserFilter = v
	}
	if v, ok := raw["ldap_username_attr"]; ok {
		cfg.LDAPUsernameAttr = v
	}
	if v, ok := raw["ldap_admin_group"]; ok {
		cfg.LDAPAdminGroup = v
	}
	cfg.fillDirectoryDefaults()
	s.mu.Lock()
	s.settings = cfg
	s.settingsLoaded = true
	s.mu.Unlock()
	return nil
}

// SeedRetention stores the process retention the first time, and leaves a
// value already saved from the settings page alone.
func (s *Store) SeedRetention(d time.Duration) error {
	if d < 0 {
		d = 0
	}
	if d > maxRetention {
		d = maxRetention
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES ('retention_ms', ?) ON CONFLICT(key) DO NOTHING`, strconv.FormatInt(d.Milliseconds(), 10))
	if err != nil {
		return err
	}
	return s.loadSettings()
}

func (s *Store) SaveSettings(cfg Settings) error {
	auto := "0"
	if cfg.AutoRegister {
		auto = "1"
	}
	cfg.fillDirectoryDefaults()
	ldapEnabled := "0"
	if cfg.LDAPEnabled {
		ldapEnabled = "1"
	}
	startTLS := "0"
	if cfg.LDAPStartTLS {
		startTLS = "1"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	pairs := []struct{ key, value string }{
		{"brand_name", cfg.BrandName},
		{"brand_tagline", cfg.BrandTagline},
		{"auto_register", auto},
		{"default_group_id", cfg.DefaultGroupID},
		{"retention_ms", strconv.FormatInt(cfg.RetentionMS, 10)},
		{"session_ms", strconv.FormatInt(cfg.SessionMS, 10)},
		{"offline_ms", strconv.FormatInt(cfg.OfflineMS, 10)},
		{"ldap_enabled", ldapEnabled},
		{"ldap_url", cfg.LDAPURL},
		{"ldap_starttls", startTLS},
		{"ldap_bind_dn", cfg.LDAPBindDN},
		{"ldap_bind_password", cfg.LDAPBindPassword},
		{"ldap_base_dn", cfg.LDAPBaseDN},
		{"ldap_user_filter", cfg.LDAPUserFilter},
		{"ldap_username_attr", cfg.LDAPUsernameAttr},
		{"ldap_admin_group", cfg.LDAPAdminGroup},
	}
	for _, pair := range pairs {
		_, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, pair.key, pair.value)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = cfg
	s.settingsLoaded = true
	s.mu.Unlock()
	return nil
}

// PendingHost is a hostname that signed a registration while automatic
// registration was off. Approving it creates the host. The agent gets a
// token the next time it registers.
type PendingHost struct {
	Hostname     string    `json:"hostname"`
	AgentVersion string    `json:"agent_version"`
	RequestedAt  time.Time `json:"requested_at"`
}

func (s *Store) ListPending() ([]PendingHost, error) {
	rows, err := s.db.Query(`SELECT hostname, agent_version, requested_at FROM pending_hosts ORDER BY requested_at, hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingHost{}
	for rows.Next() {
		var item PendingHost
		var requested int64
		if err := rows.Scan(&item.Hostname, &item.AgentVersion, &requested); err != nil {
			return nil, err
		}
		item.RequestedAt = time.UnixMilli(requested).UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DismissPending(hostname string) error {
	res, err := s.db.Exec(`DELETE FROM pending_hosts WHERE hostname=?`, hostname)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ApprovePending creates a host for a waiting hostname, in the default group
// when one is set. A hostname that already exists is left as it is.
func (s *Store) ApprovePending(hostname string) (Host, error) {
	var version string
	err := s.db.QueryRow(`SELECT agent_version FROM pending_hosts WHERE hostname=?`, hostname).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	if err != nil {
		return Host{}, err
	}
	var id string
	err = s.db.QueryRow(`SELECT id FROM hosts WHERE hostname=?`, hostname).Scan(&id)
	if err == nil {
		if _, err := s.db.Exec(`DELETE FROM pending_hosts WHERE hostname=?`, hostname); err != nil {
			return Host{}, err
		}
		return s.Host(id)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Host{}, err
	}
	host, _, _, err := s.registerHost(hostname, version, true)
	return host, err
}
