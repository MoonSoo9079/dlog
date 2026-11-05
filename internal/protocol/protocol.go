// Package protocol is the JSON contract between dlog-agent and dlog-server.
package protocol

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxMessage    = 16 * 1024
	MaxImage      = 256
	MaxName       = 128
	MaxVersion    = 64
	MaxBatch      = 1000
	AgentBatchMax = 500
)

// Record is one container log line.
type Record struct {
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	Image         string    `json:"image"`
	Stream        string    `json:"stream"`
	Timestamp     time.Time `json:"timestamp"`
	Message       string    `json:"message"`
}

type IngestRequest struct {
	AgentVersion string   `json:"agent_version"`
	Records      []Record `json:"records"`
}

type IngestResponse struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

type ContainerSnapshot struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Image  string            `json:"image"`
	State  string            `json:"state"`
	Labels map[string]string `json:"labels,omitempty"`
}

type Heartbeat struct {
	AgentVersion      string              `json:"agent_version"`
	Hostname          string              `json:"hostname"`
	DockerVersion     string              `json:"docker_version,omitempty"`
	Dropped           int64               `json:"dropped"`
	Containers        []ContainerSnapshot `json:"containers,omitempty"`
	ReplaceContainers bool                `json:"replace_containers"`
}

type HeartbeatResponse struct {
	Status string `json:"status"`
}

// RegisterRequest is signed by the shared agent certificate.
// Timestamp limits how long a captured request can be replayed.
type RegisterRequest struct {
	Hostname     string    `json:"hostname"`
	AgentVersion string    `json:"agent_version"`
	Timestamp    time.Time `json:"timestamp"`
}

// RegisterResponse is returned to the agent. The host token stays on that
// agent; an operator never copies it.
type RegisterResponse struct {
	HostID    string `json:"host_id"`
	Hostname  string `json:"hostname"`
	HostToken string `json:"host_token"`
}

type LogLine struct {
	ID            int64     `json:"id"`
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container_name"`
	Image         string    `json:"image"`
	Stream        string    `json:"stream"`
	Timestamp     time.Time `json:"timestamp"`
	Message       string    `json:"message"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// NormalizeMessage strips NULs, replaces broken UTF-8, and caps the size.
func NormalizeMessage(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	if len(s) <= MaxMessage {
		return s
	}
	s = s[:MaxMessage]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

// Clip shortens s to at most max bytes without splitting a rune.
func Clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	s = s[:max]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
