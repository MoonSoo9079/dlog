package server

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"dlog/internal/token"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Account is an operator. The password hash never leaves the store.
// AllHosts is the scope for a user. Admins see every host either way.
// GroupIDs is the groups a user can see when AllHosts is false.
type Account struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	AllHosts  bool      `json:"all_hosts"`
	GroupIDs  []string  `json:"group_ids"`
	CreatedAt time.Time `json:"created_at"`
}

// SeesAll reports whether the account can read every host.
func (a Account) SeesAll() bool {
	return a.Role == RoleAdmin || a.AllHosts
}

// SeesHost reports whether the account can read a host in groupID.
// An empty groupID is a host with no group.
func (a Account) SeesHost(groupID string) bool {
	if a.SeesAll() {
		return true
	}
	if groupID == "" {
		return false
	}
	for _, id := range a.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

var (
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	dummyHash       []byte
)

func init() {
	hash, err := bcrypt.GenerateFromPassword([]byte("dlog-dummy-password"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	dummyHash = hash
}

func validUsername(s string) bool {
	return usernamePattern.MatchString(s)
}

func validRole(s string) bool {
	return s == RoleAdmin || s == RoleUser
}

func validPassword(s string) bool {
	n := utf8.RuneCountInString(s)
	return n >= 8 && n <= 72 && len(s) <= 72
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, password, role string) (Account, error) {
	username = strings.TrimSpace(username)
	if !validUsername(username) || !validPassword(password) || !validRole(role) {
		return Account{}, ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Account{}, err
	}
	acct := Account{
		ID:        token.NewID("usr_"),
		Username:  username,
		Role:      role,
		AllHosts:  true,
		GroupIDs:  []string{},
		CreatedAt: time.Now().UTC(),
	}
	_, err = s.db.Exec(`INSERT INTO users (id, username, password_hash, role, all_hosts, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		acct.ID, acct.Username, string(hash), acct.Role, acct.CreatedAt.UnixMilli())
	if isUnique(err) {
		return Account{}, ErrConflict
	}
	if err != nil {
		return Account{}, err
	}
	return acct, nil
}

func (s *Store) Authenticate(username, password string) (Account, error) {
	username = strings.TrimSpace(username)
	var hash string
	var created int64
	var all int
	var acct Account
	err := s.db.QueryRow(`SELECT id, username, password_hash, role, all_hosts, created_at FROM users WHERE username = ? COLLATE NOCASE`, username).
		Scan(&acct.ID, &acct.Username, &hash, &acct.Role, &all, &created)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return Account{}, ErrUnauthorized
	}
	if err != nil {
		return Account{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Account{}, ErrUnauthorized
	}
	acct.AllHosts = all != 0
	acct.CreatedAt = time.UnixMilli(created).UTC()
	if err := s.fillAccess(&acct); err != nil {
		return Account{}, err
	}
	return acct, nil
}

func (s *Store) User(id string) (Account, error) {
	var created int64
	var all int
	var acct Account
	err := s.db.QueryRow(`SELECT id, username, role, all_hosts, created_at FROM users WHERE id=?`, id).
		Scan(&acct.ID, &acct.Username, &acct.Role, &all, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, err
	}
	acct.AllHosts = all != 0
	acct.CreatedAt = time.UnixMilli(created).UTC()
	if err := s.fillAccess(&acct); err != nil {
		return Account{}, err
	}
	return acct, nil
}

func (s *Store) ListUsers() ([]Account, error) {
	rows, err := s.db.Query(`SELECT id, username, role, all_hosts, created_at FROM users ORDER BY username COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var created int64
		var all int
		var acct Account
		if err := rows.Scan(&acct.ID, &acct.Username, &acct.Role, &all, &created); err != nil {
			return nil, err
		}
		acct.AllHosts = all != 0
		acct.CreatedAt = time.UnixMilli(created).UTC()
		out = append(out, acct)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillAccess(&out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetAccess sets whether a user sees every host or only the named groups.
// allHosts clears the group list. An unknown group id is ErrNotFound.
// An empty group list with allHosts false sees no hosts.
func (s *Store) SetAccess(id string, allHosts bool, groupIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM users WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(groupIDs))
	for _, gid := range groupIDs {
		gid = strings.TrimSpace(gid)
		if gid == "" {
			return ErrInvalid
		}
		if _, ok := seen[gid]; ok {
			continue
		}
		seen[gid] = struct{}{}
		var n int
		if err := tx.QueryRow(`SELECT COUNT(1) FROM host_groups WHERE id=?`, gid).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		clean = append(clean, gid)
	}
	flag := 0
	if allHosts {
		flag = 1
		clean = nil
	}
	if _, err := tx.Exec(`UPDATE users SET all_hosts=? WHERE id=?`, flag, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_groups WHERE user_id=?`, id); err != nil {
		return err
	}
	for _, gid := range clean {
		if _, err := tx.Exec(`INSERT INTO user_groups (user_id, group_id) VALUES (?, ?)`, id, gid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) fillAccess(acct *Account) error {
	acct.GroupIDs = []string{}
	rows, err := s.db.Query(`SELECT group_id FROM user_groups WHERE user_id=? ORDER BY group_id`, acct.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var gid string
		if err := rows.Scan(&gid); err != nil {
			return err
		}
		acct.GroupIDs = append(acct.GroupIDs, gid)
	}
	return rows.Err()
}

func (s *Store) SetUserRole(id, role string) error {
	if !validRole(role) {
		return ErrInvalid
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	err = tx.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current == RoleAdmin && role != RoleAdmin {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role=?`, RoleAdmin).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(`UPDATE users SET role=? WHERE id=?`, role, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPassword(id, password string) error {
	if !validPassword(password) {
		return ErrInvalid
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, string(hash), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	err = tx.QueryRow(`SELECT role FROM users WHERE id=?`, id).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role == RoleAdmin {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role=?`, RoleAdmin).Scan(&n); err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
