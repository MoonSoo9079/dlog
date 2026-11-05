package token

import "testing"

func TestHashStableAndEqual(t *testing.T) {
	const raw = "ht_example"
	if Hash(raw) != Hash(raw) {
		t.Fatal("hash changed")
	}
	if !Equal(raw, raw) {
		t.Fatal("expected equal secrets to match")
	}
	if Equal(raw, raw+"x") {
		t.Fatal("different secrets matched")
	}
	if Equal("", "x") {
		t.Fatal("empty secret matched")
	}
}

func TestNewUnique(t *testing.T) {
	a := New("ht_")
	b := New("ht_")
	if a == b {
		t.Fatal("tokens collided")
	}
	if len(a) < 20 {
		t.Fatalf("token too short: %q", a)
	}
	id := NewID("hst_")
	if id == NewID("hst_") {
		t.Fatal("ids collided")
	}
}
