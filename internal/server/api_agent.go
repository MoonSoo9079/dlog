package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"dlog/internal/agentcred"
	"dlog/internal/protocol"
)

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustProxy)
	if s.regLimit.blocked(ip, 30, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := agentcred.Verify(s.certificate(), body, r.Header.Get(agentcred.Header)); err != nil {
		s.regLimit.hit(ip)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req protocol.RegisterRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || dec.More() {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if !agentcred.Fresh(req.Timestamp, time.Now()) {
		s.regLimit.hit(ip)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	if !protocol.ValidHostname(req.Hostname) {
		writeError(w, http.StatusBadRequest, "invalid hostname")
		return
	}
	host, hostToken, created, err := s.store.Register(req.Hostname, req.AgentVersion)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	slog.Info("host registered", "host", host.Hostname, "id", host.ID, "new", created)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, protocol.RegisterResponse{HostID: host.ID, Hostname: host.Hostname, HostToken: hostToken})
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	host, ok := s.hostFromRequest(w, r)
	if !ok {
		return
	}
	var req protocol.IngestRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if len(req.Records) > protocol.MaxBatch {
		writeError(w, http.StatusRequestEntityTooLarge, "too many records")
		return
	}
	valid := make([]protocol.Record, 0, len(req.Records))
	rejected := 0
	var first error
	for i := range req.Records {
		rec := req.Records[i]
		if err := normalizeRecord(&rec); err != nil {
			rejected++
			if first == nil {
				first = err
			}
			continue
		}
		valid = append(valid, rec)
	}
	accepted, err := s.store.Ingest(host.ID, valid)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if rejected > 0 {
		slog.Warn("ingest rejected records", "host", host.Hostname, "rejected", rejected, "reason", first)
	}
	writeJSON(w, http.StatusOK, protocol.IngestResponse{Accepted: accepted, Rejected: rejected})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	host, ok := s.hostFromRequest(w, r)
	if !ok {
		return
	}
	var hb protocol.Heartbeat
	if err := decodeJSON(w, r, &hb); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if hb.Hostname != "" && hb.Hostname != host.Hostname {
		slog.Warn("heartbeat hostname differs from registered host", "registered", host.Hostname, "reported", hb.Hostname)
	}
	if len(hb.Containers) > 5000 {
		hb.Containers = hb.Containers[:5000]
	}
	kept := hb.Containers[:0]
	for i := range hb.Containers {
		c := hb.Containers[i]
		if err := normalizeSnapshot(&c); err != nil {
			continue
		}
		kept = append(kept, c)
	}
	hb.Containers = kept
	if err := s.store.Heartbeat(host.ID, hb); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.HeartbeatResponse{Status: HostEnabled})
}
