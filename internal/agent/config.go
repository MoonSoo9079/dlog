package agent

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/protocol"
)

// Config controls where the agent ships logs and how it authenticates.
type Config struct {
	ServerURL         string
	Hostname          string
	StateDir          string
	DockerHost        string
	Since             time.Duration
	BatchSize         int
	FlushInterval     time.Duration
	HeartbeatInterval time.Duration
	MaxBuffer         int
	IncludeSelf       bool
	Exclude           map[string]struct{}

	// PSK is the shared registration id and key.
	PSK agentcred.Key

	// Engine and HTTP override the Docker socket and HTTP client. Tests set them.
	Engine Engine
	HTTP   *http.Client
}

func (c *Config) applyDefaults() {
	if c.DockerHost == "" {
		c.DockerHost = "/var/run/docker.sock"
	}
	if c.Since == 0 {
		c.Since = 10 * time.Minute
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 200
	}
	if c.BatchSize > protocol.AgentBatchMax {
		c.BatchSize = protocol.AgentBatchMax
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = time.Second
	}
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 15 * time.Second
	}
	if c.MaxBuffer <= 0 {
		c.MaxBuffer = 10000
	}
	if c.MaxBuffer < c.BatchSize {
		c.MaxBuffer = c.BatchSize
	}
	if c.Exclude == nil {
		c.Exclude = map[string]struct{}{}
	}
}

func (c Config) validate() error {
	u, err := url.Parse(c.ServerURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("server URL must be http or https and include a host")
	}
	if !protocol.ValidHostname(c.Hostname) {
		return fmt.Errorf("invalid hostname %q", c.Hostname)
	}
	if c.StateDir == "" {
		return errors.New("state dir is required")
	}
	if _, err := agentcred.Sign(&c.PSK, []byte("ok")); err != nil {
		return errors.New("psk id and key are required")
	}
	return nil
}
