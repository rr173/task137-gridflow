// Package httpapi exposes the engine over HTTP using only the standard
// library. It owns the request multiplexer, JSON helpers, error mapping, and
// the smoke-test subcommand dispatcher.
package httpapi

import (
	"encoding/json"
	"net/http"

	"task137-gridflow/internal/berr"
	"task137-gridflow/internal/service"
	"task137-gridflow/internal/store"
	"task137-gridflow/internal/webfs"
)

// Server wires the store, service, and routes together.
type Server struct {
	store   *store.Store
	svc     *service.Service
	mux     *http.ServeMux
}

// New constructs the server and registers all routes.
func New(s *store.Store, svc *service.Service) *Server {
	srv := &Server{store: s, svc: svc, mux: http.NewServeMux()}
	srv.routes()
	return srv
}

// Handler returns the http.Handler (used by httptest and main).
func (s *Server) Handler() http.Handler {
	return s.mux
}

// routes registers every API route plus the embedded frontend.
func (s *Server) routes() {
	// Frontend (embedded).
	if h, ok := webfs.Handler(); ok {
		s.mux.Handle("/", h)
	}

	registerNetwork(s)
	registerDispatch(s)
	registerTopology(s)
	registerQuery(s)
}

// writeJSON encodes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr maps a berr.Error to its HTTP status and writes a JSON body.
func writeErr(w http.ResponseWriter, err error) {
	if err == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	if be, ok := err.(*berr.Error); ok {
		writeJSON(w, be.Code.HTTPStatus(), map[string]any{
			"error": be.Msg,
			"code":  string(be.Code),
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error": err.Error(),
		"code":  "internal",
	})
}

// decodeJSON decodes the request body into v.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
