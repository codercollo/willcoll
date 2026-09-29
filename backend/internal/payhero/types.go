// Package payhero talks to PayHero Kenya (system-design.txt section 7): the
// STK push that asks a tenant or manager to pay, and the callbacks PayHero
// sends back.
//
// The request path, auth header and callback field names follow PayHero's
// public API as understood when this was written, and MUST be checked against
// a live account before go-live. They are isolated here (client.go,
// webhook.go) so a correction touches nothing else. Callback parsing accepts
// the wire format under several key spellings for that reason.
package payhero

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// STKPushRequest asks PayHero to prompt a phone for an M-Pesa payment. It is
// the JSON body of POST {baseURL}/api/v2/payments.
type STKPushRequest struct {
	// Amount is in whole shillings: an STK push cannot carry cents.
	Amount int64 `json:"amount"`
	// PhoneNumber in international form, e.g. 254722000001.
	PhoneNumber string `json:"phone_number"`
	// ChannelID is the PayHero channel the money settles through: a
	// property's collections channel, or Willcoll's own billing channel.
	ChannelID string `json:"channel_id"`
	// Provider is the mobile-money network; NewClient fills in "m-pesa".
	Provider string `json:"provider"`
	// ExternalReference comes back on the callback unchanged and is how a
	// payment is tied to its intent or invoice.
	ExternalReference string `json:"external_reference"`
	CustomerName      string `json:"customer_name,omitempty"`
	// CallbackURL is where PayHero posts the outcome.
	CallbackURL string `json:"callback_url,omitempty"`
}

// Channel type accepted by "Register Payment Channel"
// (docs.payhero.co.ke: Payment Channels → Register Payment Channel).
const (
	ChannelTypeBank    = "bank"
	ChannelTypePaybill = "paybill"
	ChannelTypeTill    = "till"
)

// RegisterChannelRequest is the JSON body of POST
// {baseURL}/api/v2/payment_channels. account_id, short_code are numbers on
// PayHero's wire format; account_number and description are strings.
type RegisterChannelRequest struct {
	ChannelType string `json:"channel_type"` // bank | paybill | till
	AccountID   int64  `json:"account_id"`
	// ShortCode is the M-Pesa paybill/till number, or (for a bank channel)
	// the bank's own M-Pesa paybill number.
	ShortCode int64 `json:"short_code"`
	// AccountNumber is the paybill account number, or — for a bank channel —
	// the landlord's bank account number.
	AccountNumber string `json:"account_number"`
	Description   string `json:"description,omitempty"`
}

// Channel is a registered PayHero payment channel, returned by both Register
// Payment Channel and Get Payment Channels. Confirmed live against
// GET {baseURL}/api/v2/payment_channels on 2026-09-28: id, account_id are
// JSON numbers, but short_code — despite being documented as a number — is
// actually returned as a JSON string (e.g. "522522"); UnmarshalJSON below
// accepts either so callers (and tests) can just use a plain int64.
type Channel struct {
	ID              int64  `json:"id"`
	ChannelType     string `json:"channel_type"`
	TransactionType string `json:"transaction_type"`
	AccountID       int64  `json:"account_id"`
	ShortCode       int64  `json:"short_code"`
	AccountNumber   string `json:"account_number"`
	Description     string `json:"description"`
	IsActive        bool   `json:"is_active"`
	BalancePlain    string `json:"balance_plain"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// UnmarshalJSON decodes Channel with ShortCode loosened to accept a quoted
// number, since PayHero sends it as a string. "type alias Channel" has
// Channel's fields but none of its methods, so this doesn't recurse.
func (c *Channel) UnmarshalJSON(b []byte) error {
	type alias Channel
	var w struct {
		alias
		ShortCode flexNumber `json:"short_code"`
	}
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*c = Channel(w.alias)
	c.ShortCode = int64(w.ShortCode)
	return nil
}

// flexNumber unmarshals a JSON field PayHero sends inconsistently as either
// a bare number or a quoted number string.
type flexNumber int64

func (n *flexNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("payhero: short_code %q is not a number: %w", s, err)
	}
	*n = flexNumber(v)
	return nil
}

// channelList unmarshals Get Payment Channels' response. Confirmed live on
// 2026-09-28: it wraps the array under "payment_channels", alongside a
// "pagination" object — not a bare array or "data", as the docs' JS-rendered
// page did not yield before that call. Both are still accepted defensively.
type channelList struct {
	Plain   []Channel `json:"-"`
	Wrapped struct {
		PaymentChannels []Channel `json:"payment_channels"`
		Data            []Channel `json:"data"`
	} `json:"-"`
}

func (l *channelList) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &l.Plain); err == nil {
		return nil
	}
	return json.Unmarshal(b, &l.Wrapped)
}

func (l *channelList) channels() []Channel {
	if len(l.Plain) > 0 {
		return l.Plain
	}
	if len(l.Wrapped.PaymentChannels) > 0 {
		return l.Wrapped.PaymentChannels
	}
	return l.Wrapped.Data
}

// STKPushResponse is PayHero acknowledgement that the push was queued. The
// payment itself is confirmed later, by callback.
type STKPushResponse struct {
	Success           bool   `json:"success"`
	Status            string `json:"status"`
	Reference         string `json:"reference"`
	CheckoutRequestID string `json:"CheckoutRequestID"`
}

// CollectionsWebhookPayload is one callback on the collections channel: the
// outcome of an STK push, or an organic paybill payment. Parsed and
// normalised by ParseCollectionsPayload.
type CollectionsWebhookPayload struct {
	// Success is true when money actually moved. A failed or cancelled push
	// has Success false and no receipt.
	Success           bool
	MpesaReceipt      string
	Amount            string // decimal shillings, e.g. "6000.00"
	Msisdn            string
	PayerName         string
	AccountReference  string // the STK external reference, or a bank account number
	ChannelID         string
	CheckoutRequestID string
	ResultDescription string
	Raw               []byte
}

// SubscriptionsWebhookPayload is one callback on the billing channel: the
// outcome of a manager subscription payment.
type SubscriptionsWebhookPayload struct {
	Success           bool
	MpesaReceipt      string
	Amount            string
	Msisdn            string
	Reference         string // the subscription invoice reference we sent
	ChannelID         string
	CheckoutRequestID string
	ResultDescription string
	Raw               []byte
}

var (
	// ErrNotConfigured is returned when PayHero credentials are not set.
	ErrNotConfigured = errors.New("payhero: not configured")

	// ErrUnavailable is returned when PayHero cannot be reached.
	ErrUnavailable = errors.New("payhero: service unavailable")

	// ErrUnauthorized is returned by VerifyWebhook for a callback that fails
	// the configured IP or token/signature check.
	ErrUnauthorized = errors.New("payhero: webhook not authorized")

	// ErrVerificationNotConfigured is returned by VerifyWebhook when neither
	// an IP allowlist nor a secret is set: with nothing to check against,
	// every callback is refused rather than trusted.
	ErrVerificationNotConfigured = errors.New("payhero: webhook verification is not configured")

	// ErrInvalidPayload is returned for a callback that is not the expected
	// JSON.
	ErrInvalidPayload = errors.New("payhero: invalid webhook payload")
)

// APIError is a non-2xx answer from PayHero, keeping its body so the reason
// (bad channel, insufficient balance, invalid number ...) is not lost.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("payhero: HTTP %d: %s", e.StatusCode, e.Body)
}

// Retryable reports whether trying again might succeed: server errors and
// rate limiting, not a rejected request.
func (e *APIError) Retryable() bool {
	return e.StatusCode >= 500 || e.StatusCode == 429
}
