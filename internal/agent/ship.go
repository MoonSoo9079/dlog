package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dlog/internal/protocol"
	"dlog/internal/version"
)

func (a *Agent) shipLoop(ctx context.Context) {
	backoff := time.Second
	for {
		recs := a.buf.wait(ctx, a.cfg.BatchSize, a.cfg.FlushInterval)
		if ctx.Err() != nil {
			a.flushRemaining()
			return
		}
		if len(recs) == 0 {
			continue
		}
		err := a.sendBatch(ctx, recs)
		if err == nil || errors.Is(err, errDrop) {
			if saveErr := a.state.Advance(recs); saveErr != nil {
				slog.Error("save cursor", "err", saveErr)
				if !sleep(ctx, time.Second) {
					a.flushRemaining()
					return
				}
				continue
			}
			a.buf.ack(len(recs))
			backoff = time.Second
			continue
		}
		a.noteSendErr(err)
		wait := backoff
		if errors.Is(err, errAuth) || errors.Is(err, errDisabled) {
			wait = 30 * time.Second
		}
		if !sleep(ctx, wait) {
			a.flushRemaining()
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) flushRemaining() {
	recs := a.buf.peek(a.cfg.MaxBuffer)
	if len(recs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.sendBatch(ctx, recs); err != nil {
		slog.Warn("final flush failed", "err", err, "records", len(recs))
		return
	}
	if err := a.state.Advance(recs); err != nil {
		slog.Error("save cursor", "err", err)
		return
	}
	a.buf.ack(len(recs))
}

func (a *Agent) noteSendErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.errStreak++
	if time.Since(a.lastErr) < 15*time.Second {
		return
	}
	a.lastErr = time.Now()
	slog.Warn("ingest failed", "err", err, "failures", a.errStreak)
}

func (a *Agent) sendBatch(ctx context.Context, recs []protocol.Record) error {
	body, err := json.Marshal(protocol.IngestRequest{AgentVersion: version.Version, Records: recs})
	if err != nil {
		return fmt.Errorf("%w: %v", errDrop, err)
	}
	return a.postAuthed(ctx, "/api/v1/ingest", body, nil)
}

type buffer struct {
	mu       sync.Mutex
	recs     []protocol.Record
	inflight int
	max      int
	dropped  int64
}

func newBuffer(max int) *buffer {
	return &buffer{max: max}
}

func (b *buffer) push(r protocol.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.recs) >= b.max {
		if b.inflight < len(b.recs) {
			copy(b.recs[b.inflight:], b.recs[b.inflight+1:])
			b.recs = b.recs[:len(b.recs)-1]
			b.dropped++
		} else {
			b.dropped++
			return
		}
	}
	b.recs = append(b.recs, r)
}

func (b *buffer) peek(n int) []protocol.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.recs) {
		n = len(b.recs)
	}
	out := make([]protocol.Record, n)
	copy(out, b.recs[:n])
	b.inflight = n
	return out
}

func (b *buffer) ack(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.recs) {
		n = len(b.recs)
	}
	b.recs = append([]protocol.Record(nil), b.recs[n:]...)
	b.inflight = 0
}

func (b *buffer) droppedCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

func (b *buffer) wait(ctx context.Context, batch int, flush time.Duration) []protocol.Record {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var deadline time.Time
	for {
		b.mu.Lock()
		if len(b.recs) == 0 {
			deadline = time.Time{}
		} else if deadline.IsZero() {
			deadline = time.Now().Add(flush)
		}
		full := len(b.recs) >= batch
		due := len(b.recs) > 0 && !deadline.IsZero() && !time.Now().Before(deadline)
		if full || due {
			n := batch
			if n > len(b.recs) {
				n = len(b.recs)
			}
			out := make([]protocol.Record, n)
			copy(out, b.recs[:n])
			b.inflight = n
			b.mu.Unlock()
			return out
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
