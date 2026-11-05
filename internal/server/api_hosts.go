package server

import (
	"log/slog"
	"net/http"
	"strings"

	"dlog/internal/protocol"
)

func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.store.ListHosts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	groups, err := s.store.ListGroups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts, "groups": groups})
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	hostID := r.URL.Query().Get("host_id")
	if _, err := s.store.Host(hostID); err != nil {
		writeStoreError(w, err)
		return
	}
	items, err := s.store.Containers(hostID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": items})
}

func (s *Server) handleRenameHost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname string `json:"hostname"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	body.Hostname = strings.TrimSpace(body.Hostname)
	if !protocol.ValidHostname(body.Hostname) {
		writeError(w, http.StatusBadRequest, "invalid hostname")
		return
	}
	id := r.PathValue("id")
	if err := s.store.RenameHost(id, body.Hostname); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "hostname": body.Hostname})
}

func (s *Server) handleDisableHost(w http.ResponseWriter, r *http.Request) {
	s.setStatus(w, r, HostDisabled)
}

func (s *Server) handleEnableHost(w http.ResponseWriter, r *http.Request) {
	s.setStatus(w, r, HostEnabled)
}

func (s *Server) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	id := r.PathValue("id")
	if err := s.store.SetHostStatus(id, status); err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("host status", "id", id, "status", status)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteHost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteHost(id); err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("host deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}
