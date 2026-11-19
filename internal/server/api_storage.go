package server

import (
	"log/slog"
	"net/http"
)

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.StorageStats()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleCompactStorage(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Compact(); err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("database compacted")
	stats, err := s.store.StorageStats()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
