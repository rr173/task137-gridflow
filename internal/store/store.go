// Package store is the SQLite persistence layer. It owns the schema, all
// CRUD operations, transactions, and the full-state LoadAll used for restart
// recovery. Reads inside a transaction use the tx-aware variants to avoid
// SQLite deadlocks under SetMaxOpenConns(1) (a transaction holds the single
// connection, so any read on the shared *sql.DB inside it would block).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"task137-gridflow/internal/domain"

	_ "modernc.org/sqlite"
)

// DBTX is the common interface satisfied by *sql.DB and *sql.Tx. All read
// helpers accept it so the same code works inside and outside a transaction.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database and runs migrations.
func Open(path string) (*Store, error) {
	// For ":memory:" use a shared-cache in-memory database so the single
	// connection sees a consistent, non-persistent store.
	dsn := path
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	// Single connection avoids write contention and the WAL-reader race.
	db.SetMaxOpenConns(1)
	// Enforce foreign keys and a sane busy timeout directly (the _pragma DSN
	// form is driver-version specific; plain PRAGMAs are reliable).
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB (used only for tx-outside aggregate reads).
func (s *Store) DB() *sql.DB { return s.db }

// InTx runs fn inside a single transaction.
func (s *Store) InTx(ctx context.Context, fn func(tx DBTX) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS buses (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  vnom REAL NOT NULL,
  vmin REAL NOT NULL DEFAULT 0.95,
  vmax REAL NOT NULL DEFAULT 1.05,
  vspec REAL NOT NULL DEFAULT 1.0,
  theta_spec REAL NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS branches (
  id TEXT PRIMARY KEY,
  from_bus TEXT NOT NULL,
  to_bus TEXT NOT NULL,
  kind TEXT NOT NULL,
  r REAL NOT NULL,
  x REAL NOT NULL,
  b_shunt REAL NOT NULL DEFAULT 0,
  tap REAL NOT NULL DEFAULT 1.0,
  mva_limit REAL NOT NULL,
  in_service INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY(from_bus) REFERENCES buses(id),
  FOREIGN KEY(to_bus) REFERENCES buses(id)
);
CREATE TABLE IF NOT EXISTS generators (
  id TEXT PRIMARY KEY,
  bus_id TEXT NOT NULL,
  pmin REAL NOT NULL,
  pmax REAL NOT NULL,
  ramp_mw REAL NOT NULL,
  min_up INTEGER NOT NULL,
  min_down INTEGER NOT NULL,
  qmin REAL NOT NULL DEFAULT 0,
  qmax REAL NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'offline',
  up_periods INTEGER NOT NULL DEFAULT 0,
  down_periods INTEGER NOT NULL DEFAULT 0,
  p_output REAL NOT NULL DEFAULT 0,
  FOREIGN KEY(bus_id) REFERENCES buses(id)
);
CREATE TABLE IF NOT EXISTS loads (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bus_id TEXT NOT NULL,
  period INTEGER NOT NULL,
  p_mw REAL NOT NULL,
  q_mvar REAL NOT NULL,
  UNIQUE(bus_id, period),
  FOREIGN KEY(bus_id) REFERENCES buses(id)
);
CREATE TABLE IF NOT EXISTS periods (
  seq INTEGER PRIMARY KEY,
  status TEXT NOT NULL DEFAULT 'planned',
  total_gen REAL NOT NULL DEFAULT 0,
  total_load REAL NOT NULL DEFAULT 0,
  total_loss REAL NOT NULL DEFAULT 0,
  verdict TEXT NOT NULL DEFAULT 'feasible',
  reserve_req REAL NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS period_gen_results (
  period INTEGER NOT NULL,
  gen_id TEXT NOT NULL,
  p_output REAL NOT NULL,
  q_output REAL NOT NULL,
  committed INTEGER NOT NULL,
  PRIMARY KEY(period, gen_id)
);
CREATE TABLE IF NOT EXISTS period_bus_results (
  period INTEGER NOT NULL,
  bus_id TEXT NOT NULL,
  vmag REAL NOT NULL,
  theta REAL NOT NULL,
  PRIMARY KEY(period, bus_id)
);
CREATE TABLE IF NOT EXISTS period_flow_results (
  period INTEGER NOT NULL,
  branch_id TEXT NOT NULL,
  p_from REAL NOT NULL,
  q_from REAL NOT NULL,
  s_mva REAL NOT NULL,
  loading_pct REAL NOT NULL,
  overload INTEGER NOT NULL,
  PRIMARY KEY(period, branch_id)
);
CREATE TABLE IF NOT EXISTS period_violations (
  period INTEGER NOT NULL,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL,
  ref TEXT NOT NULL,
  detail TEXT NOT NULL,
  value REAL NOT NULL,
  limit_val REAL NOT NULL,
  PRIMARY KEY(period, seq)
);
CREATE TABLE IF NOT EXISTS events (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  period INTEGER NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS cases (
  name TEXT PRIMARY KEY,
  snapshot TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS app_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

// ---------------- Buses ----------------

func (s *Store) CreateBus(ctx context.Context, b domain.Bus) error {
	return s.InTx(ctx, func(tx DBTX) error {
		return CreateBusTx(tx, ctx, b)
	})
}

// CreateBusTx inserts a bus inside an existing transaction.
func CreateBusTx(tx DBTX, ctx context.Context, b domain.Bus) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO buses(id,name,type,vnom,vmin,vmax,vspec,theta_spec) VALUES(?,?,?,?,?,?,?,?)`,
		b.ID, b.Name, string(b.Type), b.VNom, b.VMin, b.VMax, b.VSpec, b.ThetaSpec)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "constraint") {
			return fmt.Errorf("bus %s already exists", b.ID)
		}
		return fmt.Errorf("insert bus: %w", err)
	}
	return nil
}

// ListBuses returns all buses.
func (s *Store) ListBuses(ctx context.Context) ([]domain.Bus, error) {
	return ListBusesTx(s.db, ctx)
}

// ListBusesTx returns all buses using the given DBTX (use the tx variant
// inside a transaction).
func ListBusesTx(tx DBTX, ctx context.Context) ([]domain.Bus, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,name,type,vnom,vmin,vmax,vspec,theta_spec FROM buses ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Bus
	for rows.Next() {
		var b domain.Bus
		var t string
		if err := rows.Scan(&b.ID, &b.Name, &t, &b.VNom, &b.VMin, &b.VMax, &b.VSpec, &b.ThetaSpec); err != nil {
			return nil, err
		}
		b.Type = domain.BusType(t)
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBusTx fetches a single bus by id inside a transaction.
func GetBusTx(tx DBTX, ctx context.Context, id string) (domain.Bus, error) {
	var b domain.Bus
	var t string
	err := tx.QueryRowContext(ctx, `SELECT id,name,type,vnom,vmin,vmax,vspec,theta_spec FROM buses WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &t, &b.VNom, &b.VMin, &b.VMax, &b.VSpec, &b.ThetaSpec)
	if err == sql.ErrNoRows {
		return b, fmt.Errorf("bus %s not found", id)
	}
	if err != nil {
		return b, err
	}
	b.Type = domain.BusType(t)
	return b, nil
}

// GetBus fetches a single bus by id.
func (s *Store) GetBus(ctx context.Context, id string) (domain.Bus, error) {
	return GetBusTx(s.db, ctx, id)
}

// GetGenerator fetches a single generator by id.
func (s *Store) GetGenerator(ctx context.Context, id string) (domain.Generator, error) {
	return GetGeneratorTx(s.db, ctx, id)
}

// ---------------- Branches ----------------

func (s *Store) CreateBranch(ctx context.Context, b domain.Branch) error {
	return s.InTx(ctx, func(tx DBTX) error {
		return CreateBranchTx(tx, ctx, b)
	})
}

// CreateBranchTx inserts a branch inside an existing transaction.
func CreateBranchTx(tx DBTX, ctx context.Context, b domain.Branch) error {
	svc := 1
	if !b.InService {
		svc = 0
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO branches(id,from_bus,to_bus,kind,r,x,b_shunt,tap,mva_limit,in_service) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.FromBus, b.ToBus, string(b.Kind), b.R, b.X, b.BShunt, b.Tap, b.MVALimit, svc)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "FOREIGN KEY") {
			return fmt.Errorf("branch %s invalid or duplicate", b.ID)
		}
		return fmt.Errorf("insert branch: %w", err)
	}
	return nil
}

// ListBranches returns all branches.
func (s *Store) ListBranches(ctx context.Context) ([]domain.Branch, error) {
	return ListBranchesTx(s.db, ctx)
}

// ListBranchesTx returns all branches using the given DBTX.
func ListBranchesTx(tx DBTX, ctx context.Context) ([]domain.Branch, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,from_bus,to_bus,kind,r,x,b_shunt,tap,mva_limit,in_service FROM branches ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Branch
	for rows.Next() {
		var b domain.Branch
		var k string
		var svc int
		if err := rows.Scan(&b.ID, &b.FromBus, &b.ToBus, &k, &b.R, &b.X, &b.BShunt, &b.Tap, &b.MVALimit, &svc); err != nil {
			return nil, err
		}
		b.Kind = domain.BranchKind(k)
		b.InService = svc != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBranchServiceTx toggles a branch's in_service flag inside a transaction.
func SetBranchServiceTx(tx DBTX, ctx context.Context, id string, inService bool) error {
	svc := 0
	if inService {
		svc = 1
	}
	res, err := tx.ExecContext(ctx, `UPDATE branches SET in_service=? WHERE id=?`, svc, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("branch %s not found", id)
	}
	return nil
}

// ---------------- Generators ----------------

func (s *Store) CreateGenerator(ctx context.Context, g domain.Generator) error {
	return s.InTx(ctx, func(tx DBTX) error {
		return CreateGeneratorTx(tx, ctx, g)
	})
}

// CreateGeneratorTx inserts a generator inside an existing transaction.
func CreateGeneratorTx(tx DBTX, ctx context.Context, g domain.Generator) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO generators(id,bus_id,pmin,pmax,ramp_mw,min_up,min_down,qmin,qmax,status,up_periods,down_periods,p_output)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.BusID, g.PMin, g.PMax, g.RampMW, g.MinUp, g.MinDown, g.QMin, g.QMax,
		string(g.Status), g.UpPeriods, g.DownPeriods, g.POutput)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "FOREIGN KEY") {
			return fmt.Errorf("generator %s invalid or duplicate", g.ID)
		}
		return fmt.Errorf("insert generator: %w", err)
	}
	return nil
}

