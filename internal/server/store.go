package server

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dlog/internal/protocol"

	_ "modernc.org/sqlite"
)

const (
	HostEnabled  = "enabled"
	HostDisabled = "disabled"
	OnlineWindow = 45 * time.Second
)

var (
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("already exists")
	ErrUnauthorized = errors.New("unauthorized")
	ErrDisabled     = errors.New("disabled")
	ErrLastAdmin    = errors.New("last admin")
	ErrInvalid      = errors.New("invalid")
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(abs) + "?mode=rwc&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if err := execPragma(db, q); err != nil {
			if strings.Contains(q, "journal_mode") {
				if err2 := execPragma(db, "PRAGMA journal_mode=DELETE"); err2 != nil {
					db.Close()
					return nil, fmt.Errorf("%s: %w", q, err)
				}
				continue
			}
			db.Close()
			return nil, fmt.Errorf("%s: %w", q, err)
		}
	}
	if err := execScript(db, schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateHostGroups(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateSessions(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateDisplayNames(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateLogKeys(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func execPragma(db *sql.DB, q string) error {
	rows, err := db.Query(q)
	if err != nil {
		return err
	}
	return rows.Close()
}

func execScript(db *sql.DB, script string) error {
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

const schema = `
CREATE TABLE IF NOT EXISTS host_groups (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_host_groups_name ON host_groups(name COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS hosts (
  id TEXT PRIMARY KEY,
  hostname TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL DEFAULT '',
  token_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'enabled',
  agent_version TEXT NOT NULL DEFAULT '',
  docker_version TEXT NOT NULL DEFAULT '',
  metadata TEXT NOT NULL DEFAULT '{}',
  dropped INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER,
  group_id TEXT REFERENCES host_groups(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS containers (
  host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  name TEXT NOT NULL,
  image TEXT NOT NULL DEFAULT '',
  labels TEXT NOT NULL DEFAULT '{}',
  state TEXT NOT NULL DEFAULT 'running',
  last_seen_at INTEGER NOT NULL,
  PRIMARY KEY (host_id, container_id)
);

CREATE TABLE IF NOT EXISTS logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  host_id TEXT NOT NULL REFERENCES hosts(id) ON DELETE CASCADE,
  container_id TEXT NOT NULL,
  container_name TEXT NOT NULL,
  image TEXT NOT NULL DEFAULT '',
  stream TEXT NOT NULL,
  ts INTEGER NOT NULL,
  message TEXT NOT NULL,
  line_key TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_logs_host_id ON logs(host_id, id);
CREATE INDEX IF NOT EXISTS idx_logs_host_ctr_id ON logs(host_id, container_id, id);
CREATE INDEX IF NOT EXISTS idx_logs_ts ON logs(ts);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS sessions (
  id_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
`

// migrateHostGroups adds hosts.group_id on databases created before groups
// existed. CREATE TABLE IF NOT EXISTS does not alter a live hosts table, and
// the group index cannot be in schema because that script runs first.
func migrateHostGroups(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(hosts)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "group_id" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !has {
		if _, err := db.Exec(`ALTER TABLE hosts ADD COLUMN group_id TEXT REFERENCES host_groups(id) ON DELETE SET NULL`); err != nil {
			return fmt.Errorf("hosts.group_id: %w", err)
		}
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_hosts_group ON hosts(group_id)`); err != nil {
		return fmt.Errorf("idx_hosts_group: %w", err)
	}
	return nil
}

// migrateSessions adds sessions.user_id on databases created before accounts.
// Sessions from the admin-token era have no user, so they are dropped.
func migrateSessions(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "user_id" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !has {
		if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN user_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("sessions.user_id: %w", err)
		}
	}
	if _, err := db.Exec(`DELETE FROM sessions WHERE user_id=''`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`); err != nil {
		return fmt.Errorf("idx_sessions_user: %w", err)
	}
	return nil
}

// migrateDisplayNames adds hosts.display_name. The hostname stays the
// registration key. The display name is only what the console shows.
func migrateDisplayNames(db *sql.DB) error {
	has, err := hasColumn(db, "hosts", "display_name")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE hosts ADD COLUMN display_name TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("hosts.display_name: %w", err)
	}
	return nil
}

// migrateLogKeys gives every stored line a stable key and keeps one row when
// a retry inserted the same line twice. The unique index is created here, not
// in schema, because an existing logs table does not have the column yet.
func migrateLogKeys(db *sql.DB) error {
	has, err := hasColumn(db, "logs", "line_key")
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.Exec(`ALTER TABLE logs ADD COLUMN line_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("logs.line_key: %w", err)
		}
	}
	for {
		rows, err := db.Query(`SELECT id, container_id, stream, ts, message FROM logs WHERE line_key='' LIMIT 200`)
		if err != nil {
			return err
		}
		type oldLine struct {
			id                  int64
			containerID, stream string
			ts                  int64
			message             string
		}
		var batch []oldLine
		for rows.Next() {
			var line oldLine
			if err := rows.Scan(&line.id, &line.containerID, &line.stream, &line.ts, &line.message); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, line)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(batch) == 0 {
			break
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		for _, line := range batch {
			key := lineKey(protocol.Record{
				ContainerID: line.containerID,
				Stream:      line.stream,
				Timestamp:   time.UnixMilli(line.ts).UTC(),
				Message:     line.message,
			})
			if _, err := tx.Exec(`UPDATE logs SET line_key=? WHERE id=?`, key, line.id); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`DELETE FROM logs WHERE id NOT IN (SELECT MIN(id) FROM logs GROUP BY host_id, line_key)`); err != nil {
		return fmt.Errorf("logs dedup: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_line ON logs(host_id, line_key)`); err != nil {
		return fmt.Errorf("idx_logs_line: %w", err)
	}
	return nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

func clipVersion(s string) string {
	return protocol.Clip(strings.TrimSpace(s), protocol.MaxVersion)
}
