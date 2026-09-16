package api

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

	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/mailer"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/codercollo/willcoll-sys/internal/reports"
	"github.com/codercollo/willcoll-sys/internal/subscriptions"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/julienschmidt/httprouter"
)

// Server is the HTTP shell. It holds the router, the DB pool, and the service
// objects the handlers will call. Services are constructor-injected here and
// shared across every handler, never built inside a handler.
type Server struct {
	router        *httprouter.Router
	pool          *pgxpool.Pool
	auth          *auth.Service
	tenancy       *tenancy.Service
	mailer        mailer.Mailer
	branding      *branding.Service
	money         *money.Service
	notify        *notify.Service
	reports       *reports.Service
	subscriptions *subscriptions.Service
	logger        *slog.Logger
}

// NewServer wires the router, DB pool, and services into a single Server.
func NewServer(pool *pgxpool.Pool, authService *auth.Service, tenancyService *tenancy.Service, mailerService mailer.Mailer, brandingService *branding.Service, moneyService *money.Service, notifyService *notify.Service, subscriptionsService *subscriptions.Service) *Server {
	s := &Server{
		router:        httprouter.New(),
		pool:          pool,
		auth:          authService,
		tenancy:       tenancyService,
		mailer:        mailerService,
		branding:      brandingService,
		money:         moneyService,
		notify:        notifyService,
		reports:       reports.NewService(pool),
		subscriptions: subscriptionsService,
		logger:        slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	s.registerRoutes()

	return s
}

// Handler returns the fully-wired middleware chain. It is used by Run and by
// integration tests that need to exercise the router over HTTP without opening
// a real listening socket.
func (s *Server) Handler() http.Handler {
	return s.routes()
}

// Run starts the HTTP server on addr (the configured server.addr / SERVER_ADDR,
// read from config in cmd/willcoll/main.go) and blocks until SIGINT or SIGTERM
// is received, then shuts the server down gracefully.
func (s *Server) Run(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		s.logger.Info("starting HTTP server", "addr", addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		s.logger.Info("shutdown signal received", "signal", ctx.Err())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	s.logger.Info("server stopped")
	return nil
}
