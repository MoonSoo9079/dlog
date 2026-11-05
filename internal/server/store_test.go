package server

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"dlog/internal/protocol"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "dlog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sample(ts time.Time, msg string) protocol.Record {
	return protocol.Record{
		ContainerID:   "abc123abc123",
		ContainerName: "app",
		Image:         "demo:1",
		Stream:        "stdout",
		Timestamp:     ts,
		Message:       msg,
	}
}

func TestStoreHostAuthIngestQuery(t *testing.T) {
	s := testStore(t)
	host, raw, err := s.CreateHost("web-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateHost("nope"); err != ErrUnauthorized {
		t.Fatalf("bad token: %v", err)
	}
	got, err := s.AuthenticateHost(raw)
	if err != nil || got.ID != host.ID {
		t.Fatalf("auth: %+v %v", got, err)
	}
	now := time.Now().UTC()
	if _, err := s.Ingest(host.ID, []protocol.Record{sample(now.Add(-time.Second), "100% done"), sample(now, "hello")}); err != nil {
		t.Fatal(err)
	}
	latest, err := s.Logs(LogFilter{HostID: host.ID, Limit: 1})
	if err != nil || len(latest) != 1 || latest[0].Message != "hello" {
		t.Fatalf("latest: %+v %v", latest, err)
	}
	older, err := s.Logs(LogFilter{HostID: host.ID, Mode: LogBefore, CursorID: latest[0].ID, Limit: 10})
	if err != nil || len(older) != 1 || older[0].Message != "100% done" {
		t.Fatalf("older: %+v %v", older, err)
	}
	pct, err := s.Logs(LogFilter{HostID: host.ID, Query: "%"})
	if err != nil || len(pct) != 1 || pct[0].Message != "100% done" {
		t.Fatalf("like escape: %+v %v", pct, err)
	}
	hosts, err := s.ListHosts()
	if err != nil || len(hosts) != 1 || !hosts[0].Online || hosts[0].ContainerCount != 1 {
		t.Fatalf("hosts: %+v %v", hosts, err)
	}
	if err := s.SetHostStatus(host.ID, HostDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateHost(raw); err != ErrDisabled {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := s.Ingest(host.ID, []protocol.Record{sample(now, "nope")}); err != ErrDisabled {
		t.Fatalf("ingest disabled: %v", err)
	}
}

func TestRegisterAgainAndDelete(t *testing.T) {
	s := testStore(t)
	host, raw, created, err := s.Register("node-a", "0.1.0")
	if err != nil || !created || raw == "" || host.Hostname != "node-a" {
		t.Fatalf("register: %+v %s created=%v %v", host, raw, created, err)
	}
	if _, err := s.AuthenticateHost(raw); err != nil {
		t.Fatal(err)
	}
	again, raw2, created, err := s.Register("node-a", "0.1.0")
	if err != nil || created || again.ID != host.ID || raw2 == raw {
		t.Fatalf("re-register: %+v %s created=%v %v", again, raw2, created, err)
	}
	if _, err := s.AuthenticateHost(raw); err != ErrUnauthorized {
		t.Fatalf("old token: %v", err)
	}
	if _, err := s.AuthenticateHost(raw2); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.Ingest(host.ID, []protocol.Record{sample(now, "keep")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostStatus(host.ID, HostDisabled); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Register("node-a", "0.1.0"); err != ErrDisabled {
		t.Fatalf("disabled register: %v", err)
	}
	if err := s.DeleteHost(host.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Host(host.ID); err != ErrNotFound {
		t.Fatalf("host still present: %v", err)
	}
	ctrs, err := s.Containers(host.ID)
	if err != nil || len(ctrs) != 0 {
		t.Fatalf("containers survived delete: %+v %v", ctrs, err)
	}
	lines, err := s.Logs(LogFilter{HostID: host.ID})
	if err != nil || len(lines) != 0 {
		t.Fatalf("logs survived delete: %+v %v", lines, err)
	}
}

func TestGroups(t *testing.T) {
	s := testStore(t)
	a, _, err := s.CreateHost("a-host")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.CreateHost("b-host")
	if err != nil {
		t.Fatal(err)
	}
	edge, err := s.CreateGroup("Edge")
	if err != nil || edge.Name != "Edge" || edge.HostCount != 0 || len(edge.ID) < 5 {
		t.Fatalf("create: %+v %v", edge, err)
	}
	if _, err := s.CreateGroup("edge"); err != ErrConflict {
		t.Fatalf("case duplicate: %v", err)
	}
	if err := s.RenameGroup(edge.ID, "edge"); err != nil {
		t.Fatalf("case rename: %v", err)
	}
	other, err := s.CreateGroup("Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RenameGroup(other.ID, "EDGE"); err != ErrConflict {
		t.Fatalf("rename conflict: %v", err)
	}
	if err := s.SetHostGroup(a.ID, edge.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostGroup(a.ID, edge.ID); err != nil {
		t.Fatalf("same group again: %v", err)
	}
	if err := s.SetHostGroup(b.ID, "grp_missing"); err != ErrNotFound {
		t.Fatalf("missing group: %v", err)
	}
	if err := s.SetHostGroup("hst_missing", edge.ID); err != ErrNotFound {
		t.Fatalf("missing host: %v", err)
	}
	hosts, err := s.ListHosts()
	if err != nil || len(hosts) != 2 || hosts[0].GroupID != edge.ID || hosts[1].GroupID != "" {
		t.Fatalf("hosts: %+v %v", hosts, err)
	}
	groups, err := s.ListGroups()
	if err != nil || len(groups) != 2 || groups[0].Name != "edge" || groups[0].HostCount != 1 {
		t.Fatalf("groups: %+v %v", groups, err)
	}
	if err := s.DeleteGroup(edge.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(edge.ID); err != ErrNotFound {
		t.Fatalf("delete again: %v", err)
	}
	got, err := s.Host(a.ID)
	if err != nil || got.GroupID != "" {
		t.Fatalf("after delete: %+v %v", got, err)
	}
	if err := s.SetHostGroup(a.ID, ""); err != nil {
		t.Fatal(err)
	}
	groups, err = s.ListGroups()
	if err != nil || len(groups) != 1 || groups[0].ID != other.ID {
		t.Fatalf("remaining: %+v %v", groups, err)
	}
}

func TestMigrateHostGroups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dlog.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE hosts (
  id TEXT PRIMARY KEY,
  hostname TEXT NOT NULL UNIQUE,
  token_hash TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL DEFAULT 'enabled',
  agent_version TEXT NOT NULL DEFAULT '',
  docker_version TEXT NOT NULL DEFAULT '',
  metadata TEXT NOT NULL DEFAULT '{}',
  dropped INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER
)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO hosts (id, hostname, token_hash, status, created_at) VALUES ('hst_old', 'old-host', 'hash', 'enabled', 1700000000000)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	hosts, err := s.ListHosts()
	if err != nil || len(hosts) != 1 || hosts[0].Hostname != "old-host" || hosts[0].GroupID != "" {
		t.Fatalf("migrated hosts: %+v %v", hosts, err)
	}
	g, err := s.CreateGroup("Lab")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetHostGroup(hosts[0].ID, g.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Host(hosts[0].ID)
	if err != nil || got.GroupID != g.ID {
		t.Fatalf("assigned: %+v %v", got, err)
	}
}

func TestPurgeAndSession(t *testing.T) {
	s := testStore(t)
	host, _, err := s.CreateHost("web-02")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := s.Ingest(host.ID, []protocol.Record{sample(old, "old"), sample(time.Now().UTC(), "new")}); err != nil {
		t.Fatal(err)
	}
	n, err := s.PurgeLogs(time.Now().Add(-time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("purge %d %v", n, err)
	}
	lines, err := s.Logs(LogFilter{HostID: host.ID})
	if err != nil || len(lines) != 1 || lines[0].Message != "new" {
		t.Fatalf("after purge: %+v %v", lines, err)
	}
	raw, _, err := s.CreateSession(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.TouchSession(raw, time.Hour)
	if err != nil || !ok {
		t.Fatalf("touch: %v %v", ok, err)
	}
	if err := s.DeleteSession(raw); err != nil {
		t.Fatal(err)
	}
	ok, err = s.TouchSession(raw, time.Hour)
	if err != nil || ok {
		t.Fatalf("deleted session still valid: %v %v", ok, err)
	}
}
