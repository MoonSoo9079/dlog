package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/cli"
	"dlog/internal/server"
	"dlog/internal/token"
	"dlog/internal/version"
)

type config struct {
	Addr          string
	DataDir       string
	AdminToken    string
	AgentCert     string
	AgentKey      string
	Retention     time.Duration
	TLSCert       string
	TLSKey        string
	TrustProxy    bool
	SecureCookies bool
	LogLevel      string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(runHealthcheck())
	}
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		os.Exit(2)
	}
	cli.SetupLog(cfg.LogLevel)
	if err := run(cfg); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func parseConfig(args []string) (config, error) {
	retention, err := cli.Duration(os.Getenv("DLOG_RETENTION"), 168*time.Hour)
	if err != nil {
		return config{}, fmt.Errorf("DLOG_RETENTION: %w", err)
	}
	fs := flag.NewFlagSet("dlog-server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfg := config{Retention: retention}
	fs.StringVar(&cfg.Addr, "addr", cli.Env("DLOG_ADDR", ":8080"), "listen address")
	fs.StringVar(&cfg.DataDir, "data-dir", cli.Env("DLOG_DATA_DIR", "data/server"), "directory for the database and generated admin token")
	fs.StringVar(&cfg.AdminToken, "admin-token", cli.Env("DLOG_ADMIN_TOKEN", ""), "operator token; generated and saved on first run when empty")
	fs.StringVar(&cfg.AgentCert, "agent-cert", cli.Env("DLOG_AGENT_CERT", ""), "shared agent certificate; generated into the data directory when empty")
	fs.StringVar(&cfg.AgentKey, "agent-key", cli.Env("DLOG_AGENT_KEY", ""), "where to write the agent private key when generating a certificate")
	fs.Func("retention", "delete logs older than this duration (0 keeps them; 7d or 168h)", func(s string) error {
		d, err := cli.Duration(s, cfg.Retention)
		if err != nil {
			return err
		}
		cfg.Retention = d
		return nil
	})
	fs.StringVar(&cfg.TLSCert, "tls-cert", cli.Env("DLOG_TLS_CERT", ""), "TLS certificate file")
	fs.StringVar(&cfg.TLSKey, "tls-key", cli.Env("DLOG_TLS_KEY", ""), "TLS private key file")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cli.EnvBool("DLOG_TRUST_PROXY", false), "honor X-Forwarded-For and X-Forwarded-Proto")
	fs.BoolVar(&cfg.SecureCookies, "secure-cookies", cli.EnvBool("DLOG_SECURE_COOKIES", false), "mark the session cookie Secure")
	fs.StringVar(&cfg.LogLevel, "log-level", cli.Env("DLOG_LOG_LEVEL", "info"), "debug, info, warn, or error")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return config{}, errors.New("tls-cert and tls-key must be set together")
	}
	return cfg, nil
}

func run(cfg config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	admin, generated, err := resolveAdminToken(cfg.DataDir, cfg.AdminToken)
	if err != nil {
		return err
	}
	if generated {
		fmt.Fprintf(os.Stderr, "generated admin token (shown once): %s\nstored in %s\n", admin, filepath.Join(cfg.DataDir, "admin.token"))
	}
	if len(admin) < 20 {
		slog.Warn("admin token is short; use a long random value outside local labs")
	}
	certPath := cfg.AgentCert
	keyPath := cfg.AgentKey
	if certPath == "" {
		certPath = filepath.Join(cfg.DataDir, "agent.crt")
	}
	if keyPath == "" {
		keyPath = filepath.Join(cfg.DataDir, "agent.key")
	}
	agentCert, generatedCert, err := agentcred.Ensure(certPath, keyPath)
	if err != nil {
		return err
	}
	if generatedCert {
		slog.Warn("generated agent certificate; copy both files to every agent", "cert", certPath, "key", keyPath, "sha256", agentcred.Fingerprint(agentCert))
	} else {
		slog.Info("agent certificate", "cert", certPath, "sha256", agentcred.Fingerprint(agentCert))
	}
	st, err := server.Open(filepath.Join(cfg.DataDir, "dlog.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go purgeLoop(ctx, st, cfg.Retention)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(st, admin, agentCert, cfg.TrustProxy, cfg.SecureCookies).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" {
			errCh <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
			return
		}
		errCh <- srv.ListenAndServe()
	}()
	slog.Info("listening", "addr", cfg.Addr, "version", version.Version, "tls", cfg.TLSCert != "")

	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errCh:
		stop()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func purgeLoop(ctx context.Context, st *server.Store, retention time.Duration) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if retention > 0 {
				n, err := st.PurgeLogs(time.Now().Add(-retention))
				if err != nil {
					slog.Error("purge logs", "err", err)
				} else if n > 0 {
					slog.Info("purged logs", "rows", n)
				}
			}
			if err := st.PurgeSessions(); err != nil {
				slog.Error("purge sessions", "err", err)
			}
		}
	}
}

func resolveAdminToken(dir, configured string) (string, bool, error) {
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured), false, nil
	}
	path := filepath.Join(dir, "admin.token")
	b, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", false, err
	}
	raw := token.New("at_")
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		return "", false, err
	}
	return raw, true, nil
}

func runHealthcheck() int {
	url := os.Getenv("DLOG_HEALTH_URL")
	if url == "" {
		addr := os.Getenv("DLOG_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		_, port, err := net.SplitHostPort(addr)
		if err != nil || port == "" {
			port = "8080"
		}
		scheme := "http"
		if os.Getenv("DLOG_TLS_CERT") != "" {
			scheme = "https"
		}
		url = scheme + "://127.0.0.1:" + port + "/api/v1/health"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	if strings.HasPrefix(url, "https://") {
		client = server.InsecureClient()
	}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, resp.Status)
		return 1
	}
	return 0
}
