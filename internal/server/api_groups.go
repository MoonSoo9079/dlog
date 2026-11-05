package server

import (
	"log/slog"
	"net/http"
	"strings"
)

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !validGroupName(name) {
		writeError(w, http.StatusBadRequest, "invalid name")
		return
	}
	g, err := s.store.CreateGroup(name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("group created", "id", g.ID, "name", g.Name)
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	name := strings.TrimSpace(body.Name)
	if !validGroupName(name) {
		writeError(w, http.StatusBadRequest, "invalid name")
		return
	}
	id := r.PathValue("id")
	if err := s.store.RenameGroup(id, name); err != nil {
		writeStoreError(w, err)
		return
	}
	g, err := s.store.Group(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteGroup(id); err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("group deleted", "id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSetHostGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GroupID *string `json:"group_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if body.GroupID == nil {
		writeError(w, http.StatusBadRequest, "invalid group")
		return
	}
	groupID := strings.TrimSpace(*body.GroupID)
	id := r.PathValue("id")
	if err := s.store.SetHostGroup(id, groupID); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
