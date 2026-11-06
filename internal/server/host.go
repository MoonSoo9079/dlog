package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"dlog/internal/token"
)

type Host struct {
	ID             string            `json:"id"`
	Hostname       string            `json:"hostname"`
	DisplayName    string            `json:"display_name"`
	Status         string            `json:"status"`
	Online         bool              `json:"online"`
	AgentVersion   string            `json:"agent_version"`
	DockerVersion  string            `json:"docker_version"`
	Dropped        int64             `json:"dropped"`
	CreatedAt      time.Time         `json:"created_at"`
	LastSeenAt     *time.Time        `json:"last_seen_at,omitempty"`
	ContainerCount int               `json:"container_count"`
	GroupID        string            `json:"group_id"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

func (s *Store) CreateHost(hostname string) (Host, string, error) {
	raw := token.New("ht_")
	host, err := s.insertHost(hostname, raw, "", "")
	return host, raw, err
}

func (s *Store) insertHost(hostname, raw, agentVersion, dockerVersion string) (Host, error) {
	now := time.Now().UTC()
	id := token.NewID("hst_")
	_, err := s.db.Exec(`
INSERT INTO hosts (id, hostname, token_hash, status, agent_version, docker_version, metadata, dropped, created_at)
VALUES (?, ?, ?, ?, ?, ?, '{}', 0, ?)`,
		id, hostname, token.Hash(raw), HostEnabled, agentVersion, dockerVersion, now.UnixMilli())
	if isUnique(err) {
		return Host{}, ErrConflict
	}
	if err != nil {
		return Host{}, err
	}
	return s.Host(id)
}

// Register creates a host or, when the hostname already exists and is enabled,
// issues a new host token for that host. A disabled host stays disabled.
// A new hostname is refused when automatic registration is off.
func (s *Store) Register(hostname, agentVersion string) (Host, string, bool, error) {
	return s.registerHost(hostname, agentVersion, false)
}

func (s *Store) registerHost(hostname, agentVersion string, force bool) (Host, string, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Host{}, "", false, err
	}
	defer tx.Rollback()

	raw := token.New("ht_")
	now := time.Now().UTC()
	var id, status string
	err = tx.QueryRow(`SELECT id, status FROM hosts WHERE hostname=?`, hostname).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		if !force && !s.settingsSnapshot().AutoRegister {
			_, err = tx.Exec(`
INSERT INTO pending_hosts (hostname, agent_version, requested_at) VALUES (?, ?, ?)
ON CONFLICT(hostname) DO UPDATE SET agent_version=excluded.agent_version, requested_at=excluded.requested_at`,
				hostname, clipVersion(agentVersion), now.UnixMilli())
			if err != nil {
				return Host{}, "", false, err
			}
			if err := tx.Commit(); err != nil {
				return Host{}, "", false, err
			}
			return Host{}, "", false, ErrRegistrationClosed
		}
		groupID, err := s.defaultGroupID(tx)
		if err != nil {
			return Host{}, "", false, err
		}
		id = token.NewID("hst_")
		var group any
		if groupID != "" {
			group = groupID
		}
		_, err = tx.Exec(`
INSERT INTO hosts (id, hostname, token_hash, status, agent_version, docker_version, metadata, dropped, created_at, group_id)
VALUES (?, ?, ?, ?, ?, '', '{}', 0, ?, ?)`,
			id, hostname, token.Hash(raw), HostEnabled, clipVersion(agentVersion), now.UnixMilli(), group)
		if isUnique(err) {
			return Host{}, "", false, ErrConflict
		}
		if err != nil {
			return Host{}, "", false, err
		}
		if _, err := tx.Exec(`DELETE FROM pending_hosts WHERE hostname=?`, hostname); err != nil {
			return Host{}, "", false, err
		}
		if err := tx.Commit(); err != nil {
			return Host{}, "", false, err
		}
		host, err := s.Host(id)
		return host, raw, true, err
	}
	if err != nil {
		return Host{}, "", false, err
	}
	if status != HostEnabled {
		return Host{}, "", false, ErrDisabled
	}
	if _, err := tx.Exec(`UPDATE hosts SET token_hash=?, agent_version=? WHERE id=?`, token.Hash(raw), clipVersion(agentVersion), id); err != nil {
		return Host{}, "", false, err
	}
	if _, err := tx.Exec(`DELETE FROM pending_hosts WHERE hostname=?`, hostname); err != nil {
		return Host{}, "", false, err
	}
	if err := tx.Commit(); err != nil {
		return Host{}, "", false, err
	}
	host, err := s.Host(id)
	return host, raw, false, err
}

func (s *Store) defaultGroupID(tx *sql.Tx) (string, error) {
	id := s.settingsSnapshot().DefaultGroupID
	if id == "" {
		return "", nil
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM host_groups WHERE id=?`, id).Scan(&n); err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return id, nil
}

