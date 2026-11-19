package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"dlog/internal/protocol"
)

type Container struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	State      string            `json:"state"`
	Labels     map[string]string `json:"labels"`
	LastSeenAt time.Time         `json:"last_seen_at"`
}

func (s *Store) Heartbeat(hostID string, hb protocol.Heartbeat) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRow(`SELECT status FROM hosts WHERE id=?`, hostID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	if status != HostEnabled {
		return ErrDisabled
	}
	now := time.Now().UTC().UnixMilli()
	dropped := hb.Dropped
	if dropped < 0 {
		dropped = 0
	}
	if _, err := tx.Exec(`
UPDATE hosts SET last_seen_at=?, agent_version=?, docker_version=?, dropped=? WHERE id=?`,
		now, clipVersion(hb.AgentVersion), clipVersion(hb.DockerVersion), dropped, hostID); err != nil {
		return err
	}
	if hb.ReplaceContainers {
		if _, err := tx.Exec(`UPDATE containers SET state='exited' WHERE host_id=? AND state='running'`, hostID); err != nil {
			return err
		}
		for _, c := range hb.Containers {
			labels := "{}"
			if len(c.Labels) > 0 {
				if b, err := json.Marshal(clipLabels(c.Labels)); err == nil {
					labels = string(b)
				}
			}
			state := c.State
			if state == "" {
				state = "running"
			}
			if _, err := tx.Exec(`
INSERT INTO containers (host_id, container_id, name, image, labels, state, last_seen_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host_id, container_id) DO UPDATE SET
  name=excluded.name,
  image=excluded.image,
  labels=excluded.labels,
  state=excluded.state,
  last_seen_at=excluded.last_seen_at`,
				hostID, c.ID, c.Name, c.Image, labels, state, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) Containers(hostID string) ([]Container, error) {
	rows, err := s.db.Query(`
SELECT container_id, name, image, labels, state, last_seen_at
FROM containers WHERE host_id=?
ORDER BY CASE state WHEN 'running' THEN 0 ELSE 1 END, name`, hostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Container
	for rows.Next() {
		var c Container
		var labels string
		var seen int64
		if err := rows.Scan(&c.ID, &c.Name, &c.Image, &labels, &c.State, &seen); err != nil {
			return nil, err
		}
		c.LastSeenAt = time.UnixMilli(seen).UTC()
		c.Labels = map[string]string{}
		if labels != "" && labels != "{}" {
			_ = json.Unmarshal([]byte(labels), &c.Labels)
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Container{}
	}
	return out, rows.Err()
}

// DeleteContainer removes a container and its stored logs.
// A running container on an online host is refused with ErrContainerRunning.
func (s *Store) DeleteContainer(hostID, containerID string) error {
	host, err := s.Host(hostID)
	if err != nil {
		return err
	}
	var state string
	err = s.db.QueryRow(`SELECT state FROM containers WHERE host_id=? AND container_id=?`, hostID, containerID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state == "running" && host.Online {
		return ErrContainerRunning
	}
	for {
		res, err := s.db.Exec(`DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE host_id=? AND container_id=? LIMIT 5000)`, hostID, containerID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break
		}
	}
	var res sql.Result
	if host.Online {
		res, err = s.db.Exec(`DELETE FROM containers WHERE host_id=? AND container_id=? AND state<>'running'`, hostID, containerID)
	} else {
		res, err = s.db.Exec(`DELETE FROM containers WHERE host_id=? AND container_id=?`, hostID, containerID)
	}
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var cur string
		err := s.db.QueryRow(`SELECT state FROM containers WHERE host_id=? AND container_id=?`, hostID, containerID).Scan(&cur)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur == "running" && host.Online {
			return ErrContainerRunning
		}
	}
	return nil
}

// DeleteExitedContainers removes every inactive container for a host and its stored logs.
// When the host is online, running containers are kept.
func (s *Store) DeleteExitedContainers(hostID string) (int64, error) {
	host, err := s.Host(hostID)
	if err != nil {
		return 0, err
	}
	query := `SELECT container_id FROM containers WHERE host_id=? AND state<>'running'`
	if !host.Online {
		query = `SELECT container_id FROM containers WHERE host_id=?`
	}
	rows, err := s.db.Query(query, hostID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var deleted int64
	for _, cid := range ids {
		if err := s.DeleteContainer(hostID, cid); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrContainerRunning) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// PurgeExitedContainers deletes exited containers that have not been seen since before, along with their logs.
func (s *Store) PurgeExitedContainers(before time.Time) (int64, error) {
	rows, err := s.db.Query(`SELECT host_id, container_id FROM containers WHERE state<>'running' AND last_seen_at<?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	type pair struct{ hostID, containerID string }
	var targets []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.hostID, &p.containerID); err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var deleted int64
	for _, p := range targets {
		if err := s.DeleteContainer(p.hostID, p.containerID); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrContainerRunning) {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func clipLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	n := 0
	for k, v := range in {
		if n >= 64 {
			break
		}
		k = protocol.Clip(k, 128)
		v = protocol.Clip(v, 512)
		if k == "" {
			continue
		}
		out[k] = v
		n++
	}
	return out
}
