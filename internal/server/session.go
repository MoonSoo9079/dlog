package server

import (
	"database/sql"
	"errors"
	"time"

	"dlog/internal/token"
)

func (s *Store) CreateSession(userID string, ttl time.Duration) (string, time.Time, error) {
	if userID == "" {
		return "", time.Time{}, ErrInvalid
	}
	raw := token.New("ses_")
	exp := time.Now().Add(ttl).UTC()
	_, err := s.db.Exec(`INSERT INTO sessions (id_hash, user_id, expires_at) VALUES (?, ?, ?)`, token.Hash(raw), userID, exp.UnixMilli())
	return raw, exp, err
}

func (s *Store) TouchSession(raw string, ttl time.Duration) (Account, bool, error) {
	if raw == "" {
		return Account{}, false, nil
	}
	now := time.Now()
	var acct Account
	var created int64
	err := s.db.QueryRow(`
SELECT u.id, u.username, u.role, u.created_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id_hash=? AND s.expires_at>?`, token.Hash(raw), now.UnixMilli()).
		Scan(&acct.ID, &acct.Username, &acct.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, err
	}
	acct.CreatedAt = time.UnixMilli(created).UTC()
	if _, err := s.db.Exec(`UPDATE sessions SET expires_at=? WHERE id_hash=?`, now.Add(ttl).UnixMilli(), token.Hash(raw)); err != nil {
		return Account{}, false, err
	}
	return acct, true, nil
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
