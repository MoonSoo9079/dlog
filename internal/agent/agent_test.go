package agent

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/server"
)

func TestSameContainerAndSkip(t *testing.T) {
	id := strings.Repeat("ab", 32)
	if !sameContainer(id, id[:12]) {
		t.Fatal("prefix should match")
	}
	if sameContainer(id, "ffffffffffff") {
		t.Fatal("different id matched")
	}
	a := &Agent{
		cfg:    Config{Exclude: map[string]struct{}{"skip-me": {}}},
		selfID: id,
	}
	if !a.skip(Container{ID: id, Name: "agent"}) {
		t.Fatal("expected to skip self")
	}
	if !a.skip(Container{ID: "bbbbbbbbbbbb", Name: "skip-me"}) {
		t.Fatal("expected name exclude")
	}
	if !a.skip(Container{ID: "cccccccccccc", Name: "web", Labels: map[string]string{"dlog.exclude": "true"}}) {
		t.Fatal("expected label exclude")
	}
	if a.skip(Container{ID: "dddddddddddd", Name: "web", Labels: map[string]string{}}) {
		t.Fatal("expected to keep container")
	}
}

func TestContainerIDIgnoresOtherContainersOnTheHost(t *testing.T) {
	const other = "a440ad2a00297df642edf315bc4bf149dac621e18b77107410830c0e73e95344"
	cgroup := "0::/user.slice/user-1000.slice/session-1.scope\n0::/init.scope\n"
	mount := "/var/lib/docker/rootfs/overlayfs/" + other + " rw\n"
	if got := containerIDFrom(cgroup, ""); got != "" {
		t.Fatalf("host process took an id from nowhere: %s", got)
	}
	// mountinfo is passed only when the process is inside a container.
	if got := containerIDFrom(cgroup, mount); got != other {
		t.Fatalf("in-container mountinfo id: got %s", got)
	}
	self := strings.Repeat("ab", 32)
	if got := containerIDFrom("0::/docker/"+self, mount); got != self {
		t.Fatalf("cgroup id should win: got %s", got)
	}
}

func TestAgentShipsLogs(t *testing.T) {
	st, err := server.Open(t.TempDir() + "/dlog.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key, err := agentcred.Generate()
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(server.New(st, "admin-test-token", key.Certificate(), false, false).Handler())
	t.Cleanup(api.Close)

	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	docker := httptest.NewServer(fakeDocker(id))
	t.Cleanup(docker.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{
			ServerURL:         api.URL,
			Hostname:          "agent-test",
			StateDir:          t.TempDir(),
			AgentCert:         key.Pair(),
			Since:             time.Hour,
			BatchSize:         1,
			FlushInterval:     20 * time.Millisecond,
			HeartbeatInterval: time.Hour,
			Engine:            NewDockerHTTP(docker.URL, nil),
			HTTP:              &http.Client{},
		})
	}()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		hosts, err := st.ListHosts()
		if err != nil {
			t.Fatal(err)
		}
		if len(hosts) == 1 {
			lines, err := st.Logs(server.LogFilter{HostID: hosts[0].ID, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) > 0 && strings.Contains(lines[0].Message, "hello-dlog") && lines[0].ContainerName == "app" && lines[0].Stream == "stdout" {
				cancel()
				select {
				case err := <-errCh:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("agent did not stop")
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatal("log did not arrive")
}

func fakeDocker(id string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Version":"29.9.2","ApiVersion":"1.56","MinAPIVersion":"1.44"}`)
	})
	mux.HandleFunc("/v1.56/containers/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `[{"Id":"`+id+`","Names":["/app"],"Image":"demo:1","State":"running","Labels":{"app":"demo"}}]`)
	})
	mux.HandleFunc("/v1.56/containers/"+id+"/json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Id":"`+id+`","Name":"/app","Config":{"Image":"demo:1","Tty":false,"Labels":{"app":"demo"}},"State":{"Status":"running","Running":true}}`)
	})
	mux.HandleFunc("/v1.56/containers/"+id+"/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("follow") != "1" || r.URL.Query().Get("timestamps") != "1" {
			http.Error(w, "bad log request", http.StatusBadRequest)
			return
		}
		payload := time.Now().UTC().Format(time.RFC3339Nano) + " hello-dlog\n"
		frame := make([]byte, 8+len(payload))
		frame[0] = 1
		binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
		copy(frame[8:], payload)
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(frame); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("/v1.56/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	return mux
}