// ListGenerators returns all generators.
func (s *Store) ListGenerators(ctx context.Context) ([]domain.Generator, error) {
	return ListGeneratorsTx(s.db, ctx)
}

// ListGeneratorsTx returns all generators using the given DBTX.
func ListGeneratorsTx(tx DBTX, ctx context.Context) ([]domain.Generator, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id,bus_id,pmin,pmax,ramp_mw,min_up,min_down,qmin,qmax,status,up_periods,down_periods,p_output FROM generators ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Generator
	for rows.Next() {
		var g domain.Generator
		var st string
		if err := rows.Scan(&g.ID, &g.BusID, &g.PMin, &g.PMax, &g.RampMW, &g.MinUp, &g.MinDown, &g.QMin, &g.QMax, &st, &g.UpPeriods, &g.DownPeriods, &g.POutput); err != nil {
			return nil, err
		}
		g.Status = domain.GenStatus(st)
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGeneratorTx fetches a generator inside a transaction.
func GetGeneratorTx(tx DBTX, ctx context.Context, id string) (domain.Generator, error) {
	var g domain.Generator
	var st string
	err := tx.QueryRowContext(ctx,
		`SELECT id,bus_id,pmin,pmax,ramp_mw,min_up,min_down,qmin,qmax,status,up_periods,down_periods,p_output FROM generators WHERE id=?`, id).
		Scan(&g.ID, &g.BusID, &g.PMin, &g.PMax, &g.RampMW, &g.MinUp, &g.MinDown, &g.QMin, &g.QMax, &st, &g.UpPeriods, &g.DownPeriods, &g.POutput)
	if err == sql.ErrNoRows {
		return g, fmt.Errorf("generator %s not found", id)
	}
	if err != nil {
		return g, err
	}
	g.Status = domain.GenStatus(st)
	return g, nil
}

// SetGeneratorStateTx updates a generator's status, counters and output.
func SetGeneratorStateTx(tx DBTX, ctx context.Context, g domain.Generator) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE generators SET status=?,up_periods=?,down_periods=?,p_output=? WHERE id=?`,
		string(g.Status), g.UpPeriods, g.DownPeriods+1, g.POutput, g.ID)
	return err
}

// ---------------- Loads ----------------

// UpsertLoad inserts or replaces a load for a (bus, period).
func (s *Store) UpsertLoad(ctx context.Context, l domain.Load) error {
	return s.InTx(ctx, func(tx DBTX) error {
		return UpsertLoadTx(tx, ctx, l)
	})
}

// UpsertLoadTx upserts a load inside a transaction.
func UpsertLoadTx(tx DBTX, ctx context.Context, l domain.Load) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO loads(bus_id,period,p_mw,q_mvar) VALUES(?,?,?,?)
		 ON CONFLICT(bus_id,period) DO UPDATE SET p_mw=excluded.p_mw, q_mvar=excluded.q_mvar`,
		l.BusID, l.Period, l.PMW, l.QMvar)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return fmt.Errorf("bus %s not found", l.BusID)
		}
		return fmt.Errorf("upsert load: %w", err)
	}
	return nil
}

