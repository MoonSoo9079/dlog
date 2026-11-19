package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dlog/internal/protocol"
)

const maxExportLines = 50000

func (s *Server) parseLogFilter(w http.ResponseWriter, r *http.Request) (LogFilter, bool) {
	q := r.URL.Query()
	f := LogFilter{
		ContainerID: q.Get("container_id"),
		Stream:      q.Get("stream"),
		Query:       q.Get("q"),
		Exclude:     q.Get("exclude"),
		Level:       strings.ToLower(strings.TrimSpace(q.Get("level"))),
		Mode:        LogLatest,
	}
	hostID := q.Get("host_id")
	groupID := q.Get("group_id")
	if hostID != "" {
		if _, ok := s.visibleHost(w, r, hostID); !ok {
			return LogFilter{}, false
		}
		f.HostID = hostID
	} else {
		ids, ok := s.resolveMultiHostScope(w, r, groupID)
		if !ok {
			return LogFilter{}, false
		}
		f.HostIDs = ids
	}
	if f.Stream != "" && f.Stream != "stdout" && f.Stream != "stderr" {
		writeError(w, http.StatusBadRequest, "invalid stream")
		return LogFilter{}, false
	}
	if !validLogLevel(f.Level) {
		writeError(w, http.StatusBadRequest, "invalid level")
		return LogFilter{}, false
	}
	if len(f.Query) > 500 {
		writeError(w, http.StatusBadRequest, "query too long")
		return LogFilter{}, false
	}
	if len(f.Exclude) > 500 {
		writeError(w, http.StatusBadRequest, "exclude too long")
		return LogFilter{}, false
	}
	if _, hasAfter := q["after_id"]; hasAfter {
		if _, hasBefore := q["before_id"]; hasBefore {
			writeError(w, http.StatusBadRequest, "after_id and before_id together are not supported")
			return LogFilter{}, false
		}
		n, err := strconv.ParseInt(q.Get("after_id"), 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid after_id")
			return LogFilter{}, false
		}
		f.Mode = LogAfter
		f.CursorID = n
	} else if _, hasBefore := q["before_id"]; hasBefore {
		n, err := strconv.ParseInt(q.Get("before_id"), 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid before_id")
			return LogFilter{}, false
		}
		f.Mode = LogBefore
		f.CursorID = n
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since")
			return LogFilter{}, false
		}
		f.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until")
			return LogFilter{}, false
		}
		f.Until = &t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return LogFilter{}, false
		}
		f.Limit = n
	}
	return f, true
}

func (s *Server) resolveMultiHostScope(w http.ResponseWriter, r *http.Request, groupID string) ([]string, bool) {
	me := principalFrom(r)
	if groupID == "" && me.SeesAll() {
		return nil, true
	}
	hosts, err := s.store.ListHosts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	groups, err := s.store.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	hosts, groups = visibleFleet(me, hosts, groups)
	if groupID == "" {
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ids = append(ids, h.ID)
		}
		return ids, true
	}
	if groupID == "ungrouped" {
		if !me.SeesAll() {
			writeStoreError(w, ErrNotFound)
			return nil, false
		}
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			if h.GroupID == "" {
				ids = append(ids, h.ID)
			}
		}
		return ids, true
	}
	found := false
	for _, g := range groups {
		if g.ID == groupID {
			found = true
			break
		}
	}
	if !found {
		writeStoreError(w, ErrNotFound)
		return nil, false
	}
	ids := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h.GroupID == groupID {
			ids = append(ids, h.ID)
		}
	}
	return ids, true
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	f, ok := s.parseLogFilter(w, r)
	if !ok {
		return
	}
	lines, err := s.store.Logs(f)
	if err != nil {
		slog.Error("query logs", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}

func (s *Server) handleLogExport(w http.ResponseWriter, r *http.Request) {
	f, ok := s.parseLogFilter(w, r)
	if !ok {
		return
	}
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "log"
	}
	if format != "log" && format != "jsonl" {
		writeError(w, http.StatusBadRequest, "invalid format")
		return
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	filename := "dlog-" + stamp + "." + format
	if format == "jsonl" {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")

	bw := bufio.NewWriterSize(w, 32*1024)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	err := s.store.StreamLogs(f, maxExportLines, func(ln protocol.LogLine) error {
		if format == "jsonl" {
			return enc.Encode(ln)
		}
		_, err := fmt.Fprintf(bw, "%s [%s] [%s] [%s] %s\n",
			ln.Timestamp.UTC().Format(time.RFC3339Nano),
			ln.HostName,
			ln.ContainerName,
			ln.Stream,
			ln.Message,
		)
		return err
	})
	if err != nil {
		slog.Error("export logs", "err", err)
	}
	_ = bw.Flush()
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	f, ok := s.parseLogFilter(w, r)
	if !ok {
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	// Subscribe before catch-up query so no lines are dropped in between.
	sub := s.hub.subscribe(filterMatcher(f))
	defer s.hub.unsubscribe(sub)

	if _, err := w.Write([]byte(": ok\n\n")); err != nil {
		return
	}
	_ = rc.Flush()

	lastID := f.CursorID
	if f.Mode == LogAfter {
		catchup := f
		catchup.Limit = 1000
		if lines, err := s.store.Logs(catchup); err == nil && len(lines) > 0 {
			if err := writeSSELines(w, rc, lines); err != nil {
				return
			}
			lastID = lines[len(lines)-1].ID
		}
	}

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case batch := <-sub.ch:
			filtered := batch[:0]
			for _, ln := range batch {
				if ln.ID > lastID {
					filtered = append(filtered, ln)
					lastID = ln.ID
				}
			}
			if len(filtered) == 0 {
				continue
			}
			if err := writeSSELines(w, rc, filtered); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}

func writeSSELines(w http.ResponseWriter, rc *http.ResponseController, lines []protocol.LogLine) error {
	b, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte("event: lines\ndata: ")); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}
	return rc.Flush()
}