func (s *Store) SetHostStatus(id, status string) error {
	res, err := s.db.Exec(`UPDATE hosts SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RenameHost(id, hostname string) error {
	res, err := s.db.Exec(`UPDATE hosts SET hostname=? WHERE id=?`, hostname, id)
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDisplayName sets the name the console shows. An empty name shows the
// hostname. Registration keeps looking the host up by hostname.
func (s *Store) SetDisplayName(id, name string) error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM hosts WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	_, err := s.db.Exec(`UPDATE hosts SET display_name=? WHERE id=?`, name, id)
	return err
}

func (s *Store) DeleteHost(id string) error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM hosts WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	for {
		res, err := s.db.Exec(`DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE host_id=? LIMIT 5000)`, id)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break
		}
	}
	_, err := s.db.Exec(`DELETE FROM hosts WHERE id=?`, id)
	return err
}

func (s *Store) Host(id string) (Host, error) {
	row := s.db.QueryRow(hostSelect+` WHERE h.id=?`, id)
	h, err := s.scanHostOnline(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrNotFound
	}
	return h, err
}

func (s *Store) ListHosts() ([]Host, error) {
	rows, err := s.db.Query(hostSelect + ` ORDER BY lower(CASE WHEN h.display_name <> '' THEN h.display_name ELSE h.hostname END), h.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := s.scanHostOnline(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if out == nil {
		out = []Host{}
	}
	return out, rows.Err()
}

const hostSelect = `
SELECT h.id, h.hostname, h.display_name, h.status, h.agent_version, h.docker_version, h.metadata, h.dropped,
       h.created_at, h.last_seen_at, COALESCE(h.group_id, ''),
       (SELECT COUNT(*) FROM containers c WHERE c.host_id = h.id)
FROM hosts h`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHost(row rowScanner) (Host, error) {
	var h Host
	var meta string
	var created int64
	var seen sql.NullInt64
	err := row.Scan(&h.ID, &h.Hostname, &h.DisplayName, &h.Status, &h.AgentVersion, &h.DockerVersion, &meta, &h.Dropped, &created, &seen, &h.GroupID, &h.ContainerCount)
	if err != nil {
		return Host{}, err
	}
	h.CreatedAt = time.UnixMilli(created).UTC()
	if seen.Valid {
		t := time.UnixMilli(seen.Int64).UTC()
		h.LastSeenAt = &t
	}
	if meta != "" && meta != "{}" {
		_ = json.Unmarshal([]byte(meta), &h.Metadata)
	}
	return h, nil
}

func (s *Store) scanHostOnline(row rowScanner) (Host, error) {
	h, err := scanHost(row)
	if err != nil {
		return Host{}, err
	}
	window := s.settingsSnapshot().Offline()
	h.Online = h.Status == HostEnabled && h.LastSeenAt != nil && time.Since(*h.LastSeenAt) <= window
	return h, nil
}

func (s *Store) AuthenticateHost(raw string) (Host, error) {
	if strings.TrimSpace(raw) == "" {
		return Host{}, ErrUnauthorized
	}
	row := s.db.QueryRow(hostSelect+` WHERE h.token_hash=?`, token.Hash(raw))
	h, err := s.scanHostOnline(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Host{}, ErrUnauthorized
	}
	if err != nil {
		return Host{}, err
	}
	if h.Status != HostEnabled {
		return h, ErrDisabled
	}
	return h, nil
}
