package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hostID := q.Get("host_id")
	if _, err := s.store.Host(hostID); err != nil {
		writeStoreError(w, err)
		return
	}
	f := LogFilter{
		HostID:      hostID,
		ContainerID: q.Get("container_id"),
		Stream:      q.Get("stream"),
		Query:       q.Get("q"),
		Mode:        LogLatest,
	}
	if f.Stream != "" && f.Stream != "stdout" && f.Stream != "stderr" {
		writeError(w, http.StatusBadRequest, "invalid stream")
		return
	}
	if len(f.Query) > 500 {
		writeError(w, http.StatusBadRequest, "query too long")
		return
	}
	if _, hasAfter := q["after_id"]; hasAfter {
		if _, hasBefore := q["before_id"]; hasBefore {
			writeError(w, http.StatusBadRequest, "after_id and before_id together are not supported")
			return
		}
		n, err := strconv.ParseInt(q.Get("after_id"), 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid after_id")
			return
		}
		f.Mode = LogAfter
		f.CursorID = n
	} else if _, hasBefore := q["before_id"]; hasBefore {
		n, err := strconv.ParseInt(q.Get("before_id"), 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid before_id")
			return
		}
		f.Mode = LogBefore
		f.CursorID = n
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid since")
			return
		}
		f.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid until")
			return
		}
		f.Until = &t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		f.Limit = n
	}
	lines, err := s.store.Logs(f)
	if err != nil {
		slog.Error("query logs", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
}
