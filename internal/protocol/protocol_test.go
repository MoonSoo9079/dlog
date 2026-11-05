package protocol

import "testing"

func TestNormalizeMessage(t *testing.T) {
	if got := NormalizeMessage("a\x00b"); got != "ab" {
		t.Fatalf("nul: %q", got)
	}
	long := stringsRepeat("x", MaxMessage+50)
	got := NormalizeMessage(long)
	if len(got) <= MaxMessage || got[len(got)-len("…"):] != "…" {
		t.Fatalf("truncate: len=%d", len(got))
	}
	if !ValidHostname("web-01.example") {
		t.Fatal("expected hostname to be valid")
	}
	if ValidHostname("") || ValidHostname("has space") || ValidHostname("-bad") {
		t.Fatal("expected hostname to be rejected")
	}
}

func stringsRepeat(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
