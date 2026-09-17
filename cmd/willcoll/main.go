// Command willcoll is the API server: it wires config, DB pool, router,
// background dispatch, and graceful shutdown (project-structure.txt §cmd).
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/codercollo/willcoll-sys/internal/api"
	"github.com/codercollo/willcoll-sys/internal/auth"
	"github.com/codercollo/willcoll-sys/internal/billing"
	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/config"
	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/codercollo/willcoll-sys/internal/mailer"
	"github.com/codercollo/willcoll-sys/internal/money"
	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/codercollo/willcoll-sys/internal/payments"
	"github.com/codercollo/willcoll-sys/internal/scoring"
	"github.com/codercollo/willcoll-sys/internal/subscriptions"
	"github.com/codercollo/willcoll-sys/internal/tenancy"
	"github.com/codercollo/willcoll-sys/pkg/intasendclient"
	"github.com/codercollo/willcoll-sys/pkg/smsclient"
)

// lateFeeInterval is how often the nightly late-fee job runs.
const lateFeeInterval = 24 * time.Hour

func main() {
	if err := run(); err != nil {
		slog.Error("willcoll: fatal", "error", err)
		os.Exit(1)
	}
}

// run wires every dependency and blocks in Server.Run until SIGINT/SIGTERM,
// at which point Server.Run itself performs the HTTP graceful shutdown
// (LGF §12); run then drains the background dispatchers and closes the pool
// before returning, so nothing outlives the process.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	pasetoKey, err := hex.DecodeString(cfg.PASETOKey)
	if err != nil {
		return fmt.Errorf("decode PASETO key: %w", err)
	}

	pool, err := db.NewPoolWithConfig(context.Background(), cfg.DatabaseDSN, db.PoolConfig{
		MaxConns:        cfg.DBMaxConns,
		MaxConnIdleTime: cfg.DBMaxConnIdleTime,
		MaxConnLifetime: cfg.DBMaxConnLifetime,
	})
	if err != nil {
		return fmt.Errorf("open db pool: %w", err)
	}
	defer pool.Close()

	authService := auth.NewService(pasetoKey, pool)
	tenancyService := tenancy.NewService(pool)
	brandingService := branding.NewService(pool)
	moneyService := money.NewService(pool)

	smtpSender := mailer.NewService(mailer.SMTPConfig{
		Host:     cfg.SMTP.Host,
		Port:     cfg.SMTP.Port,
		Username: cfg.SMTP.Username,
		Password: cfg.SMTP.Password,
		Sender:   cfg.SMTP.Sender,
	})
	mailerDispatcher := mailer.NewDispatcher(smtpSender)
	mailerDispatcher.Start()
	defer mailerDispatcher.Stop()

	smsGateway := smsclient.NewClient(cfg.SMS.GatewayURL, cfg.SMS.APIKey, cfg.SMS.APISecret, cfg.SMS.SenderID)
	notifyService := notify.NewService(smsGateway, brandingService)
	notifyService.Start()
	defer notifyService.Stop()

	intasendGateway := intasendclient.NewClient(intasendBaseURL(cfg.IntaSend.Environment), cfg.IntaSend.SecretKey)
	subscriptionsService := subscriptions.NewService(pool, intasendGateway, cfg.IntaSend.WebhookSecret)
	paymentsService := payments.NewService(pool, intasendGateway, moneyService, notifyService, cfg.IntaSend.WebhookSecret)
	scoringService := scoring.NewService(pool, cfg.Scoring.SidecarBaseURL, cfg.Scoring.SharedSecret)

	billingService := billing.NewService(moneyService, pool, notifyService)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	stopLateFees, lateFeesDone := runNightlyLateFees(billingService, logger)
	defer func() {
		stopLateFees()
		<-lateFeesDone
	}()

	server := api.NewServer(pool, authService, tenancyService, mailerDispatcher, brandingService, moneyService, notifyService, subscriptionsService, paymentsService, scoringService)

	return server.Run(cfg.ServerAddr)
}

// runNightlyLateFees starts the ChargeLateFees background job (spec §4.3
// policy layer, LGF §12 graceful-shutdown pattern) on a fixed interval,
// running once immediately at startup. Calling the returned stop function
// signals the loop to exit after any run already in progress finishes — a
// run in flight is never cancelled mid-way, only the *next* one is
// prevented, matching how the mailer/notify dispatchers drain (Phase 11).
// The returned channel closes once the goroutine has actually exited, so the
// caller can wait for it during shutdown.
func runNightlyLateFees(billingService *billing.Service, logger *slog.Logger) (stop func(), done <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})

	var once sync.Once
	stop = func() { once.Do(cancel) }

	go func() {
		defer close(doneCh)

		chargeLateFeesOnce(billingService, logger)

		ticker := time.NewTicker(lateFeeInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				chargeLateFeesOnce(billingService, logger)
			}
		}
	}()

	return stop, doneCh
}

// chargeLateFeesOnce runs one ChargeLateFees pass against a fresh background
// context — deliberately not the shutdown context, so a run already
// underway when SIGINT/SIGTERM arrives completes rather than being aborted
// mid-charge.
func chargeLateFeesOnce(billingService *billing.Service, logger *slog.Logger) {
	report, err := billingService.ChargeLateFees(context.Background())
	if err != nil {
		logger.Error("charge late fees", "error", err)
		return
	}
	logger.Info("charged late fees", "invoices_charged", report.InvoicesCharged, "amount_charged_cents", report.AmountCharged)
}

// intasendBaseURL picks IntaSend's live or sandbox API host for environment
// (config.intasend.environment / INTASEND_ENVIRONMENT).
func intasendBaseURL(environment string) string {
	if environment == "live" {
		return "https://payment.intasend.com/api/v1"
	}
	return "https://sandbox.intasend.com/api/v1"
}
