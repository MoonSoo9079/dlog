package server

import (
	"embed"
	"net/http"
	"regexp"
)

//go:embed web/index.html web/login.html web/admin.html web/account.html web/forbidden.html web/app.css web/app.js web/bootstrap.min.css web/bootstrap.bundle.min.js web/viewer.css web/fonts/*.woff2
var webFS embed.FS

var fontName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*\.woff2$`)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.principal(w, r); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	serveAsset(w, "web/index.html", "text/html; charset=utf-8")
}

func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.principal(w, r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if acct.Role != RoleAdmin {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		serveAsset(w, "web/forbidden.html", "text/html; charset=utf-8")
		return
	}
	serveAsset(w, "web/admin.html", "text/html; charset=utf-8")
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.principal(w, r); !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	serveAsset(w, "web/account.html", "text/html; charset=utf-8")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.principal(w, r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	serveAsset(w, "web/login.html", "text/html; charset=utf-8")
}

func (s *Server) handleCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	serveAsset(w, "web/app.css", "text/css; charset=utf-8")
}

func (s *Server) handleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	serveAsset(w, "web/app.js", "text/javascript; charset=utf-8")
}

func (s *Server) handleViewerCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	serveAsset(w, "web/viewer.css", "text/css; charset=utf-8")
}

func (s *Server) handleFont(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if !fontName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=604800")
	serveAsset(w, "web/fonts/"+name, "font/woff2")
}

func (s *Server) handleBootstrapCSS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveAsset(w, "web/bootstrap.min.css", "text/css; charset=utf-8")
}

func (s *Server) handleBootstrapJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveAsset(w, "web/bootstrap.bundle.min.js", "text/javascript; charset=utf-8")
}

func serveAsset(w http.ResponseWriter, name, contentType string) {
	b, err := webFS.ReadFile(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(b)
}