// ListLoadsTx returns all loads for a period inside a transaction.
func ListLoadsTx(tx DBTX, ctx context.Context, period int) ([]domain.Load, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,bus_id,period,p_mw,q_mvar FROM loads WHERE period=?`, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Load
	for rows.Next() {
		var l domain.Load
		if err := rows.Scan(&l.ID, &l.BusID, &l.Period, &l.PMW, &l.QMvar); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ---------------- Periods ----------------

// UpsertPeriodTx inserts or updates a period inside a transaction.
func UpsertPeriodTx(tx DBTX, ctx context.Context, p domain.Period) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO periods(seq,status,total_gen,total_load,total_loss,verdict,reserve_req)
		 VALUES(?,?,?,?,?,?,?) ON CONFLICT(seq) DO UPDATE SET status=excluded.status,total_gen=excluded.total_gen,
		 total_load=excluded.total_load,total_loss=excluded.total_loss,verdict=excluded.verdict,reserve_req=excluded.reserve_req`,
		p.Seq, string(p.Status), p.TotalGen, p.TotalLoad, p.TotalLoss, string(p.Verdict), p.ReserveReq)
	return err
}

// ListPeriods returns all periods.
func (s *Store) ListPeriods(ctx context.Context) ([]domain.Period, error) {
	return ListPeriodsTx(s.db, ctx)
}

