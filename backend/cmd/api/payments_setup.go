package main

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/sms"
)

// stkPusher is what handlers need from the PayHero client. The real client
// satisfies it; tests substitute a fake.
type stkPusher interface {
	STKPush(ctx context.Context, req payhero.STKPushRequest) (*payhero.STKPushResponse, error)
	RegisterChannel(ctx context.Context, req payhero.RegisterChannelRequest) (*payhero.Channel, error)
	ListChannels(ctx context.Context) ([]payhero.Channel, error)
}

// smsSender is what handlers need from the SMS client.
type smsSender interface {
	SendOTP(ctx context.Context, phone, code string, ttl time.Duration) error
	SendPaymentConfirmation(ctx context.Context, phone string, p sms.PaymentConfirmation) error
}

// newPaymentClients builds the PayHero and SMS clients from config. Both are
// always non-nil: an unconfigured client fails each call with ErrNotConfigured
// rather than the process failing to start, so a deployment without PayHero
// or SMS credentials still serves everything else.
func newPaymentClients(cfg config) (*payhero.Client, *sms.Client) {
	return payhero.NewClient(cfg.payhero.baseURL, cfg.payhero.apiKey, nil),
		sms.NewClient(cfg.africastalking.username, cfg.africastalking.apiKey, "", nil)
}

// paymentConfigProblems checks the payment settings that the enabled features
// depend on, and returns one line per problem. Missing settings are reported
// loudly at startup because their absence otherwise only shows up as a failed
// payment. In production they are errors; elsewhere, warnings.
func paymentConfigProblems(cfg config) []string {
	var problems []string
	add := func(s string) { problems = append(problems, s) }

	payheroConfigured := cfg.payhero.baseURL != "" && cfg.payhero.apiKey != ""
	if !payheroConfigured {
		add("PayHero is not configured (PAYHERO_BASE_URL / PAYHERO_API_KEY): STK pushes and subscription billing will fail")
	} else {
		if cfg.payhero.collectionsChannelID == "" {
			add("PAYHERO_COLLECTIONS_CHANNEL_ID is not set: it is the fallback for organic paybill payments that do not name a channel")
		}
		if cfg.payhero.billingChannelID == "" {
			add("PAYHERO_BILLING_CHANNEL_ID is not set: manager subscription payments cannot be requested")
		}
		if cfg.payhero.callbackBaseURL == "" {
			add("PAYHERO_CALLBACK_BASE_URL is not set: PayHero will not know where to send payment confirmations")
		}
		if cfg.payhero.accountID == "" {
			add("PAYHERO_ACCOUNT_ID is not set: a manager cannot register a new payment channel (existing channel IDs entered manually still work)")
		}
	}
	if cfg.payhero.webhookSecret == "" && len(cfg.payhero.webhookIPs) == 0 {
		add("neither PAYHERO_WEBHOOK_SECRET nor PAYHERO_WEBHOOK_IPS is set: every PayHero callback will be refused")
	}
	if cfg.pay.sessionSecret == "" {
		add("PAY_SESSION_SECRET is not set: the public /v1/pay endpoints are disabled")
	}
	if cfg.africastalking.apiKey == "" || cfg.africastalking.username == "" {
		add("Africa's Talking is not configured (AFRICASTALKING_API_KEY / AFRICASTALKING_USERNAME): tenants cannot receive their payment codes or confirmations")
	}
	if cfg.qr.baseURL == "" {
		add("QR_BASE_URL is not set: unit QR codes cannot be generated (it must be the stable public domain printed stickers point at, e.g. https://willcoll.app)")
	} else if u, err := url.Parse(cfg.qr.baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		add("QR_BASE_URL is not an absolute http(s) URL: unit QR codes cannot be generated")
	}
	return problems
}

// logPaymentConfig reports config problems at startup: errors in production,
// warnings elsewhere. It never stops the server, so unrelated features keep
// working; the affected endpoints refuse clearly instead.
func logPaymentConfig(cfg config, logger *slog.Logger) {
	for _, p := range paymentConfigProblems(cfg) {
		if cfg.env == "production" {
			logger.Error("payment configuration", "problem", p)
		} else {
			logger.Warn("payment configuration", "problem", p)
		}
	}
}
