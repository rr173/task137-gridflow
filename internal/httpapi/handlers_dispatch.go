package httpapi

import (
	"net/http"
	"strconv"

	"task137-gridflow/internal/berr"
)

// registerDispatch wires the dispatch / period / reserve / reconcile routes.
func registerDispatch(s *Server) {
	s.mux.HandleFunc("POST /dispatch/run", s.runDispatch)
	s.mux.HandleFunc("GET /dispatch/{period}", s.getDispatch)
	s.mux.HandleFunc("GET /dispatch/{period}/violations", s.getViolations)
	s.mux.HandleFunc("GET /reserve/{period}", s.getReserve)
	s.mux.HandleFunc("POST /periods/advance", s.advancePeriod)
	s.mux.HandleFunc("POST /dispatch/{period}/release", s.releasePeriod)
	s.mux.HandleFunc("POST /reconcile", s.reconcile)
	s.mux.HandleFunc("POST /periods", s.seedPeriod)
	s.mux.HandleFunc("GET /periods", s.listPeriods)
}

type runDispatchReq struct {
	Period int `json:"period"`
}

func (s *Server) runDispatch(w http.ResponseWriter, r *http.Request) {
	var req runDispatchReq
	_ = decodeJSON(r, &req)
	res, err := s.svc.RunDispatch(r.Context(), req.Period)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) getDispatch(w http.ResponseWriter, r *http.Request) {
	period, err := strconv.Atoi(r.PathValue("period"))
	if err != nil {
		writeErr(w, berr.Invalidf("invalid period"))
		return
	}
	res, err := s.svc.GetDispatch(r.Context(), period)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) getViolations(w http.ResponseWriter, r *http.Request) {
	period, err := strconv.Atoi(r.PathValue("period"))
	if err != nil {
		writeErr(w, berr.Invalidf("invalid period"))
		return
	}
	res, err := s.svc.GetDispatch(r.Context(), period)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"period": period, "verdict": res.Verdict, "violations": res.Violations})
}

func (s *Server) getReserve(w http.ResponseWriter, r *http.Request) {
	period, err := strconv.Atoi(r.PathValue("period"))
	if err != nil {
		writeErr(w, berr.Invalidf("invalid period"))
		return
	}
	reserve, req, ok, err := s.svc.GetReserve(r.Context(), period)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"period": period, "reserve_mw": reserve, "requirement_mw": req, "adequate": ok,
	})
}

func (s *Server) advancePeriod(w http.ResponseWriter, r *http.Request) {
	seq, err := s.svc.AdvancePeriod(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"period": seq})
}

func (s *Server) releasePeriod(w http.ResponseWriter, r *http.Request) {
	period, err := strconv.Atoi(r.PathValue("period"))
	if err != nil {
		writeErr(w, berr.Invalidf("invalid period"))
		return
	}
	if err := s.svc.ReleasePeriod(r.Context(), period); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"period": period, "status": "committed"})
}

func (s *Server) reconcile(w http.ResponseWriter, r *http.Request) {
	msg, err := s.svc.Reconcile(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"message": msg})
}

type seedReq struct {
	ReserveReq float64 `json:"reserve_req"`
}

func (s *Server) seedPeriod(w http.ResponseWriter, r *http.Request) {
	var req seedReq
	_ = decodeJSON(r, &req)
	seq, err := s.svc.SeedPeriod(r.Context(), req.ReserveReq)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"period": seq})
}

func (s *Server) listPeriods(w http.ResponseWriter, r *http.Request) {
	ps, err := s.store.ListPeriods(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"periods": ps})
}
