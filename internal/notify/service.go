package notify

import (
	"context"

	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SMSGateway is the consumer-defined seam for sending an SMS. pkg/smsclient
// implements it.
type SMSGateway interface {
	Send(ctx context.Context, msisdn, body string) error
}

// Service dispatches branded SMS messages to Tenants and Landlords.
type Service struct {
	gateway  SMSGateway
	branding *branding.Service
	pool     *pgxpool.Pool
	dispatch *Dispatcher
}

// NewService constructs a notify Service. A branding service is required so
// templates can resolve {{.BrandName}} without hardcoding a platform brand.
// pool backs the per-Organization template overrides (Phase 8.2) — SendTemplate
// falls back to the shipped-with-the-binary template when an Organization has
// never saved one.
func NewService(smsGateway SMSGateway, brandingService *branding.Service, pool *pgxpool.Pool) *Service {
	return &Service{
		gateway:  smsGateway,
		branding: brandingService,
		pool:     pool,
		dispatch: NewDispatcher(smsGateway),
	}
}

// Start launches the background SMS workers.
func (s *Service) Start() { s.dispatch.Start() }

// Stop drains the SMS queue and waits for workers to finish.
func (s *Service) Stop() { s.dispatch.Stop() }
