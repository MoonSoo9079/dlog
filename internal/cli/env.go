package cli

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

func Env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func EnvBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

// Duration parses Go durations and a day suffix, such as 7d.
func Duration(s string, fallback time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback, nil
	}
	if s == "0" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok && !strings.ContainsAny(n, "hms") {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, err
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func SetupLog(level string) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
