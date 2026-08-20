package httpapi

import (
	"net/http"
)

// registerTopology wires the generator state-machine and branch topology routes.
func registerTopology(s *Server) {
	s.mux.HandleFunc("POST /generators/{id}/commit", s.commitGenerator)
	s.mux.HandleFunc("POST /generators/{id}/decommit", s.decommitGenerator)
	s.mux.HandleFunc("GET /generators/{id}/status", s.generatorStatus)
	s.mux.HandleFunc("POST /branches/{id}/outage", s.outageBranch)
	s.mux.HandleFunc("POST /branches/{id}/restore", s.restoreBranch)
}

func (s *Server) commitGenerator(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.CommitGenerator(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "committed"})
}

func (s *Server) decommitGenerator(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.DecommitGenerator(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "offline"})
}

func (s *Server) generatorStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := s.store.GetGenerator(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, g)
}

func (s *Server) outageBranch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.OutageBranch(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "in_service": false})
}

func (s *Server) restoreBranch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.RestoreBranch(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "in_service": true})
}
