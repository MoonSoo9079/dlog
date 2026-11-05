package server

import (
	"database/sql"
	"errors"
	"regexp"
	"time"

	"dlog/internal/token"
)

// Group is a folder of hosts. A host belongs to at most one group.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	HostCount int       `json:"host_count"`
	CreatedAt time.Time `json:"created_at"`
}

var groupNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$`)

func validGroupName(s string) bool {
	return groupNameRE.MatchString(s)
}

func (s *Store) CreateGroup(name string) (Group, error) {
	id := token.NewID("grp_")
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO host_groups (id, name, created_at) VALUES (?, ?, ?)`, id, name, now.UnixMilli())
	if isUnique(err) {
		return Group{}, ErrConflict
	}
	if err != nil {
		return Group{}, err
	}
	return s.Group(id)
}

func (s *Store) RenameGroup(id, name string) error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM host_groups WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	_, err := s.db.Exec(`UPDATE host_groups SET name=? WHERE id=?`, name, id)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (s *Store) DeleteGroup(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE hosts SET group_id=NULL WHERE group_id=?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM host_groups WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) SetHostGroup(hostID, groupID string) error {
	if groupID != "" {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM host_groups WHERE id=?`, groupID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM hosts WHERE id=?`, hostID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	var err error
	if groupID == "" {
		_, err = s.db.Exec(`UPDATE hosts SET group_id=NULL WHERE id=?`, hostID)
	} else {
		_, err = s.db.Exec(`UPDATE hosts SET group_id=? WHERE id=?`, groupID, hostID)
	}
	return err
}

func (s *Store) Group(id string) (Group, error) {
	g, err := scanGroup(s.db.QueryRow(groupSelect+` WHERE g.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	return g, err
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(groupSelect + ` ORDER BY g.name COLLATE NOCASE, g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

const groupSelect = `
SELECT g.id, g.name, g.created_at,
       (SELECT COUNT(*) FROM hosts h WHERE h.group_id = g.id)
FROM host_groups g`

func scanGroup(row rowScanner) (Group, error) {
	var g Group
	var created int64
	err := row.Scan(&g.ID, &g.Name, &created, &g.HostCount)
	if err != nil {
		return Group{}, err
	}
	g.CreatedAt = time.UnixMilli(created).UTC()
	return g, nil
}
