package server

import (
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
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
	HostIDs     []string // When HostID == "": nil means all hosts; non-nil restricts to those host IDs.
	ContainerID string
	Stream      string
	Query       string
	Exclude     string
	Level       string
	Since       *time.Time
	Until       *time.Time
	CursorID    int64
	Mode        LogMode
	Limit       int
}

func (s *Store) Ingest(hostID string, records []protocol.Record) (int, error) {
	accepted, _, err := s.IngestWithLines(hostID, records)
	return accepted, err
}

func (s *Store) IngestWithLines(hostID string, records []protocol.Record) (int, []protocol.LogLine, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()

	var status, hostname, displayName string
	err = tx.QueryRow(`SELECT status, hostname, display_name FROM hosts WHERE id=?`, hostID).Scan(&status, &hostname, &displayName)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrUnauthorized
	}
	if err != nil {
		return 0, nil, err
	}
	if status != HostEnabled {
		return 0, nil, ErrDisabled
	}
	hostLabel := strings.TrimSpace(displayName)
	if hostLabel == "" {
		hostLabel = hostname
	}
	now := time.Now().UTC().UnixMilli()
	accepted := 0
	var inserted []protocol.LogLine
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
			return 0, nil, err
		}
		res, err := tx.Exec(`
INSERT INTO logs (host_id, container_id, container_name, image, stream, ts, message, line_key)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(host_id, line_key) DO NOTHING`,
			hostID, r.ContainerID, r.ContainerName, r.Image, r.Stream, r.Timestamp.UnixMilli(), r.Message, lineKey(r))
		if err != nil {
			return 0, nil, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			accepted += int(n)
			id, _ := res.LastInsertId()
			inserted = append(inserted, protocol.LogLine{
				ID:            id,
				HostID:        hostID,
				HostName:      hostLabel,
				ContainerID:   r.ContainerID,
				ContainerName: r.ContainerName,
				Image:         r.Image,
				Stream:        r.Stream,
				Timestamp:     r.Timestamp.UTC(),
				Message:       r.Message,
			})
		}
	}
	if _, err := tx.Exec(`UPDATE hosts SET last_seen_at=? WHERE id=?`, now, hostID); err != nil {
		return 0, nil, err
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return accepted, inserted, nil
}

// lineKey identifies one line for a host. A retried batch carries the same
// key, so it does not create a second row. Two identical lines in the same
// millisecond share a key and are stored once.
func lineKey(r protocol.Record) string {
	sum := sha256.New()
	sum.Write([]byte(r.ContainerID))
	sum.Write([]byte{0})
	sum.Write([]byte(r.Stream))
	sum.Write([]byte{0})
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(r.Timestamp.UnixMilli()))
	sum.Write(ts[:])
	sum.Write([]byte{0})
	sum.Write([]byte(r.Message))
	return hex.EncodeToString(sum.Sum(nil))
}

