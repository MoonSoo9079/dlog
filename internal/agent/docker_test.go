package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientUsesDaemonAPIVersion(t *testing.T) {
	var sawOld, sawNew bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			io.WriteString(w, `{"Version":"29.9.2","ApiVersion":"1.56","MinAPIVersion":"1.44"}`)
		case "/v1.41/containers/json":
			sawOld = true
			http.Error(w, `{"message":"client version 1.41 is too old. Minimum supported API version is 1.44"}`, http.StatusBadRequest)
		case "/v1.56/containers/json":
			sawNew = true
			io.WriteString(w, `[{"Id":"abc123","Names":["/app"],"Image":"demo:1","State":"running"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewDockerHTTP(srv.URL, srv.Client())
	list, err := c.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sawOld {
		t.Fatal("client called rejected API v1.41")
	}
	if !sawNew {
		t.Fatal("client did not call API v1.56")
	}
	if len(list) != 1 || list[0].Name != "app" || list[0].State != "running" {
		t.Fatalf("list: %+v", list)
	}
}

func TestDockerSocketPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses mode 000")
	}
	dir := t.TempDir()
	path := dir + "/docker.sock"
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ln.Close()
		os.Chmod(path, 0o700)
	})
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	c, err := NewDocker(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = c.Version(ctx)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "docker group") {
		t.Fatalf("error: %v", err)
	}
}
