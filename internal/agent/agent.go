package agent

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

var (
	errAuth     = errors.New("unauthorized")
	errDisabled = errors.New("host disabled")
	errDrop     = errors.New("drop batch")
)

// Run registers with the shared pre-shared key if needed, tails every running container, and pushes batches until ctx is cancelled.
// Delivery is at-least-once: a crash after the server accepts a batch and before the cursor is saved
// can send those lines again.
func Run(ctx context.Context, cfg Config) error {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}
	st, err := LoadState(cfg.StateDir)
	if err != nil {
		return err
	}
	httpClient := cfg.HTTP
	if httpClient == nil {
		httpClient, err = newHTTPClient(cfg.ServerURL)
		if err != nil {
			return err
		}
	}
	eng := cfg.Engine
	if eng == nil {
		eng, err = NewDocker(cfg.DockerHost)
		if err != nil {
			return err
		}
	}
	a := &Agent{
		cfg:    cfg,
		state:  st,
		eng:    eng,
		http:   httpClient,
		buf:    newBuffer(cfg.MaxBuffer),
		slots:  map[string]slot{},
		live:   map[string]*liveContainer{},
		selfID: ownContainerID(),
	}
	if a.selfID != "" && !cfg.IncludeSelf {
		short := a.selfID
		if len(short) > 12 {
			short = short[:12]
		}
		slog.Info("excluding this agent container", "id", short)
	}
	if err := a.waitIdentity(ctx); err != nil {
		return err
	}
	slog.Info("agent started", "server", cfg.ServerURL, "hostname", cfg.Hostname)

	a.wg.Add(4)
	go func() { defer a.wg.Done(); a.shipLoop(ctx) }()
	go func() { defer a.wg.Done(); a.heartbeatLoop(ctx) }()
	go func() { defer a.wg.Done(); a.eventLoop(ctx) }()
	go func() { defer a.wg.Done(); a.relistLoop(ctx) }()
	a.syncOnce(ctx)

	<-ctx.Done()
	a.wg.Wait()
	return ctx.Err()
}

type Agent struct {
	cfg    Config
	state  *StateFile
	eng    Engine
	http   *http.Client
	buf    *buffer
	selfID string

	mu            sync.Mutex
	wg            sync.WaitGroup
	slots         map[string]slot
	live          map[string]*liveContainer
	gen           int
	lastErr       time.Time
	errStreak     int
	discoverySeen bool
	discoveryKey  string
}

type slot struct {
	gen    int
	cancel context.CancelFunc
}

type liveContainer struct {
	mu sync.Mutex
	c  Container
}

func (l *liveContainer) snapshot() Container {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.c
}

func (l *liveContainer) update(c Container) {
	l.mu.Lock()
	l.c = c
	l.mu.Unlock()
}

func (l *liveContainer) rename(name string) {
	l.mu.Lock()
	l.c.Name = name
	l.mu.Unlock()
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
