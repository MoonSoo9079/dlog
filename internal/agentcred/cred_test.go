package agentcred

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignVerifyAndFiles(t *testing.T) {
	k, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"hostname":"web-01"}`)
	sig, err := Sign(k, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(k, k.ID(), sig, body); err != nil {
		t.Fatal(err)
	}
	if err := Verify(k, k.ID(), sig, append(body, ' ')); err == nil {
		t.Fatal("tampered body was accepted")
	}
	if err := Verify(k, "other-id", sig, body); err == nil {
		t.Fatal("wrong id was accepted")
	}
	other, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(other, k.ID(), sig, body); err == nil {
		t.Fatal("other key was accepted")
	}

	dir := t.TempDir()
	idPath := filepath.Join(dir, "psk.id")
	keyPath := filepath.Join(dir, "psk.key")
	if err := k.Write(idPath, keyPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key mode %o", info.Mode().Perm())
	}
	loaded, gen, err := Ensure(idPath, keyPath)
	if err != nil || gen {
		t.Fatalf("ensure existing: gen=%v err=%v", gen, err)
	}
	if loaded.ID() != k.ID() || loaded.Text() != k.Text() {
		t.Fatal("loaded key differs")
	}
	sig, err = Sign(loaded, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(k, loaded.ID(), sig, body); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(loaded) == "" || Fingerprint(loaded) != Fingerprint(k) {
		t.Fatal("fingerprint")
	}
	again, err := Parse(loaded.ID(), loaded.Text())
	if err != nil || again.Text() != k.Text() {
		t.Fatalf("parse: %v", err)
	}

	freshDir := t.TempDir()
	made, gen, err := Ensure(filepath.Join(freshDir, "psk.id"), filepath.Join(freshDir, "psk.key"))
	if err != nil || !gen || made.ID() == "" {
		t.Fatalf("ensure new: gen=%v err=%v", gen, err)
	}
	partial := t.TempDir()
	if err := os.WriteFile(filepath.Join(partial, "psk.id"), []byte(k.ID()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(filepath.Join(partial, "psk.id"), filepath.Join(partial, "psk.key")); err == nil {
		t.Fatal("id without key was accepted")
	}
	if _, err := Parse("bad id", k.Text()); err == nil {
		t.Fatal("id with a space was accepted")
	}
	if _, err := Parse(k.ID(), "aaaa"); err == nil {
		t.Fatal("short key was accepted")
	}

	now := time.Now()
	if !Fresh(now, now.Add(Window)) || Fresh(now.Add(-Window-time.Second), now) {
		t.Fatal("fresh window")
	}
}
