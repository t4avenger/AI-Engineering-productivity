package ui

import (
	"net/http"
	"strings"
)

// Wrap routes dashboard paths to the UI and forwards everything else to next.
func (s *Server) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			http.StripPrefix("/static/", s.static).ServeHTTP(w, r)
			return
		}
		if s.serve(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) bool {
	if s.servePublic(w, r) {
		return true
	}
	if !s.isDashboardPath(r.URL.Path) {
		return false
	}
	if !s.authenticated(r) {
		http.Redirect(w, r, "/unlock", http.StatusSeeOther)
		return true
	}
	s.serveProtected(w, r)
	return true
}

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/unlock" && r.Method == http.MethodGet:
		s.unlockGet(w, r)
		return true
	case r.URL.Path == "/unlock" && r.Method == http.MethodPost:
		s.unlockPost(w, r)
		return true
	case r.URL.Path == "/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
		return true
	default:
		return false
	}
}

type route struct {
	match  func(method, path string) bool
	handle func(*Server, http.ResponseWriter, *http.Request)
}

func (s *Server) serveProtected(w http.ResponseWriter, r *http.Request) {
	for _, route := range protectedRoutes {
		if route.match(r.Method, r.URL.Path) {
			route.handle(s, w, r)
			return
		}
	}
	http.NotFound(w, r)
}

var protectedRoutes = []route{
	{match: exact(http.MethodGet, "/"), handle: (*Server).home},
	{match: exact(http.MethodGet, "/sessions"), handle: (*Server).sessionsList},
	{match: prefixSuffix(http.MethodPost, "/sessions/", "/delete"), handle: (*Server).sessionDelete},
	{match: prefixSuffix(http.MethodGet, "/sessions/", "/timeline"), handle: (*Server).sessionTimelinePartial},
	{match: prefix(http.MethodGet, "/sessions/"), handle: (*Server).sessionDetail},
	{match: exact(http.MethodGet, "/insights"), handle: (*Server).insightsPage},
	{match: exact(http.MethodGet, "/integrations"), handle: (*Server).integrationsPage},
	{match: exact(http.MethodGet, "/privacy"), handle: (*Server).privacyPage},
	{match: exact(http.MethodPost, "/privacy/delete-all"), handle: (*Server).privacyDeleteAll},
	{match: exact(http.MethodGet, "/costs"), handle: (*Server).costsPage},
}

func exact(method, path string) func(string, string) bool {
	return func(m, p string) bool { return m == method && p == path }
}

func prefix(method, prefixPath string) func(string, string) bool {
	return func(m, p string) bool {
		return m == method && strings.HasPrefix(p, prefixPath)
	}
}

func prefixSuffix(method, prefixPath, suffix string) func(string, string) bool {
	return func(m, p string) bool {
		return m == method && strings.HasPrefix(p, prefixPath) && strings.HasSuffix(p, suffix)
	}
}

func (s *Server) isDashboardPath(path string) bool {
	switch path {
	case "/", "/sessions", "/insights", "/integrations", "/privacy", "/privacy/delete-all", "/costs":
		return true
	}
	return strings.HasPrefix(path, "/sessions/")
}
