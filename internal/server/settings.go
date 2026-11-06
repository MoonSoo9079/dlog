package server

import (
	"database/sql"
	"errors"
	"regexp"
	"strconv"
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
)

// Settings is the operator configuration stored in the database.
// Retention, session length, and the offline window survive a restart.
// The server's -retention flag is used only until a value is saved.
type Settings struct {
	BrandName      string `json:"brand_name"`
	BrandTagline   string `json:"brand_tagline"`
	AutoRegister   bool   `json:"auto_register"`
	DefaultGroupID string `json:"default_group_id"`
	RetentionMS    int64  `json:"retention_ms"`
	SessionMS      int64  `json:"session_ms"`
	OfflineMS      int64  `json:"offline_ms"`
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
		AutoRegister: true,
		RetentionMS:  defaultRetention.Milliseconds(),
		SessionMS:    defaultSession.Milliseconds(),
		OfflineMS:    OnlineWindow.Milliseconds(),
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
