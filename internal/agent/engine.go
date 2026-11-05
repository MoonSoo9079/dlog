package agent

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned when the Docker daemon has no such container.
var ErrNotFound = errors.New("container not found")

// Container is the subset of container metadata the agent ships.
type Container struct {
	ID     string
	Name   string
	Image  string
	State  string
	Labels map[string]string
	TTY    bool
}

// Event is a Docker container event.
type Event struct {
	Type   string
	Action string
	Actor  struct {
		ID         string
		Attributes map[string]string
	}
}

// Engine talks to a Docker-compatible daemon.
type Engine interface {
	Version(ctx context.Context) (string, error)
	List(ctx context.Context) ([]Container, error)
	Inspect(ctx context.Context, id string) (Container, error)
	Logs(ctx context.Context, id string, since time.Time) (io.ReadCloser, error)
	Events(ctx context.Context) (<-chan Event, error)
}
