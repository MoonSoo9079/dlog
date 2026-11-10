package agent

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientConnectsWithoutCheckingCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	plain := &http.Client{Timeout: 2 * time.Second}
	if _, err := plain.Get(srv.URL); err == nil {
		t.Fatal("default client accepted the test certificate")
	}
	client, err := newHTTPClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Timeout = 2 * time.Second
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
