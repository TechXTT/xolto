package api

import "net/http"

func (s *Server) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "xolto-server"})
}
