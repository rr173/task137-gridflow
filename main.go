// Command task137-gridflow is the entry point for the power-system load-flow
// and unit-dispatch engine. It serves the HTTP API + embedded frontend, or
// runs one of the maintenance subcommands (--smoke-test, --migrate-only).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"task137-gridflow/internal/clock"
	"task137-gridflow/internal/httpapi"
	"task137-gridflow/internal/selfcheck"
	"task137-gridflow/internal/service"
	"task137-gridflow/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "gridflow.db", "SQLite database path")
	smoke := flag.Bool("smoke-test", false, "run smoke tests and exit")
	migrate := flag.Bool("migrate-only", false, "run migrations and exit")
	flag.Parse()

	if *smoke {
		// Smoke tests always run on a fresh in-memory database.
		selfcheck.RunAndExit(":memory:")
		return
	}

	s, err := store.Open(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// seed the clock from the persisted cursor
	snap, err := s.LoadAll(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "load state: %v\n", err)
		os.Exit(1)
	}
	clk := clock.New(snap.CurrentPeriod)
	svc := service.New(s, clk)
	srv := httpapi.New(s, svc)

	if *migrate {
		fmt.Printf("migrations applied to %s (buses=%d branches=%d generators=%d period=%d)\n",
			*dbPath, len(snap.Buses), len(snap.Branches), len(snap.Generators), snap.CurrentPeriod)
		return
	}

	httpSrv := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
	}

	go func() {
		log.Printf("gridflow listening on %s (db=%s)", *addr, *dbPath)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}
