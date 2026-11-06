package server

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, principalFrom(r))
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
		SignOutOthers   bool   `json:"sign_out_others"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if !validPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "password must be 8 to 72 characters")
		return
	}
	me := principalFrom(r)
	ip := clientIP(r, s.trustProxy)
	if s.login.blocked(ip, 10, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	if _, err := s.store.Authenticate(me.Username, body.CurrentPassword); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			s.login.hit(ip)
			writeError(w, http.StatusBadRequest, "wrong password")
			return
		}
		writeStoreError(w, err)
		return
	}
	if body.Password == body.CurrentPassword {
		writeError(w, http.StatusBadRequest, "choose a different password")
		return
	}
	if err := s.store.SetPassword(me.ID, body.Password); err != nil {
		writeStoreError(w, err)
		return
	}
	if body.SignOutOthers {
		keep := ""
		if c, err := r.Cookie(sessionCookie); err == nil {
			keep = c.Value
		}
		if err := s.store.DeleteOtherSessions(me.ID, keep); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, me)
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.Role = strings.TrimSpace(body.Role)
	if !validUsername(body.Username) {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}
	if !validPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "password must be 8 to 72 characters")
		return
	}
	if !validRole(body.Role) {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	acct, err := s.store.CreateUser(body.Username, body.Password, body.Role)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role     *string   `json:"role"`
		Password *string   `json:"password"`
		AllHosts *bool     `json:"all_hosts"`
		GroupIDs *[]string `json:"group_ids"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, statusForDecode(err), "bad request")
		return
	}
	if body.Role == nil && body.Password == nil && body.AllHosts == nil && body.GroupIDs == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	if body.Password != nil && !validPassword(*body.Password) {
		writeError(w, http.StatusBadRequest, "password must be 8 to 72 characters")
		return
	}
	if body.Role != nil {
		role := strings.TrimSpace(*body.Role)
		if !validRole(role) {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if err := s.store.SetUserRole(r.PathValue("id"), role); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if body.Password != nil {
		if err := s.store.SetPassword(r.PathValue("id"), *body.Password); err != nil {
			writeStoreError(w, err)
			return
		}
		if err := s.store.DeleteUserSessions(r.PathValue("id")); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	if body.AllHosts != nil || body.GroupIDs != nil {
		allHosts := false
		if body.AllHosts != nil {
			allHosts = *body.AllHosts
		}
		var groupIDs []string
		if body.GroupIDs != nil {
			groupIDs = *body.GroupIDs
		}
		if allHosts {
			groupIDs = nil
		}
		for _, id := range groupIDs {
			if strings.TrimSpace(id) == "" {
				writeError(w, http.StatusBadRequest, "invalid group")
				return
			}
		}
		if err := s.store.SetAccess(r.PathValue("id"), allHosts, groupIDs); err != nil {
			writeStoreError(w, err)
			return
		}
	}
	acct, err := s.store.User(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if principalFrom(r).ID == id {
		writeError(w, http.StatusBadRequest, "cannot delete the signed-in account")
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
