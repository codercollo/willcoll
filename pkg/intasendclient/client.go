package intasendclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Client is a thin, generic HTTP client for IntaSend's checkout API. It knows
// nothing about Willcoll business rules.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient constructs an IntaSend checkout client. baseURL should include the
// API prefix, e.g. https://sandbox.intasend.com/api/v1.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{},
	}
}

// CheckoutRequest is the JSON body accepted by IntaSend's checkout endpoint.
type CheckoutRequest struct {
	Currency    string `json:"currency"`
	Amount      string `json:"amount"`
	Email       string `json:"email"`
	PhoneNumber string `json:"phone_number"`
	Method      string `json:"method,omitempty"`
	ApiRef      string `json:"api_ref"`
	Name        string `json:"name"`
	RedirectURL string `json:"redirect_url,omitempty"`
}

// CheckoutResponse is the parsed checkout response. Raw preserves the exact
// payload for callers that persist it for audit purposes.
type CheckoutResponse struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	Raw []byte `json:"-"`
}

// CreateCheckout posts a checkout request and returns the parsed response.
func (c *Client) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("marshal checkout request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/checkout/", bytes.NewReader(body))
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("build checkout request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("send checkout request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return CheckoutResponse{}, fmt.Errorf("read checkout response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckoutResponse{}, fmt.Errorf("intasend checkout: status %d: %s", resp.StatusCode, raw)
	}

	var out CheckoutResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return CheckoutResponse{}, fmt.Errorf("decode checkout response: %w", err)
	}
	out.Raw = raw
	return out, nil
}
