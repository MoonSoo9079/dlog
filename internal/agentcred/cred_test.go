package agentcred

import (
	"bytes"
	"testing"
	"time"
)

func TestSignVerifyAndFiles(t *testing.T) {
	k, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"hostname":"web-01"}`)
	sig, err := Sign(k.Pair(), body)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(k.Certificate(), body, sig); err != nil {
		t.Fatal(err)
	}
	if err := Verify(k.Certificate(), append(body, ' '), sig); err == nil {
		t.Fatal("tampered body was accepted")
	}
	dir := t.TempDir()
	certPath := dir + "/agent.crt"
	keyPath := dir + "/agent.key"
	if err := k.Write(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	loaded, gen, err := Ensure(certPath, keyPath)
	if err != nil || gen {
		t.Fatalf("ensure existing: gen=%v err=%v", gen, err)
	}
	if !bytes.Equal(loaded.Raw, k.Certificate().Raw) {
		t.Fatal("loaded certificate differs")
	}
	pair, err := LoadPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	sig, err = Sign(pair, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(loaded, body, sig); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(loaded) == "" {
		t.Fatal("empty fingerprint")
	}
	now := time.Now()
	if !Fresh(now, now.Add(Window)) || Fresh(now.Add(-Window-time.Second), now) {
		t.Fatal("fresh window")
	}
}
