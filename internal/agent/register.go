package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/protocol"
	"dlog/internal/version"
)

func (a *Agent) waitIdentity(ctx context.Context) error {
	backoff := 2 * time.Second
	for {
		err := a.ensureIdentity(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("registration failed", "err", err)
		wait := backoff
		if !sleep(ctx, wait) {
			return ctx.Err()
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) ensureIdentity(ctx context.Context) error {
	if a.state.Token() != "" {
		return nil
	}
	return a.register(ctx)
}

func (a *Agent) register(ctx context.Context) error {
	body, err := json.Marshal(protocol.RegisterRequest{
		Hostname:     a.cfg.Hostname,
		AgentVersion: version.Version,
		Timestamp:    time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	sig, err := agentcred.Sign(a.cfg.AgentCert, body)
	if err != nil {
		return err
	}
	var resp protocol.RegisterResponse
	if err := a.postSigned(ctx, "/api/v1/register", body, sig, &resp); err != nil {
		return err
	}
	slog.Info("registered", "host", resp.Hostname, "id", resp.HostID)
	return a.state.SetIdentity(resp.HostID, resp.HostToken)
}
