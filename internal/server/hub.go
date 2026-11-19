package server

import (
	"strings"
	"sync"

	"dlog/internal/protocol"
)

type logSubscriber struct {
	ch    chan []protocol.LogLine
	match func(protocol.LogLine) bool
}

type logHub struct {
	mu   sync.RWMutex
	subs map[*logSubscriber]struct{}
}

func newLogHub() *logHub {
	return &logHub{subs: map[*logSubscriber]struct{}{}}
}

func (h *logHub) subscribe(match func(protocol.LogLine) bool) *logSubscriber {
	sub := &logSubscriber{
		ch:    make(chan []protocol.LogLine, 64),
		match: match,
	}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()
	return sub
}

func (h *logHub) unsubscribe(sub *logSubscriber) {
	h.mu.Lock()
	delete(h.subs, sub)
	h.mu.Unlock()
}

func (h *logHub) publish(lines []protocol.LogLine) {
	if len(lines) == 0 {
		return
	}
	h.mu.RLock()
	if len(h.subs) == 0 {
		h.mu.RUnlock()
		return
	}
	subs := make([]*logSubscriber, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	for _, sub := range subs {
		var matched []protocol.LogLine
		for _, ln := range lines {
			if sub.match(ln) {
				matched = append(matched, ln)
			}
		}
		if len(matched) == 0 {
			continue
		}
		select {
		case sub.ch <- matched:
		default:
			// Slow consumer: drop oldest batch to stay live without blocking ingest.
			select {
			case <-sub.ch:
			default:
			}
			select {
			case sub.ch <- matched:
			default:
			}
		}
	}
}

func filterMatcher(f LogFilter) func(protocol.LogLine) bool {
	var allow map[string]bool
	if f.HostID == "" && f.HostIDs != nil {
		allow = make(map[string]bool, len(f.HostIDs))
		for _, id := range f.HostIDs {
			allow[id] = true
		}
	}
	qLower := strings.ToLower(f.Query)
	exLower := strings.ToLower(f.Exclude)
	return func(ln protocol.LogLine) bool {
		if f.HostID != "" {
			if ln.HostID != f.HostID {
				return false
			}
		} else if allow != nil && !allow[ln.HostID] {
			return false
		}
		if f.ContainerID != "" && ln.ContainerID != f.ContainerID {
			return false
		}
		if f.Stream != "" && ln.Stream != f.Stream {
			return false
		}
		if f.Since != nil && ln.Timestamp.Before(*f.Since) {
			return false
		}
		if f.Until != nil && ln.Timestamp.After(*f.Until) {
			return false
		}
		if qLower != "" && !strings.Contains(strings.ToLower(ln.Message), qLower) {
			return false
		}
		if exLower != "" && strings.Contains(strings.ToLower(ln.Message), exLower) {
			return false
		}
		if f.Level != "" && !matchesLevel(ln.Message, f.Level) {
			return false
		}
		return true
	}
}
