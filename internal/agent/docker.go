package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is an Engine backed by the Docker HTTP API over a Unix socket.
// api is the daemon's current API version, for example "v1.56". Docker 29
// rejects clients that still call /v1.41.
type Client struct {
	http *http.Client
	base string

	mu     sync.Mutex
	api    string
	logged bool
}

var apiVersionRE = regexp.MustCompile(`^1\.\d+$`)

func NewDocker(socketPath string) (*Client, error) {
	path := strings.TrimPrefix(socketPath, "unix://")
	if path == "" {
		return nil, errors.New("docker socket path is empty")
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "unix", path)
			if err != nil && errors.Is(err, os.ErrPermission) {
				return nil, fmt.Errorf("permission denied opening %s: this socket is usually mode 0660 and group docker. Add your user to the docker group and start a new login, or run the agent container as root with the socket mounted: %w", path, err)
			}
			return conn, err
		},
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	}
	return &Client{
		http: &http.Client{Transport: transport},
		base: "http://docker",
	}, nil
}

// NewDockerHTTP builds a client for tests or a TCP daemon.
func NewDockerHTTP(base string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{}
	}
	return &Client{http: client, base: strings.TrimRight(base, "/")}
}

func (c *Client) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body struct {
		Version string `json:"Version"`
	}
	if err := c.getJSON(ctx, "/version", &body); err != nil {
		return "", err
	}
	return body.Version, nil
}

func (c *Client) List(ctx context.Context) ([]Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var items []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Image  string            `json:"Image"`
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
	}
	if err := c.getJSON(ctx, "/containers/json", &items); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(items))
	for _, it := range items {
		out = append(out, Container{
			ID:     it.ID,
			Name:   firstName(it.Names),
			Image:  it.Image,
			State:  it.State,
			Labels: orEmpty(it.Labels),
		})
	}
	return out, nil
}

func (c *Client) Inspect(ctx context.Context, id string) (Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !safeID(id) {
		return Container{}, fmt.Errorf("invalid container id")
	}
	var body struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Tty    bool              `json:"Tty"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
	}
	if err := c.getJSON(ctx, "/containers/"+id+"/json", &body); err != nil {
		return Container{}, err
	}
	return Container{
		ID:     body.ID,
		Name:   strings.TrimPrefix(body.Name, "/"),
		Image:  body.Config.Image,
		State:  body.State.Status,
		Labels: orEmpty(body.Config.Labels),
		TTY:    body.Config.Tty,
	}, nil
}

func (c *Client) Logs(ctx context.Context, id string, since time.Time) (io.ReadCloser, error) {
	if !safeID(id) {
		return nil, fmt.Errorf("invalid container id")
	}
	q := url.Values{}
	q.Set("follow", "1")
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("timestamps", "1")
	if !since.IsZero() {
		q.Set("since", strconv.FormatInt(since.Unix(), 10))
	}
	resp, err := c.do(ctx, "/containers/"+id+"/logs?"+q.Encode())
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (c *Client) Events(ctx context.Context) (<-chan Event, error) {
	filters := `{"type":{"container":true},"event":{"start":true,"die":true,"destroy":true,"remove":true,"rename":true,"unpause":true,"restart":true}}`
	q := url.Values{}
	q.Set("filters", filters)
	resp, err := c.do(ctx, "/events?"+q.Encode())
	if err != nil {
		return nil, err
	}
	ch := make(chan Event, 16)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		dec := json.NewDecoder(resp.Body)
		for {
			var ev Event
			if err := dec.Decode(&ev); err != nil {
				return
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (c *Client) getJSON(ctx context.Context, path string, dest any) error {
	resp, err := c.do(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(dest)
}

// apiPrefix learns the daemon's current API version from GET /version.
// Docker 29's minimum is 1.44, so a hardcoded /v1.41 list or logs call is rejected
// and the agent never follows containers that are already running.
func (c *Client) apiPrefix(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.api != "" {
		api := c.api
		c.mu.Unlock()
		return api, nil
	}
	c.mu.Unlock()

	var body struct {
		Version    string `json:"Version"`
		APIVersion string `json:"ApiVersion"`
		APIAlt     string `json:"APIVersion"`
	}
	if err := c.getJSON(ctx, "/version", &body); err != nil {
		return "", err
	}
	ver := body.APIVersion
	if ver == "" {
		ver = body.APIAlt
	}
	if !apiVersionRE.MatchString(ver) {
		return "", fmt.Errorf("docker /version did not report an API version (engine %s)", body.Version)
	}
	api := "/v" + ver
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == "" {
		c.api = api
	}
	if !c.logged {
		c.logged = true
		slog.Info("docker api", "version", ver, "engine", body.Version)
	}
	return c.api, nil
}

func (c *Client) do(ctx context.Context, path string) (*http.Response, error) {
	if path != "/version" {
		prefix, err := c.apiPrefix(ctx)
		if err != nil {
			return nil, err
		}
		path = prefix + path
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotFound
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("docker %s: %s: %s", path, resp.Status, bytesTrim(b))
	}
	return resp, nil
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func safeID(id string) bool {
	if id == "" || len(id) > 128 || strings.Contains(id, "/") {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func bytesTrim(b []byte) string {
	return strings.TrimSpace(string(b))
}
