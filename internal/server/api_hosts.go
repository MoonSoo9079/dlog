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
		Hostname    *string `json:"hostname"`
		DisplayName *string `json:"display_name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if body.Hostname == nil && body.DisplayName == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	var hostname string
	if body.Hostname != nil {
		hostname = strings.TrimSpace(*body.Hostname)
		if !protocol.ValidHostname(hostname) {
			writeError(w, http.StatusBadRequest, "invalid hostname")
			return
		}
	}
	var displayName string
	if body.DisplayName != nil {
		displayName = strings.TrimSpace(*body.DisplayName)
		if displayName != "" && !validGroupName(displayName) {
			writeError(w, http.StatusBadRequest, "invalid name")
			return
		}
	}
	id := r.PathValue("id")
	if body.Hostname != nil {
		if err := s.store.RenameHost(id, hostname); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if body.DisplayName != nil {
		if err := s.store.SetDisplayName(id, displayName); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	host, err := s.store.Host(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, host)
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
