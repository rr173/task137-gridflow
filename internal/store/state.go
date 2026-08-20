// Package store — state.go holds the audit event log, named case snapshots,
// the app_state key/value table, and the full-state LoadAll used for restart
// recovery. Splitting it from store.go keeps the topology CRUD focused.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"task137-gridflow/internal/domain"
)

// ---------------- Events ----------------

// AppendEventTx writes an audit event inside a transaction.
func AppendEventTx(tx DBTX, ctx context.Context, period int, kind domain.EventKind, payload any) error {
	pj, err := json.Marshal(payload)
	if err != nil {
		pj = []byte(`{}`)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(period,kind,payload) VALUES(?,?,?)`, period, string(domain.EventAdvance), string(pj))
	return err
}

// ListEvents returns audit events, optionally limited.
func (s *Store) ListEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	q := `SELECT seq,period,kind,payload FROM events ORDER BY seq DESC`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		var e domain.Event
		var k string
		if err := rows.Scan(&e.Seq, &e.Period, &k, &e.Payload); err != nil {
			return nil, err
		}
		e.Kind = domain.EventKind(k)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------- Cases ----------------

// SaveCase stores a named network snapshot.
func (s *Store) SaveCase(ctx context.Context, name string, snapshot any) error {
	pj, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO cases(name,snapshot) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET snapshot=excluded.snapshot`, name, string(pj))
	return err
}

// ListCases returns all case names.
func (s *Store) ListCases(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM cases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---------------- App state ----------------

// GetState reads an app_state key.
func (s *Store) GetState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_state WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetState writes an app_state key.
func (s *Store) SetState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// SetStateTxLocal writes an app_state key inside an existing transaction.
func SetStateTxLocal(tx DBTX, ctx context.Context, key, value string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO app_state(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// GetBranchTx fetches a single branch inside a transaction.
func GetBranchTx(tx DBTX, ctx context.Context, id string) (domain.Branch, error) {
	var b domain.Branch
	var k string
	var svc int
	err := tx.QueryRowContext(ctx, `SELECT id,from_bus,to_bus,kind,r,x,b_shunt,tap,mva_limit,in_service FROM branches WHERE id=?`, id).
		Scan(&b.ID, &b.FromBus, &b.ToBus, &k, &b.R, &b.X, &b.BShunt, &b.Tap, &b.MVALimit, &svc)
	if err == sql.ErrNoRows {
		return b, fmt.Errorf("branch %s not found", id)
	}
	if err != nil {
		return b, err
	}
	b.Kind = domain.BranchKind(k)
	b.InService = svc != 0
	return b, nil
}

// ---------------- Snapshot for restart ----------------

// Snapshot is the full in-memory state loaded from disk at restart.
type Snapshot struct {
	Buses         []domain.Bus
	Branches      []domain.Branch
	Generators    []domain.Generator
	Periods       []domain.Period
	Loads         map[int][]domain.Load // period -> loads
	CurrentPeriod int
}

// LoadAll rebuilds the full state from disk. Reads happen on the shared *sql.DB
// (no transaction is needed because this is a read-only aggregate pass and no
// write transaction is open).
func (s *Store) LoadAll(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{Loads: map[int][]domain.Load{}}
	var err error
	snap.Buses, err = ListBusesTx(s.db, ctx)
	if err != nil {
		return nil, err
	}
	snap.Branches, err = ListBranchesTx(s.db, ctx)
	if err != nil {
		return nil, err
	}
	snap.Generators, err = ListGeneratorsTx(s.db, ctx)
	if err != nil {
		return nil, err
	}
	snap.Periods, err = ListPeriodsTx(s.db, ctx)
	if err != nil {
		return nil, err
	}
	// loads: group by period
	rows, err := s.db.QueryContext(ctx, `SELECT id,bus_id,period,p_mw,q_mvar FROM loads ORDER BY period,bus_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l domain.Load
		if err := rows.Scan(&l.ID, &l.BusID, &l.Period, &l.PMW, &l.QMvar); err != nil {
			return nil, err
		}
		snap.Loads[l.Period] = append(snap.Loads[l.Period], l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cur, _ := s.GetState(ctx, "current_period")
	if cur != "" {
		fmt.Sscanf(cur, "%d", &snap.CurrentPeriod)
	}
	return snap, nil
}

// ResetAll drops all rows. Used by tests and --smoke-test for a clean slate.
func (s *Store) ResetAll(ctx context.Context) error {
	return s.InTx(ctx, func(tx DBTX) error {
		for _, t := range []string{"period_violations", "period_flow_results", "period_bus_results", "period_gen_results", "events", "loads", "generators", "branches", "buses", "periods", "cases", "app_state"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
				return err
			}
		}
		return nil
	})
}
