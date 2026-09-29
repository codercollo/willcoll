package payhero

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Headers and query parameter VerifyWebhook reads a secret from.
const (
	TokenHeader     = "X-Webhook-Token"
	TokenQueryParam = "token"
	SignatureHeader = "X-Payhero-Signature"
)

// VerifyConfig says how to authenticate a PayHero callback. Whatever is set is
// enforced, and all of it must pass:
//
//   - AllowedIPs: the connecting client IP must be in the list (addresses or
//     CIDRs). Use with the reverse proxy trusted-proxies setting so the IP is
//     the real one.
//   - Secret: the request must carry it as the X-Webhook-Token header or a
//     ?token= query parameter (PayHero lets us choose the callback URL, so an
//     unguessable token in it is the practical control), or a valid
//     hex HMAC-SHA256 of the body in X-Payhero-Signature.
//
// With neither set, VerifyWebhook refuses everything.
type VerifyConfig struct {
	AllowedIPs []string
	Secret     string
}

// Enabled reports whether any check is configured.
func (c VerifyConfig) Enabled() bool { return len(c.AllowedIPs) > 0 || c.Secret != "" }

// VerifyWebhook authenticates a callback. clientIP is the caller address as
// resolved by the server (after trusted-proxy handling) and body the raw
// request body. It returns ErrUnauthorized, or ErrVerificationNotConfigured
// when nothing is configured to check against.
func VerifyWebhook(r *http.Request, body []byte, clientIP string, cfg VerifyConfig) error {
	if !cfg.Enabled() {
		return ErrVerificationNotConfigured
	}

	if len(cfg.AllowedIPs) > 0 && !ipAllowed(clientIP, cfg.AllowedIPs) {
		return fmt.Errorf("%w: address not allowed", ErrUnauthorized)
	}

	if cfg.Secret != "" && !secretMatches(r, body, cfg.Secret) {
		return fmt.Errorf("%w: bad token or signature", ErrUnauthorized)
	}
	return nil
}

func ipAllowed(clientIP string, allowed []string) bool {
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if ip == nil {
		return false
	}
	for _, a := range allowed {
		a = strings.TrimSpace(a)
		if _, network, err := net.ParseCIDR(a); err == nil {
			if network.Contains(ip) {
				return true
			}
			continue
		}
		if other := net.ParseIP(a); other != nil && other.Equal(ip) {
			return true
		}
	}
	return false
}

func secretMatches(r *http.Request, body []byte, secret string) bool {
	equal := func(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

	if tok := r.Header.Get(TokenHeader); tok != "" && equal(tok, secret) {
		return true
	}
	if tok := r.URL.Query().Get(TokenQueryParam); tok != "" && equal(tok, secret) {
		return true
	}
	if sig := r.Header.Get(SignatureHeader); sig != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		return equal(strings.ToLower(strings.TrimSpace(sig)), want)
	}
	return false
}

// ParseCollectionsPayload reads a collections callback.
func ParseCollectionsPayload(body []byte) (*CollectionsWebhookPayload, error) {
	f, err := flatten(body)
	if err != nil {
		return nil, err
	}
	p := &CollectionsWebhookPayload{
		Success:           f.success(),
		MpesaReceipt:      f.str("MpesaReceiptNumber", "mpesa_receipt", "MpesaReceipt", "TransID"),
		Amount:            f.amount("Amount", "amount", "TransAmount"),
		Msisdn:            f.str("Phone", "msisdn", "MSISDN", "phone_number", "PhoneNumber"),
		PayerName:         f.str("payer_name", "CustomerName", "customer_name", "Name"),
		AccountReference:  f.str("ExternalReference", "external_reference", "account_reference", "BillRefNumber"),
		ChannelID:         f.str("channel_id", "ChannelID", "channel"),
		CheckoutRequestID: f.str("CheckoutRequestID", "checkout_request_id"),
		ResultDescription: f.str("ResultDesc", "result_desc", "message"),
		Raw:               body,
	}
	if p.Success && (p.MpesaReceipt == "" || p.Amount == "") {
		return nil, fmt.Errorf("%w: a successful payment needs a receipt and an amount", ErrInvalidPayload)
	}
	if !p.Success && p.AccountReference == "" && p.CheckoutRequestID == "" {
		return nil, fmt.Errorf("%w: a failed payment needs a reference", ErrInvalidPayload)
	}
	return p, nil
}

// ParseSubscriptionsPayload reads a billing-channel callback.
func ParseSubscriptionsPayload(body []byte) (*SubscriptionsWebhookPayload, error) {
	f, err := flatten(body)
	if err != nil {
		return nil, err
	}
	p := &SubscriptionsWebhookPayload{
		Success:           f.success(),
		MpesaReceipt:      f.str("MpesaReceiptNumber", "mpesa_receipt", "MpesaReceipt", "TransID"),
		Amount:            f.amount("Amount", "amount", "TransAmount"),
		Msisdn:            f.str("Phone", "msisdn", "MSISDN", "phone_number", "PhoneNumber"),
		Reference:         f.str("ExternalReference", "external_reference", "account_reference", "BillRefNumber"),
		ChannelID:         f.str("channel_id", "ChannelID", "channel"),
		CheckoutRequestID: f.str("CheckoutRequestID", "checkout_request_id"),
		ResultDescription: f.str("ResultDesc", "result_desc", "message"),
		Raw:               body,
	}
	if p.Reference == "" {
		return nil, fmt.Errorf("%w: no invoice reference", ErrInvalidPayload)
	}
	if p.Success && (p.MpesaReceipt == "" || p.Amount == "") {
		return nil, fmt.Errorf("%w: a successful payment needs a receipt and an amount", ErrInvalidPayload)
	}
	return p, nil
}

// fields is a callback with PayHero nested "response" object merged into the
// top level, so both the nested and the flat wire formats read the same.
type fields map[string]any

func flatten(body []byte) (fields, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil || top == nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrInvalidPayload)
	}
	f := fields{}
	maps.Copy(f, top)
	if inner, ok := top["response"].(map[string]any); ok {
		maps.Copy(f, inner)
	}
	return f, nil
}

func (f fields) raw(keys ...string) (any, bool) {
	for _, k := range keys {
		if v, ok := f[k]; ok && v != nil {
			return v, true
		}
	}
	return nil, false
}

func (f fields) str(keys ...string) string {
	v, ok := f.raw(keys...)
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	}
	return ""
}

// amount returns a decimal string with two places, from a number or string.
func (f fields) amount(keys ...string) string {
	v, ok := f.raw(keys...)
	if !ok {
		return ""
	}
	var s string
	switch t := v.(type) {
	case json.Number:
		s = t.String()
	case string:
		s = strings.TrimSpace(t)
	default:
		return ""
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return ""
	}
	return strconv.FormatFloat(n, 'f', 2, 64)
}

// success is true when money moved: an explicit ResultCode of 0, or a status
// of "Success", or, with neither present, a receipt number.
func (f fields) success() bool {
	if v, ok := f.raw("ResultCode", "result_code"); ok {
		switch t := v.(type) {
		case json.Number:
			return t.String() == "0"
		case string:
			return strings.TrimSpace(t) == "0"
		}
	}
	if s := strings.ToLower(f.str("Status", "status")); s != "" {
		return s == "success" || s == "completed" || s == "paid"
	}
	return f.str("MpesaReceiptNumber", "mpesa_receipt", "MpesaReceipt", "TransID") != ""
}
