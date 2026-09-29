package payhero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	paymentsPath        = "/api/v2/payments"
	paymentChannelsPath = "/api/v2/payment_channels"
	maxBodyBytes        = 1 << 20
	maxErrorBytes       = 4 << 10
	provider            = "m-pesa"
)

// callTimeout bounds every outbound call. PayHero is an external dependency
// and must never be able to hold a request goroutine for longer than this,
// whatever the caller context (or the HTTP client) allows. Raised from 10s
// after a live STK push took longer than that and the tenant was wrongly
// told the request failed while PayHero had, in fact, accepted it.
const callTimeout = 30 * time.Second

// Client is a PayHero API client. It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	timeout time.Duration
}

// NewClient returns a client for baseURL. apiKey is sent verbatim as the
// Authorization header (for PayHero, "Basic <token>"). A nil httpClient gets
// a 35-second timeout — longer than callTimeout, so the per-call context
// deadline is always what actually fires, not this fallback. With an empty
// baseURL or apiKey every call returns ErrNotConfigured, so a deployment
// without PayHero fails cleanly.
func NewClient(baseURL, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 35 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, http: httpClient, timeout: callTimeout}
}

// Configured reports whether the client has the credentials to call PayHero.
func (c *Client) Configured() bool {
	return c != nil && c.baseURL != "" && c.apiKey != ""
}

// STKPush asks PayHero to prompt req.PhoneNumber for req.Amount. A nil error
// means the push was queued, not that the customer paid: the outcome arrives
// on the webhook. A non-2xx answer is an *APIError carrying PayHero body.
func (c *Client) STKPush(ctx context.Context, req STKPushRequest) (*STKPushResponse, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("payhero: amount must be a positive whole number of shillings, got %d", req.Amount)
	}
	if req.Provider == "" {
		req.Provider = provider
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// The deadline covers the whole exchange, including reading the body.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+paymentsPath, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}

	var out STKPushResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("payhero: unreadable response: %w", err)
	}
	return &out, nil
}

// RegisterChannel registers a bank, paybill or till as a settlement
// destination for STK collections (POST {baseURL}/api/v2/payment_channels).
// The returned Channel's ID is what a property's payhero_channel_id holds.
// PayHero does not de-duplicate on its side — callers wanting "reuse the
// existing channel" behaviour must check ListChannels first.
func (c *Client) RegisterChannel(ctx context.Context, req RegisterChannelRequest) (*Channel, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	switch req.ChannelType {
	case ChannelTypeBank, ChannelTypePaybill, ChannelTypeTill:
	default:
		return nil, fmt.Errorf("payhero: channel_type must be bank, paybill or till, got %q", req.ChannelType)
	}
	var out Channel
	if err := c.doJSON(ctx, http.MethodPost, paymentChannelsPath, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChannels lists every payment channel on the account (GET
// {baseURL}/api/v2/payment_channels), used to check a saved channel is
// still active and to find a channel to reuse rather than duplicate.
func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}
	var out channelList
	if err := c.doJSON(ctx, http.MethodGet, paymentChannelsPath, nil, &out); err != nil {
		return nil, err
	}
	return out.channels(), nil
}

// doJSON sends body (marshalled, or no body when nil) and decodes a 2xx
// response into out. Shared by every JSON-in/JSON-out PayHero call.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", c.apiKey)
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(out); err != nil {
		return fmt.Errorf("payhero: unreadable response: %w", err)
	}
	return nil
}
