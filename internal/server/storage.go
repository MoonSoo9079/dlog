package server

import (
	"database/sql"
	"os"
	"time"
)

type HostStorage struct {
	HostID      string `json:"host_id"`
	Hostname    string `json:"hostname"`
	DisplayName string `json:"display_name"`
	LogCount    int64  `json:"log_count"`
}

type StorageStats struct {
	DBBytes        int64         `json:"db_bytes"`
	WALBytes       int64         `json:"wal_bytes"`
	LogCount       int64         `json:"log_count"`
	HostCount      int           `json:"host_count"`
	ContainerCount int           `json:"container_count"`
	ExitedCount    int           `json:"exited_count"`
	OldestLogAt    *time.Time    `json:"oldest_log_at,omitempty"`
	NewestLogAt    *time.Time    `json:"newest_log_at,omitempty"`
	PerHost        []HostStorage `json:"per_host"`
}

func (s *Store) StorageStats() (StorageStats, error) {
	var out StorageStats
	if s.dbPath != "" {
		if info, err := os.Stat(s.dbPath); err == nil {
			out.DBBytes = info.Size()
		}
		if info, err := os.Stat(s.dbPath + "-wal"); err == nil {
			out.WALBytes = info.Size()
		}
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hosts`).Scan(&out.HostCount); err != nil {
		return StorageStats{}, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN state <> 'running' THEN 1 ELSE 0 END), 0) FROM containers`).Scan(&out.ContainerCount, &out.ExitedCount); err != nil {
		return StorageStats{}, err
	}
	var oldest, newest sql.NullInt64
	if err := s.db.QueryRow(`SELECT COUNT(*), MIN(ts), MAX(ts) FROM logs`).Scan(&out.LogCount, &oldest, &newest); err != nil {
		return StorageStats{}, err
	}
	if oldest.Valid {
		t := time.UnixMilli(oldest.Int64).UTC()
		out.OldestLogAt = &t
	}
	if newest.Valid {
		t := time.UnixMilli(newest.Int64).UTC()
		out.NewestLogAt = &t
	}
	rows, err := s.db.Query(`
SELECT h.id, h.hostname, h.display_name, COUNT(l.id) AS n
FROM hosts h
LEFT JOIN logs l ON l.host_id = h.id
GROUP BY h.id, h.hostname, h.display_name
ORDER BY n DESC, lower(CASE WHEN h.display_name <> '' THEN h.display_name ELSE h.hostname END)`)
	if err != nil {
		return StorageStats{}, err
	}
	defer rows.Close()
	out.PerHost = []HostStorage{}
	for rows.Next() {
		var item HostStorage
		if err := rows.Scan(&item.HostID, &item.Hostname, &item.DisplayName, &item.LogCount); err != nil {
			return StorageStats{}, err
		}
		out.PerHost = append(out.PerHost, item)
	}
	return out, rows.Err()
}

// Compact checkpoints the WAL and runs VACUUM to reclaim freed SQLite pages.
func (s *Store) Compact() error {
	s.compactMu.Lock()
	defer s.compactMu.Unlock()
	if err := execPragma(s.db, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return err
	}
	_ = execPragma(s.db, "PRAGMA wal_checkpoint(TRUNCATE)")
	return nil
}
