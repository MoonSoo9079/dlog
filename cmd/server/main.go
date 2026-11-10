package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	"dlog/internal/version"
)

type config struct {
	Addr          string
	DataDir       string
	AdminUser     string
	AdminPassword string
	PSKID         string
	PSKKey        string
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
	fs.StringVar(&cfg.DataDir, "data-dir", cli.Env("DLOG_DATA_DIR", "data/server"), "directory for the database and generated files")
	fs.StringVar(&cfg.AdminUser, "admin-user", cli.Env("DLOG_ADMIN_USER", "admin"), "username of the first admin account, used only when the database has no accounts")
	fs.StringVar(&cfg.AdminPassword, "admin-password", cli.Env("DLOG_ADMIN_PASSWORD", ""), "password of the first admin account; generated on first start when empty")
	fs.StringVar(&cfg.PSKID, "psk-id", cli.Env("DLOG_PSK_ID", ""), "shared agent PSK id; generated and stored in the data directory when empty")
	fs.StringVar(&cfg.PSKKey, "psk-key", cli.Env("DLOG_PSK_KEY", ""), "shared agent PSK key; generated and stored in the data directory when empty")
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
	if (cfg.PSKID == "") != (cfg.PSKKey == "") {
		return config{}, errors.New("psk-id and psk-key must be set together")
	}
	return cfg, nil
}

func run(cfg config) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	idPath := filepath.Join(cfg.DataDir, "psk.id")
	keyPath := filepath.Join(cfg.DataDir, "psk.key")
	var psk *agentcred.Key
	if cfg.PSKID != "" {
		var err error
		psk, err = agentcred.Parse(cfg.PSKID, cfg.PSKKey)
		if err != nil {
			return err
		}
		if err := psk.Write(idPath, keyPath); err != nil {
			return err
		}
		slog.Info("agent psk from environment", "id", psk.ID(), "sha256", agentcred.Fingerprint(psk))
	} else {
		var generated bool
		var err error
		psk, generated, err = agentcred.Ensure(idPath, keyPath)
		if err != nil {
			return err
		}
		if generated {
			slog.Warn("generated agent psk; copy the id and key to every agent", "id", psk.ID(), "id_file", idPath, "key_file", keyPath, "sha256", agentcred.Fingerprint(psk))
		} else {
			slog.Info("agent psk", "id", psk.ID(), "sha256", agentcred.Fingerprint(psk))
		}
	}
	st, err := server.Open(filepath.Join(cfg.DataDir, "dlog.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SeedRetention(cfg.Retention); err != nil {
		return err
	}
	if err := ensureAdmin(st, cfg.DataDir, cfg.AdminUser, cfg.AdminPassword); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go purgeLoop(ctx, st)

	api := server.New(st, psk, cfg.TrustProxy, cfg.SecureCookies)
	api.SetPSKFiles(idPath, keyPath)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(),
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

func purgeLoop(ctx context.Context, st *server.Store) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			retention := st.Retention()
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

func ensureAdmin(st *server.Store, dir, username, password string) error {
	n, err := st.CountUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		if strings.TrimSpace(password) != "" {
			slog.Info("accounts already exist; leaving them unchanged")
		}
		return nil
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	generated := false
	if strings.TrimSpace(password) == "" {
		password, err = generatePassword()
		if err != nil {
			return err
		}
		generated = true
		path := filepath.Join(dir, "admin.password")
		if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "generated admin account %s\npassword (shown once): %s\nstored in %s\n", username, password, path)
	}
	if _, err := st.CreateUser(username, password, server.RoleAdmin); err != nil {
		return fmt.Errorf("create admin account: %w", err)
	}
	if !generated {
		slog.Info("created admin account", "username", username)
	}
	if len(password) < 20 {
		slog.Warn("admin password is short; use a long password outside local labs")
	}
	return nil
}

func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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
