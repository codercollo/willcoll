// Package sms sends the short messages Willcoll needs through Africa's
// Talking: the one-time code that gates the public pay page, and the payment
// confirmation a tenant gets afterwards (system-design.txt sections 1.4, 4.7).
package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	productionURL = "https://api.africastalking.com"
	sandboxURL    = "https://api.sandbox.africastalking.com"
	messagingPath = "/version1/messaging"
	maxBodyBytes  = 1 << 20

	// callTimeout bounds every outbound call, whatever the caller context
	// allows: an SMS provider must never hold a request goroutine.
	callTimeout = 10 * time.Second
)

var (
	// ErrNotConfigured is returned when the Africa's Talking credentials are
	// not set.
	ErrNotConfigured = errors.New("sms: not configured")

	// ErrUnavailable is returned when Africa's Talking cannot be reached.
	ErrUnavailable = errors.New("sms: service unavailable")
)

// APIError is a failed send. It keeps Africa's Talking response body, so a bad
// number or an empty account is visible rather than swallowed.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("sms: Africa's Talking answered %d: %s", e.StatusCode, e.Body)
}

// Client sends SMS through Africa's Talking. It is safe for concurrent use.
type Client struct {
	username string
	apiKey   string
	baseURL  string
	http     *http.Client
	timeout  time.Duration
}

// NewClient returns a client for the given Africa's Talking account. The
// "sandbox" username talks to the sandbox API. A non-empty baseURL overrides
// the endpoint (tests use it). A nil httpClient gets a 15-second timeout.
func NewClient(username, apiKey, baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if baseURL == "" {
		baseURL = productionURL
		if username == "sandbox" {
			baseURL = sandboxURL
		}
	}
	return &Client{username: username, apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), http: httpClient, timeout: callTimeout}
}

// Configured reports whether the client has credentials.
func (c *Client) Configured() bool {
	return c != nil && c.username != "" && c.apiKey != ""
}

// SendOTP texts a one-time code that is valid for ttl.
func (c *Client) SendOTP(ctx context.Context, phone, code string, ttl time.Duration) error {
	return c.send(ctx, phone, OTPMessage(code, ttl))
}

// PaymentConfirmation is what a tenant is told after paying. Amount is already
// formatted (through pkg/moneyfmt) so SMS and screen agree.
type PaymentConfirmation struct {
	Property string
	UnitCode string
	Amount   string
	Receipt  string
}

// SendPaymentConfirmation texts a tenant that their payment arrived.
func (c *Client) SendPaymentConfirmation(ctx context.Context, phone string, p PaymentConfirmation) error {
	return c.send(ctx, phone, ConfirmationMessage(p))
}

// OTPMessage is the text of a one-time-code SMS.
func OTPMessage(code string, ttl time.Duration) string {
	minutes := int(ttl.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	return fmt.Sprintf("Your Willcoll payment code is %s. It expires in %d min. Do not share it with anyone.", code, minutes)
}

// ConfirmationMessage is the text of a payment-confirmation SMS.
func ConfirmationMessage(p PaymentConfirmation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Payment of KES %s received", p.Amount)
	if p.Property != "" || p.UnitCode != "" {
		b.WriteString(" for ")
		b.WriteString(strings.TrimSpace(p.Property + " " + p.UnitCode))
	}
	if p.Receipt != "" {
		fmt.Fprintf(&b, ". Ref %s", p.Receipt)
	}
	b.WriteString(". Thank you.")
	return b.String()
}

// sendResponse is the part of Africa's Talking reply we read.
type sendResponse struct {
	SMSMessageData struct {
		Message    string `json:"Message"`
		Recipients []struct {
			StatusCode int    `json:"statusCode"`
			Number     string `json:"number"`
			Status     string `json:"status"`
		} `json:"Recipients"`
	} `json:"SMSMessageData"`
}

func (c *Client) send(ctx context.Context, phone, message string) error {
	if !c.Configured() {
		return ErrNotConfigured
	}
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return errors.New("sms: no recipient")
	}
	if !strings.HasPrefix(phone, "+") {
		phone = "+" + phone
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	form := url.Values{"username": {c.username}, "to": {phone}, "message": {message}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagingPath, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("apiKey", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	// A 2xx can still carry a per-recipient failure (invalid number, blacklist).
	var out sendResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("sms: unreadable response: %w: %s", err, strings.TrimSpace(string(body)))
	}
	if len(out.SMSMessageData.Recipients) == 0 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	for _, r := range out.SMSMessageData.Recipients {
		// 100 processed, 101 sent, 102 queued.
		if r.StatusCode < 100 || r.StatusCode > 102 {
			return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		}
	}
	return nil
}
