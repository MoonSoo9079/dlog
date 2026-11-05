package server

import (
	"time"

	"dlog/internal/token"
)

func (s *Store) CreateSession(ttl time.Duration) (string, time.Time, error) {
	raw := token.New("ses_")
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(`INSERT INTO sessions (id_hash, expires_at) VALUES (?, ?)`, token.Hash(raw), exp.UnixMilli())
	return raw, exp, err
}

func (s *Store) TouchSession(raw string, ttl time.Duration) (bool, error) {
	if raw == "" {
		return false, nil
	}
	now := time.Now()
	res, err := s.db.Exec(`UPDATE sessions SET expires_at=? WHERE id_hash=? AND expires_at>?`,
		now.Add(ttl).UnixMilli(), token.Hash(raw), now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) DeleteSession(raw string) error {
	if raw == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id_hash=?`, token.Hash(raw))
	return err
}

func (s *Store) PurgeSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<=?`, time.Now().UnixMilli())
	return err
}
