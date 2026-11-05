package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"dlog/internal/protocol"
)

func (a *Agent) relistLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.syncOnce(ctx)
		}
	}
}

func (a *Agent) syncOnce(ctx context.Context) {
	list, err := a.eng.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("list containers", "err", err)
		}
		return
	}
	var follow, skip []string
	for _, c := range list {
		label := c.Name
		if label == "" {
			label = shortID(c.ID)
		}
		if c.State != "" && c.State != "running" {
			skip = append(skip, label+" ("+c.State+")")
			continue
		}
		if why := a.skipReason(c); why != "" {
			skip = append(skip, label+" ("+why+")")
			continue
		}
		follow = append(follow, label)
		a.ensureFollow(ctx, c)
	}
	slog.Info("discovered containers", "count", len(list), "follow", strings.Join(follow, ", "), "skip", strings.Join(skip, ", "))
}

func (a *Agent) eventLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		ch, err := a.eng.Events(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("docker events", "err", err)
			if !sleep(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for ev := range ch {
			a.handleEvent(ctx, ev)
		}
		if ctx.Err() != nil {
			return
		}
		if !sleep(ctx, backoff) {
			return
		}
	}
}

func (a *Agent) handleEvent(ctx context.Context, ev Event) {
	if ev.Type != "" && ev.Type != "container" {
		return
	}
	id := ev.Actor.ID
	switch ev.Action {
	case "start", "unpause", "restart":
		info, err := a.eng.Inspect(ctx, id)
		if err != nil || a.skip(info) {
			return
		}
		a.ensureFollow(ctx, info)
	case "rename":
		name := strings.TrimPrefix(ev.Actor.Attributes["name"], "/")
		if name == "" {
			return
		}
		a.mu.Lock()
		live := a.live[id]
		a.mu.Unlock()
		if live != nil {
			live.rename(name)
		}
	case "destroy", "remove":
		a.cancelFollow(id)
	}
}

func (a *Agent) ensureFollow(ctx context.Context, c Container) {
	if ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	if _, ok := a.slots[c.ID]; ok {
		if live := a.live[c.ID]; live != nil {
			live.update(c)
		}
		a.mu.Unlock()
		return
	}
	a.gen++
	gen := a.gen
	live := &liveContainer{c: c}
	fctx, cancel := context.WithCancel(ctx)
	a.live[c.ID] = live
	a.slots[c.ID] = slot{gen: gen, cancel: cancel}
	a.wg.Add(1)
	a.mu.Unlock()

	slog.Info("following", "container", c.Name, "id", shortID(c.ID), "image", c.Image)
	go func() {
		defer a.wg.Done()
		a.follow(fctx, live)
		a.mu.Lock()
		if sl, ok := a.slots[c.ID]; ok && sl.gen == gen {
			delete(a.slots, c.ID)
			delete(a.live, c.ID)
		}
		a.mu.Unlock()
	}()
}

func (a *Agent) cancelFollow(id string) {
	a.mu.Lock()
	sl, ok := a.slots[id]
	a.mu.Unlock()
	if ok {
		sl.cancel()
	}
}

func (a *Agent) follow(ctx context.Context, live *liveContainer) {
	id := live.snapshot().ID
	cur, has := a.state.Cursor(id)
	backoff := time.Second
	for ctx.Err() == nil {
		err := a.streamOnce(ctx, live, &cur, &has)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, ErrNotFound) {
			return
		}
		if err != nil && !errors.Is(err, io.EOF) {
			slog.Warn("log stream", "container", live.snapshot().Name, "err", err)
		}
		if _, ierr := a.eng.Inspect(ctx, id); errors.Is(ierr, ErrNotFound) {
			return
		}
		if !sleep(ctx, backoff) {
			return
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) streamOnce(ctx context.Context, live *liveContainer, cur *Cursor, has *bool) error {
	id := live.snapshot().ID
	info, err := a.eng.Inspect(ctx, id)
	if err != nil {
		return err
	}
	live.update(info)
	since := time.Now().Add(-a.cfg.Since)
	if *has {
		since = time.Unix(0, cur.UnixNano)
	}
	body, err := a.eng.Logs(ctx, id, since)
	if err != nil {
		return err
	}
	defer body.Close()

	dec := NewDecoder(body, info.TTY)
	skippedEq := false
	for {
		stream, line, err := dec.Next()
		if err != nil {
			return err
		}
		ts, msg, ok := SplitTimestamp(line)
		if !ok {
			ts = time.Now().UTC()
			msg = line
		}
		msg = protocol.NormalizeMessage(msg)
		h := lineHash(stream, msg)
		if *has {
			nano := ts.UnixNano()
			if nano < cur.UnixNano {
				continue
			}
			if nano == cur.UnixNano && h == cur.Hash && !skippedEq {
				skippedEq = true
				continue
			}
		}
		cur.UnixNano = ts.UnixNano()
		cur.Hash = h
		*has = true
		snap := live.snapshot()
		a.buf.push(protocol.Record{
			ContainerID:   snap.ID,
			ContainerName: snap.Name,
			Image:         snap.Image,
			Stream:        stream,
			Timestamp:     ts.UTC(),
			Message:       msg,
		})
	}
}

func (a *Agent) skip(c Container) bool {
	return a.skipReason(c) != ""
}

func (a *Agent) skipReason(c Container) string {
	if !a.cfg.IncludeSelf && sameContainer(a.selfID, c.ID) {
		return "this agent"
	}
	if _, ok := a.cfg.Exclude[c.Name]; ok {
		return "name excluded"
	}
	switch strings.ToLower(strings.TrimSpace(c.Labels["dlog.exclude"])) {
	case "1", "true", "yes", "on":
		return "label dlog.exclude"
	}
	return ""
}
