package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dlog/internal/agent"
	"dlog/internal/agentcred"
	"dlog/internal/cli"
)

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	cli.SetupLog(cfg.level)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx, cfg.agent); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

type parsed struct {
	agent agent.Config
	level string
}

func parseConfig(args []string) (parsed, error) {
	since, err := cli.Duration(os.Getenv("DLOG_SINCE"), 10*time.Minute)
	if err != nil {
		return parsed{}, fmt.Errorf("DLOG_SINCE: %w", err)
	}
	flush, err := cli.Duration(os.Getenv("DLOG_FLUSH"), time.Second)
	if err != nil {
		return parsed{}, fmt.Errorf("DLOG_FLUSH: %w", err)
	}
	heartbeat, err := cli.Duration(os.Getenv("DLOG_HEARTBEAT"), 15*time.Second)
	if err != nil {
		return parsed{}, fmt.Errorf("DLOG_HEARTBEAT: %w", err)
	}
	fs := flag.NewFlagSet("dlog-agent", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		hostnameFile string
		exclude      string
		pskID        string
		pskKey       string
		batch        int
		buffer       int
		level        string
	)
	out := parsed{agent: agent.Config{Since: since, FlushInterval: flush, HeartbeatInterval: heartbeat}}
	fs.StringVar(&out.agent.ServerURL, "server", cli.Env("DLOG_SERVER_URL", ""), "dlog server base URL")
	fs.StringVar(&pskID, "psk-id", cli.Env("DLOG_PSK_ID", ""), "shared PSK id, the same value on every agent")
	fs.StringVar(&pskKey, "psk-key", cli.Env("DLOG_PSK_KEY", ""), "shared PSK key, the same value on every agent")
	fs.StringVar(&out.agent.Hostname, "hostname", cli.Env("DLOG_HOSTNAME", ""), "stable name of this machine")
	fs.StringVar(&hostnameFile, "hostname-file", cli.Env("DLOG_HOSTNAME_FILE", ""), "file whose contents are the hostname, for example a mounted /etc/hostname")
	fs.StringVar(&out.agent.StateDir, "state-dir", cli.Env("DLOG_STATE_DIR", "data/agent"), "directory for the host token and log cursors")
	fs.StringVar(&out.agent.DockerHost, "docker-host", cli.Env("DLOG_DOCKER_HOST", "/var/run/docker.sock"), "Docker socket path or unix:// URL")
	fs.IntVar(&batch, "batch", envInt("DLOG_BATCH", 200), "lines per ingest request")
	fs.IntVar(&buffer, "buffer", envInt("DLOG_BUFFER", 10000), "maximum unsent lines kept in memory")
	fs.BoolVar(&out.agent.IncludeSelf, "include-self", cli.EnvBool("DLOG_INCLUDE_SELF", false), "also ship this agent container's own logs")
	fs.StringVar(&exclude, "exclude", cli.Env("DLOG_EXCLUDE", ""), "comma-separated container names to skip")
	fs.StringVar(&level, "log-level", cli.Env("DLOG_LOG_LEVEL", "info"), "debug, info, warn, or error")
	fs.Func("since", "on first attach, read logs newer than this duration (10m, 1h)", func(s string) error {
		d, err := cli.Duration(s, out.agent.Since)
		if err != nil {
			return err
		}
		out.agent.Since = d
		return nil
	})
	fs.Func("flush", "maximum time a line waits before it is sent", func(s string) error {
		d, err := cli.Duration(s, out.agent.FlushInterval)
		if err != nil {
			return err
		}
		out.agent.FlushInterval = d
		return nil
	})
	fs.Func("heartbeat", "how often to report host status", func(s string) error {
		d, err := cli.Duration(s, out.agent.HeartbeatInterval)
		if err != nil {
			return err
		}
		out.agent.HeartbeatInterval = d
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return parsed{}, err
	}
	if pskID == "" || pskKey == "" {
		return parsed{}, errors.New("psk id and key are required")
	}
	psk, err := agentcred.Parse(pskID, pskKey)
	if err != nil {
		return parsed{}, err
	}
	out.agent.PSK = *psk
	out.agent.BatchSize = batch
	out.agent.MaxBuffer = buffer
	out.level = level
	if out.agent.Hostname == "" && hostnameFile != "" {
		b, err := os.ReadFile(hostnameFile)
		if err != nil {
			return parsed{}, err
		}
		out.agent.Hostname = strings.TrimSpace(string(b))
	}
	if out.agent.Hostname == "" {
		h, err := os.Hostname()
		if err != nil || strings.TrimSpace(h) == "" {
			return parsed{}, errors.New("hostname is empty; set DLOG_HOSTNAME")
		}
		out.agent.Hostname = strings.TrimSpace(h)
	}
	out.agent.Exclude = map[string]struct{}{}
	for _, name := range strings.Split(exclude, ",") {
		name = strings.TrimPrefix(strings.TrimSpace(name), "/")
		if name != "" {
			out.agent.Exclude[name] = struct{}{}
		}
	}
	return out, nil
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscan(v, &n); err != nil {
		return fallback
	}
	return n
}
