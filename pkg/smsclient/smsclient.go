package smsclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is a thin, provider-agnostic SMS gateway client. It posts a JSON
// {to, message, sender_id} body to gatewayURL with apiKey/apiSecret as
// headers — the shape most REST SMS aggregators (Africa's Talking and
// similar) accept. The vendor is still TBD (spec §11.3, open question 3); this
// stays intentionally generic so swapping vendors is a config change, not a
// rewrite.
type Client struct {
	gatewayURL string
	apiKey     string
	apiSecret  string
	senderID   string
	http       *http.Client
}

// NewClient constructs an SMS gateway client posting to gatewayURL.
func NewClient(gatewayURL, apiKey, apiSecret, senderID string) *Client {
	return &Client{
		gatewayURL: gatewayURL,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		senderID:   senderID,
		http:       &http.Client{},
	}
}

type sendRequest struct {
	To       string `json:"to"`
	Message  string `json:"message"`
	SenderID string `json:"sender_id,omitempty"`
}

// Send posts body to msisdn via the configured gateway. It satisfies
// notify.SMSGateway.
func (c *Client) Send(ctx context.Context, msisdn, body string) error {
	payload, err := json.Marshal(sendRequest{To: msisdn, Message: body, SenderID: c.senderID})
	if err != nil {
		return fmt.Errorf("marshal sms request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gatewayURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build sms request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apiKey", c.apiKey)
	if c.apiSecret != "" {
		req.Header.Set("apiSecret", c.apiSecret)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send sms request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read sms response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sms gateway: status %d: %s", resp.StatusCode, raw)
	}

	return nil
}
