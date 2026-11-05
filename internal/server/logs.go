package server

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"dlog/internal/protocol"
)

type LogMode int

const (
	LogLatest LogMode = iota
	LogAfter
	LogBefore
)

type LogFilter struct {
	HostID      string
	ContainerID string
	Stream      string
	Query       string
	Since       *time.Time
	Until       *time.Time
	CursorID    int64
	Mode        LogMode
	Limit       int
}

func (s *Store) Ingest(hostID string, records []protocol.Record) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var status string
	err = tx.QueryRow(`SELECT status FROM hosts WHERE id=?`, hostID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrUnauthorized
	}
	if err != nil {
		return 0, err
	}
	if status != HostEnabled {
		return 0, ErrDisabled
	}
	now := time.Now().UTC().UnixMilli()
	for _, r := range records {
		if _, err := tx.Exec(`
INSERT INTO containers (host_id, container_id, name, image, labels, state, last_seen_at)
VALUES (?, ?, ?, ?, '{}', 'running', ?)
ON CONFLICT(host_id, container_id) DO UPDATE SET
  name=excluded.name,
  image=excluded.image,
  state='running',
  last_seen_at=excluded.last_seen_at`,
			hostID, r.ContainerID, r.ContainerName, r.Image, now); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`
INSERT INTO logs (host_id, container_id, container_name, image, stream, ts, message)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			hostID, r.ContainerID, r.ContainerName, r.Image, r.Stream, r.Timestamp.UnixMilli(), r.Message); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`UPDATE hosts SET last_seen_at=? WHERE id=?`, now, hostID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(records), nil
}

func (s *Store) Logs(f LogFilter) ([]protocol.LogLine, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 300
	}
	where := []string{"host_id=?"}
	args := []any{f.HostID}
	if f.ContainerID != "" {
		where = append(where, "container_id=?")
		args = append(args, f.ContainerID)
	}
	if f.Stream != "" {
		where = append(where, "stream=?")
		args = append(args, f.Stream)
	}
	if f.Query != "" {
		where = append(where, `message LIKE ? ESCAPE '\'`)
		args = append(args, likeContains(f.Query))
	}
	if f.Since != nil {
		where = append(where, "ts>=?")
		args = append(args, f.Since.UnixMilli())
	}
	if f.Until != nil {
		where = append(where, "ts<=?")
		args = append(args, f.Until.UnixMilli())
	}
	order := "ORDER BY id DESC"
	switch f.Mode {
	case LogAfter:
		where = append(where, "id>?")
		args = append(args, f.CursorID)
		order = "ORDER BY id ASC"
	case LogBefore:
		where = append(where, "id<?")
		args = append(args, f.CursorID)
		order = "ORDER BY id DESC"
	}
	q := `SELECT id, container_id, container_name, image, stream, ts, message FROM logs WHERE ` +
		strings.Join(where, " AND ") + " " + order + " LIMIT ?"
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.LogLine
	for rows.Next() {
		var ln protocol.LogLine
		var ts int64
		if err := rows.Scan(&ln.ID, &ln.ContainerID, &ln.ContainerName, &ln.Image, &ln.Stream, &ts, &ln.Message); err != nil {
			return nil, err
		}
		ln.Timestamp = time.UnixMilli(ts).UTC()
		out = append(out, ln)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []protocol.LogLine{}
	}
	if f.Mode != LogAfter {
		reverseLines(out)
	}
	return out, nil
}

func (s *Store) PurgeLogs(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM logs WHERE ts<?`, before.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func reverseLines(lines []protocol.LogLine) {
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
}

func likeContains(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, `%`, `\%`)
	q = strings.ReplaceAll(q, `_`, `\_`)
	return "%" + q + "%"
}
