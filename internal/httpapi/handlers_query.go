package httpapi

import (
	"net/http"

	"task137-gridflow/internal/berr"
)

// registerQuery wires the audit, case-snapshot, and summary routes.
func registerQuery(s *Server) {
	s.mux.HandleFunc("GET /events", s.listEvents)
	s.mux.HandleFunc("POST /cases", s.saveCase)
	s.mux.HandleFunc("GET /cases", s.listCases)
	s.mux.HandleFunc("GET /summary", s.summary)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := parseInt(l); err == nil {
			limit = n
		}
	}
	ev, err := s.store.ListEvents(r.Context(), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": ev})
}

type saveCaseReq struct {
	Name string `json:"name"`
}

func (s *Server) saveCase(w http.ResponseWriter, r *http.Request) {
	var req saveCaseReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, berr.Invalidf("invalid case: %v", err))
		return
	}
	if req.Name == "" {
		writeErr(w, berr.Invalidf("case name required"))
		return
	}
	snap, err := s.store.LoadAll(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SaveCase(r.Context(), req.Name, snap); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"name": req.Name, "buses": len(snap.Buses), "branches": len(snap.Branches), "generators": len(snap.Generators)})
}

func (s *Server) listCases(w http.ResponseWriter, r *http.Request) {
	names, err := s.store.ListCases(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"cases": names})
}

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	stats, err := s.svc.Summary(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, stats)
}

// parseInt parses a base-10 int.
func parseInt(s string) (int, error) {
	n := 0
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if len(s) == 0 {
		return 0, berr.New(berr.CodeInvalid, "empty int")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, berr.New(berr.CodeInvalid, "invalid int")
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}