// ListPeriodsTx returns all periods using the given DBTX.
func ListPeriodsTx(tx DBTX, ctx context.Context) ([]domain.Period, error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq,status,total_gen,total_load,total_loss,verdict,reserve_req FROM periods ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Period
	for rows.Next() {
		var p domain.Period
		var st, vd string
		if err := rows.Scan(&p.Seq, &st, &p.TotalGen, &p.TotalLoad, &p.TotalLoss, &vd, &p.ReserveReq); err != nil {
			return nil, err
		}
		p.Status = domain.PeriodStatus(st)
		p.Verdict = domain.Verdict(vd)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPeriodTx fetches a single period inside a transaction.
func GetPeriodTx(tx DBTX, ctx context.Context, seq int) (domain.Period, error) {
	var p domain.Period
	var st, vd string
	err := tx.QueryRowContext(ctx, `SELECT seq,status,total_gen,total_load,total_loss,verdict,reserve_req FROM periods WHERE seq=?`, seq).
		Scan(&p.Seq, &st, &p.TotalGen, &p.TotalLoad, &p.TotalLoss, &vd, &p.ReserveReq)
	if err == sql.ErrNoRows {
		return p, fmt.Errorf("period %d not found", seq)
	}
	if err != nil {
		return p, err
	}
	p.Status = domain.PeriodStatus(st)
	p.Verdict = domain.Verdict(vd)
	return p, nil
}

// ---------------- Period results ----------------

// SavePeriodResultsTx stores a period's full solution snapshot inside a transaction.
func SavePeriodResultsTx(tx DBTX, ctx context.Context, period int, gen map[string][2]float64, buses map[string][2]float64, flows map[string]FlowRow, vios []domain.Violation) error {
	// clear prior results for the period
	if _, err := tx.ExecContext(ctx, `DELETE FROM period_gen_results WHERE period=?`, period); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM period_bus_results WHERE period=?`, period); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM period_flow_results WHERE period=?`, period); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM period_violations WHERE period=?`, period); err != nil {
		return err
	}
	for gid, pq := range gen {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO period_gen_results(period,gen_id,p_output,q_output,committed) VALUES(?,?,?,?,1)`,
			period, gid, pq[0], pq[1]); err != nil {
			return err
		}
	}
	for bid, vt := range buses {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO period_bus_results(period,bus_id,vmag,theta) VALUES(?,?,?,?)`,
			period, bid, vt[0], vt[1]); err != nil {
			return err
		}
	}
	for fid, fr := range flows {
		ov := 0
		if fr.Overload {
			ov = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO period_flow_results(period,branch_id,p_from,q_from,s_mva,loading_pct,overload) VALUES(?,?,?,?,?,?,?)`,
			period, fid, fr.PFrom, fr.QFrom, fr.SMVA, fr.Loading, ov); err != nil {
			return err
		}
	}
	for i, v := range vios {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO period_violations(period,seq,kind,ref,detail,value,limit_val) VALUES(?,?,?,?,?,?,?)`,
			period, i, string(v.Kind), v.Ref, v.Detail, v.Value, v.Limit); err != nil {
			return err
		}
	}
	return nil
}

// FlowRow is a stored branch-flow snapshot.
type FlowRow struct {
	PFrom    float64
	QFrom    float64
	SMVA     float64
	Loading  float64
	Overload bool
}

// LoadPeriodResultsTx reads a period's solution snapshot inside a transaction.
func LoadPeriodResultsTx(tx DBTX, ctx context.Context, period int) (gen map[string][2]float64, buses map[string][2]float64, flows map[string]FlowRow, vios []domain.Violation, err error) {
	gen = map[string][2]float64{}
	buses = map[string][2]float64{}
	flows = map[string]FlowRow{}
	rows, qerr := tx.QueryContext(ctx, `SELECT gen_id,p_output,q_output FROM period_gen_results WHERE period=?`, period)
	if qerr != nil {
		return nil, nil, nil, nil, qerr
	}
	for rows.Next() {
		var gid string
		var p, q float64
		if err = rows.Scan(&gid, &p, &q); err != nil {
			rows.Close()
			return
		}
		gen[gid] = [2]float64{p, q}
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT bus_id,vmag,theta FROM period_bus_results WHERE period=?`, period)
	if err != nil {
		return
	}
	for rows.Next() {
		var bid string
		var vm, th float64
		if err = rows.Scan(&bid, &vm, &th); err != nil {
			rows.Close()
			return
		}
		buses[bid] = [2]float64{vm, th}
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT branch_id,p_from,q_from,s_mva,loading_pct,overload FROM period_flow_results WHERE period=?`, period)
	if err != nil {
		return
	}
	for rows.Next() {
		var bid string
		var p, q, s, l float64
		var ov int
		if err = rows.Scan(&bid, &p, &q, &s, &l, &ov); err != nil {
			rows.Close()
			return
		}
		flows[bid] = FlowRow{PFrom: p, QFrom: q, SMVA: s, Loading: l, Overload: ov != 0}
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT kind,ref,detail,value,limit_val FROM period_violations WHERE period=? ORDER BY seq`, period)
	if err != nil {
		return
	}
	for rows.Next() {
		var v domain.Violation
		var k string
		if err = rows.Scan(&k, &v.Ref, &v.Detail, &v.Value, &v.Limit); err != nil {
			rows.Close()
			return
		}
		v.Kind = domain.ViolationKind(k)
		vios = append(vios, v)
	}
	rows.Close()
	return gen, buses, flows, vios, rows.Err()
}

// ---------------- Events, cases, app state, snapshot, restart ----------------
// These live in state.go to keep this file focused on topology CRUD.


