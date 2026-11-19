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

const sessionRefreshGap = time.Minute

func (s *Store) TouchSession(raw string, ttl time.Duration) (Account, bool, error) {
	acct, ok, _, err := s.touchSession(raw, ttl)
	return acct, ok, err
}

func (s *Store) touchSession(raw string, ttl time.Duration) (Account, bool, bool, error) {
	if raw == "" {
		return Account{}, false, false, nil
	}
	now := time.Now()
	var acct Account
	var created, expiresAt int64
	var all int
	err := s.db.QueryRow(`
SELECT u.id, u.username, u.role, u.all_hosts, u.source, u.created_at, s.expires_at
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.id_hash=? AND s.expires_at>?`, token.Hash(raw), now.UnixMilli()).
		Scan(&acct.ID, &acct.Username, &acct.Role, &all, &acct.Source, &created, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, false, nil
	}
	if err != nil {
		return Account{}, false, false, err
	}
	acct.AllHosts = all != 0
	acct.CreatedAt = time.UnixMilli(created).UTC()
	if err := s.fillAccess(&acct); err != nil {
		return Account{}, false, false, err
	}
	wantExp := now.Add(ttl).UnixMilli()
	// Avoid writing to SQLite on every 1-second log poll; extend when the
	// stored expiry falls more than a minute behind the sliding window.
	if wantExp-expiresAt < sessionRefreshGap.Milliseconds() {
		return acct, true, false, nil
	}
	if _, err := s.db.Exec(`UPDATE sessions SET expires_at=? WHERE id_hash=?`, wantExp, token.Hash(raw)); err != nil {
		return Account{}, false, false, err
	}
	return acct, true, true, nil
}

func (s *Store) DeleteSession(raw string) error {
	if raw == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id_hash=?`, token.Hash(raw))
	return err
}

// DeleteOtherSessions ends every session for userID except the one in keepRaw.
func (s *Store) DeleteOtherSessions(userID, keepRaw string) error {
	if userID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=? AND id_hash<>?`, userID, token.Hash(keepRaw))
	return err
}

// DeleteUserSessions ends every session for userID.
func (s *Store) DeleteUserSessions(userID string) error {
	if userID == "" {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

func (s *Store) PurgeSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at<=?`, time.Now().UnixMilli())
	return err
}
