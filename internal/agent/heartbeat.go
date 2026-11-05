package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"dlog/internal/protocol"
	"dlog/internal/version"
)

func (a *Agent) heartbeatLoop(ctx context.Context) {
	a.sendHeartbeat(ctx)
	t := time.NewTicker(a.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sendHeartbeat(ctx)
		}
	}
}

func (a *Agent) sendHeartbeat(ctx context.Context) {
	ver, err := a.eng.Version(ctx)
	if err != nil && ctx.Err() == nil {
		slog.Debug("docker version", "err", err)
	}
	hb := protocol.Heartbeat{
		AgentVersion:  version.Version,
		Hostname:      a.cfg.Hostname,
		DockerVersion: ver,
		Dropped:       a.buf.droppedCount(),
	}
	list, err := a.eng.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("list containers", "err", err)
		}
	} else {
		hb.ReplaceContainers = true
		for _, c := range list {
			if c.State != "" && c.State != "running" {
				continue
			}
			if a.skip(c) {
				continue
			}
			hb.Containers = append(hb.Containers, protocol.ContainerSnapshot{
				ID: c.ID, Name: c.Name, Image: c.Image, State: "running", Labels: c.Labels,
			})
		}
	}
	body, err := json.Marshal(hb)
	if err != nil {
		return
	}
	if err := a.postAuthed(ctx, "/api/v1/heartbeat", body, nil); err != nil && ctx.Err() == nil {
		a.noteSendErr(err)
	}
}
