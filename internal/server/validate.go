package server

import (
	"strings"
	"time"

	"dlog/internal/protocol"
)

func normalizeRecord(r *protocol.Record) error {
	r.ContainerID = strings.TrimSpace(r.ContainerID)
	if !protocol.ValidContainerID(r.ContainerID) {
		return errBad("container_id")
	}
	r.ContainerName = strings.TrimPrefix(strings.TrimSpace(r.ContainerName), "/")
	if !protocol.CleanName(r.ContainerName) {
		return errBad("container_name")
	}
	r.Image = protocol.Clip(strings.TrimSpace(r.Image), protocol.MaxImage)
	switch strings.ToLower(strings.TrimSpace(r.Stream)) {
	case "stdout":
		r.Stream = "stdout"
	case "stderr":
		r.Stream = "stderr"
	default:
		return errBad("stream")
	}
	if r.Timestamp.IsZero() {
		return errBad("timestamp")
	}
	r.Timestamp = r.Timestamp.UTC()
	if r.Timestamp.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || r.Timestamp.After(time.Now().Add(24*time.Hour)) {
		return errBad("timestamp")
	}
	r.Message = protocol.NormalizeMessage(r.Message)
	return nil
}

func normalizeSnapshot(c *protocol.ContainerSnapshot) error {
	c.ID = strings.TrimSpace(c.ID)
	if !protocol.ValidContainerID(c.ID) {
		return errBad("container id")
	}
	c.Name = strings.TrimPrefix(strings.TrimSpace(c.Name), "/")
	if !protocol.CleanName(c.Name) {
		return errBad("container name")
	}
	c.Image = protocol.Clip(strings.TrimSpace(c.Image), protocol.MaxImage)
	switch c.State {
	case "", "running":
		c.State = "running"
	case "exited", "paused", "created", "restarting", "dead":
	default:
		c.State = "exited"
	}
	return nil
}

type badField string

func (b badField) Error() string { return "invalid " + string(b) }

func errBad(field string) error { return badField(field) }