func (s *Store) Logs(f LogFilter) ([]protocol.LogLine, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 300
	}
	q, args, empty := buildLogQuery(f, f.Limit, false)
	if empty {
		return []protocol.LogLine{}, nil
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.LogLine
	for rows.Next() {
		var ln protocol.LogLine
		var ts int64
		if err := rows.Scan(&ln.ID, &ln.HostID, &ln.HostName, &ln.ContainerID, &ln.ContainerName, &ln.Image, &ln.Stream, &ts, &ln.Message); err != nil {
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

// StreamLogs iterates up to max matching lines in chronological order for export.
func (s *Store) StreamLogs(f LogFilter, max int, fn func(protocol.LogLine) error) error {
	if max <= 0 {
		max = 10000
	}
	q, args, empty := buildLogQuery(f, max, true)
	if empty {
		return nil
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ln protocol.LogLine
		var ts int64
		if err := rows.Scan(&ln.ID, &ln.HostID, &ln.HostName, &ln.ContainerID, &ln.ContainerName, &ln.Image, &ln.Stream, &ts, &ln.Message); err != nil {
			return err
		}
		ln.Timestamp = time.UnixMilli(ts).UTC()
		if err := fn(ln); err != nil {
			return err
		}
	}
	return rows.Err()
}

func buildLogQuery(f LogFilter, limit int, ascending bool) (string, []any, bool) {
	var where []string
	var args []any
	if f.HostID != "" {
		where = append(where, "l.host_id=?")
		args = append(args, f.HostID)
	} else if f.HostIDs != nil {
		if len(f.HostIDs) == 0 {
			return "", nil, true
		}
		ph := make([]string, len(f.HostIDs))
		for i, id := range f.HostIDs {
			ph[i] = "?"
			args = append(args, id)
		}
		where = append(where, "l.host_id IN ("+strings.Join(ph, ",")+")")
	}
	if f.ContainerID != "" {
		where = append(where, "l.container_id=?")
		args = append(args, f.ContainerID)
	}
	if f.Stream != "" {
		where = append(where, "l.stream=?")
		args = append(args, f.Stream)
	}
	if f.Query != "" {
		where = append(where, `l.message LIKE ? ESCAPE '\'`)
		args = append(args, likeContains(f.Query))
	}
	if f.Exclude != "" {
		where = append(where, `l.message NOT LIKE ? ESCAPE '\'`)
		args = append(args, likeContains(f.Exclude))
	}
	if f.Level != "" {
		if cond, largs := levelSQLCondition(f.Level); cond != "" {
			where = append(where, cond)
			args = append(args, largs...)
		}
	}
	if f.Since != nil {
		where = append(where, "l.ts>=?")
		args = append(args, f.Since.UnixMilli())
	}
	if f.Until != nil {
		where = append(where, "l.ts<=?")
		args = append(args, f.Until.UnixMilli())
	}
	order := "ORDER BY l.id DESC"
	if ascending {
		order = "ORDER BY l.id ASC"
	} else {
		switch f.Mode {
		case LogAfter:
			where = append(where, "l.id>?")
			args = append(args, f.CursorID)
			order = "ORDER BY l.id ASC"
		case LogBefore:
			where = append(where, "l.id<?")
			args = append(args, f.CursorID)
			order = "ORDER BY l.id DESC"
		}
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	q := `SELECT l.id, l.host_id, CASE WHEN h.display_name <> '' THEN h.display_name ELSE h.hostname END, l.container_id, l.container_name, l.image, l.stream, l.ts, l.message ` +
		`FROM logs l JOIN hosts h ON h.id = l.host_id` + clause + " " + order + " LIMIT ?"
	args = append(args, limit)
	return q, args, false
}

func validLogLevel(s string) bool {
	switch s {
	case "", "error", "warn", "info", "debug":
		return true
	default:
		return false
	}
}

func levelTokens(level string) []string {
	switch level {
	case "error":
		return []string{"error", "fatal", "panic", "crit", "level=err", `"level":"err`, "[err]", " err "}
	case "warn":
		return []string{"warn"}
	case "info":
		return []string{"info"}
	case "debug":
		return []string{"debug", "trace"}
	default:
		return nil
	}
}

func levelSQLCondition(level string) (string, []any) {
	toks := levelTokens(level)
	if len(toks) == 0 {
		return "", nil
	}
	parts := make([]string, len(toks))
	args := make([]any, len(toks))
	for i, tok := range toks {
		parts[i] = `LOWER(l.message) LIKE ? ESCAPE '\'`
		args[i] = likeContains(tok)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func matchesLevel(msg, level string) bool {
	if level == "" {
		return true
	}
	lower := strings.ToLower(msg)
	for _, tok := range levelTokens(level) {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func (s *Store) PurgeLogs(before time.Time) (int64, error) {
	cutoff := before.UnixMilli()
	var total int64
	for {
		res, err := s.db.Exec(`DELETE FROM logs WHERE id IN (SELECT id FROM logs WHERE ts<? LIMIT 5000)`, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < 5000 {
			return total, nil
		}
	}
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
