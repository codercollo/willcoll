package subscriptions

import (
	"context"

	"github.com/codercollo/willcoll-sys/pkg/intasendclient"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Gateway is the consumer-defined seam for creating an IntaSend checkout that
// settles to Willcoll's own account.
type Gateway interface {
	CreateCheckout(ctx context.Context, req intasendclient.CheckoutRequest) (intasendclient.CheckoutResponse, error)
}

// Service owns Willcoll's own subscription billing. It never touches any
// tenant rent ledger table.
type Service struct {
	pool          *pgxpool.Pool
	gateway       Gateway
	webhookSecret string
}

// NewService constructs a subscriptions Service. webhookSecret is the
// INTASEND_WEBHOOK_SECRET used to verify incoming webhooks.
func NewService(pool *pgxpool.Pool, gateway Gateway, webhookSecret string) *Service {
	return &Service{pool: pool, gateway: gateway, webhookSecret: webhookSecret}
}
