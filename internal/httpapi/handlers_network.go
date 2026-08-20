package httpapi

import (
	"net/http"
	"strconv"

	"task137-gridflow/internal/berr"
	"task137-gridflow/internal/domain"
	"task137-gridflow/internal/store"
)

// registerNetwork wires the topology-definition routes.
func registerNetwork(s *Server) {
	s.mux.HandleFunc("POST /buses", s.createBus)
	s.mux.HandleFunc("GET /buses", s.listBuses)
	s.mux.HandleFunc("POST /branches", s.createBranch)
	s.mux.HandleFunc("GET /branches", s.listBranches)
	s.mux.HandleFunc("POST /generators", s.createGenerator)
	s.mux.HandleFunc("GET /generators", s.listGenerators)
	s.mux.HandleFunc("POST /loads", s.upsertLoad)
	s.mux.HandleFunc("GET /loads", s.listLoads)
}

func (s *Server) createBus(w http.ResponseWriter, r *http.Request) {
	var b domain.Bus
	if err := decodeJSON(r, &b); err != nil {
		writeErr(w, berr.Invalidf("invalid bus: %v", err))
		return
	}
	if b.ID == "" {
		writeErr(w, berr.Invalidf("bus id required"))
		return
	}
	if _, err := domain.ParseBusType(string(b.Type)); err != nil {
		writeErr(w, berr.Invalidf("%v", err))
		return
	}
	if b.VNom <= 0 {
		b.VNom = 1.0
	}
	if b.VMin == 0 {
		b.VMin = 0.95
	}
	if b.VMax == 0 {
		b.VMax = 1.05
	}
	if b.VSpec == 0 {
		b.VSpec = 1.0
	}
	if err := s.store.CreateBus(r.Context(), b); err != nil {
		writeErr(w, berr.Wrap(err))
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) listBuses(w http.ResponseWriter, r *http.Request) {
	bs, err := s.store.ListBuses(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"buses": bs})
}

func (s *Server) createBranch(w http.ResponseWriter, r *http.Request) {
	var b domain.Branch
	if err := decodeJSON(r, &b); err != nil {
		writeErr(w, berr.Invalidf("invalid branch: %v", err))
		return
	}
	if b.ID == "" || b.FromBus == "" || b.ToBus == "" {
		writeErr(w, berr.Invalidf("branch id, from_bus, to_bus required"))
		return
	}
	if b.Tap == 0 {
		b.Tap = 1.0
	}
	b.InService = true
	if err := s.store.CreateBranch(r.Context(), b); err != nil {
		writeErr(w, berr.Wrap(err))
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	bs, err := s.store.ListBranches(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"branches": bs})
}

func (s *Server) createGenerator(w http.ResponseWriter, r *http.Request) {
	var g domain.Generator
	if err := decodeJSON(r, &g); err != nil {
		writeErr(w, berr.Invalidf("invalid generator: %v", err))
		return
	}
	if g.ID == "" || g.BusID == "" {
		writeErr(w, berr.Invalidf("generator id and bus_id required"))
		return
	}
	bus, err := s.store.GetBus(r.Context(), g.BusID)
	if err != nil {
		writeErr(w, berr.Wrap(err))
		return
	}
	if bus.Type != domain.BusTypePV && bus.Type != domain.BusTypeSlack {
		writeErr(w, berr.Invalidf("generators must attach to a PV or slack bus, not %s", bus.Type))
		return
	}
	if g.PMin < 0 || g.PMax <= 0 || g.PMin > g.PMax {
		writeErr(w, berr.Invalidf("invalid pmin/pmax"))
		return
	}
	if g.Status == "" {
		g.Status = domain.GenOffline
	}
	// a generator created already COMMITTED has been online for one period,
	// matching the CommitGenerator path (which sets UpPeriods=1).
	if g.Status == domain.GenCommitted && g.UpPeriods == 0 {
		g.UpPeriods = 1
	}
	if err := s.store.CreateGenerator(r.Context(), g); err != nil {
		writeErr(w, berr.Wrap(err))
		return
	}
	writeJSON(w, 201, g)
}

func (s *Server) listGenerators(w http.ResponseWriter, r *http.Request) {
	gs, err := s.store.ListGenerators(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"generators": gs})
}

func (s *Server) upsertLoad(w http.ResponseWriter, r *http.Request) {
	var l domain.Load
	if err := decodeJSON(r, &l); err != nil {
		writeErr(w, berr.Invalidf("invalid load: %v", err))
		return
	}
	if l.BusID == "" || l.Period <= 0 {
		writeErr(w, berr.Invalidf("bus_id and period required"))
		return
	}
	if err := s.store.UpsertLoad(r.Context(), l); err != nil {
		writeErr(w, berr.Wrap(err))
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) listLoads(w http.ResponseWriter, r *http.Request) {
	period := 0
	if p := r.URL.Query().Get("period"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			writeErr(w, berr.Invalidf("invalid period"))
			return
		}
		period = n
	}
	ctx := r.Context()
	if period > 0 {
		err := s.store.InTx(ctx, func(tx store.DBTX) error {
			ls, err := store.ListLoadsTx(tx, ctx, period)
			if err != nil {
				return err
			}
			writeJSON(w, 200, map[string]any{"loads": ls, "period": period})
			return nil
		})
		if err != nil {
			writeErr(w, err)
		}
		return
	}
	snap, err := s.store.LoadAll(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	var all []domain.Load
	for _, ls := range snap.Loads {
		all = append(all, ls...)
	}
	writeJSON(w, 200, map[string]any{"loads": all})
}
