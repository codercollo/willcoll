package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
)

// serve starts the HTTP server and gracefully shuts it down on SIGINT/SIGTERM.
func (app *application) serve() error {
	router, err := app.routes()
	if err != nil {
		return fmt.Errorf("configuring routes: %w", err)
	}

	// Daily usage-reporting safety net (build-phases.txt 3e); a no-op when
	// usage reporting is disabled. Stops when jobsCtx is cancelled below.
	jobsCtx, stopJobs := context.WithCancel(context.Background())
	defer stopJobs()
	app.startUsageRecountJob(jobsCtx)

	// payment_inbox sweeper (build-phases.txt 3g-3); a no-op when there is
	// no platform connection. Stops when jobsCtx is cancelled below.
	app.startInboxSweeperJob(jobsCtx)

	// Owner-alert relay (C4.0): moves alert_outbox rows to the platform's
	// email outbox. Also a no-op without a platform connection.
	app.startAlertRelayJob(jobsCtx)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", app.config.port),
		Handler:      router,
		IdleTimeout:  time.Minute,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		ErrorLog:     slog.NewLogLogger(app.logger.Handler(), slog.LevelError),
	}

	// Internal /debug/vars listener (off unless METRICS_ADDR is set).
	var metricsSrv *http.Server
	if app.config.metricsAddr != "" {
		metricsSrv = httpx.NewMetricsServer(app.config.metricsAddr, app.logger)
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				app.logger.Error("metrics listener", "addr", metricsSrv.Addr, "error", err)
			}
		}()
		app.logger.Info("metrics listener (internal only)", "addr", metricsSrv.Addr)
	}

	shutdownError := make(chan error)

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		s := <-quit

		app.logger.Info("shutting down server", "signal", s.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if metricsSrv != nil {
			_ = metricsSrv.Shutdown(ctx)
		}
		if err := srv.Shutdown(ctx); err != nil {
			shutdownError <- err
			return
		}

		// Wait for any in-flight background() goroutines (emails, SMS, PDF
		// renders) to finish before letting main() exit.
		app.logger.Info("completing background tasks", "addr", srv.Addr)
		app.wg.Wait()

		shutdownError <- nil
	}()

	app.logger.Info("starting server", "addr", srv.Addr, "env", app.config.env)

	err = srv.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	err = <-shutdownError
	if err != nil {
		return err
	}

	app.logger.Info("stopped server", "addr", srv.Addr)

	return nil
}
