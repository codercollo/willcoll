package payhero

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSTKPush(t *testing.T) {
	t.Run("posts to /api/v2/payments with the raw Authorization header", func(t *testing.T) {
		var gotAuth, gotPath, gotMethod, gotType string
		var got STKPushRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth, gotPath, gotMethod, gotType = r.Header.Get("Authorization"), r.URL.Path, r.Method, r.Header.Get("Content-Type")
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"success":true,"status":"QUEUED","reference":"ref-1","CheckoutRequestID":"ws_CO_1"}`)
		}))
		defer srv.Close()

		c := NewClient(srv.URL+"/", "Basic dXNlcjpwYXNz", nil)
		resp, err := c.STKPush(context.Background(), STKPushRequest{
			Amount: 6000, PhoneNumber: "254722000001", ChannelID: "CH-9", ExternalReference: "WC-1",
			CustomerName: "JOHN", CallbackURL: "https://api.example.com/v1/webhooks/payhero/collections?token=x",
		})
		if err != nil {
			t.Fatal(err)
		}
		if gotAuth != "Basic dXNlcjpwYXNz" {
			t.Errorf("Authorization = %q, want the key passed through verbatim", gotAuth)
		}
		if gotPath != "/api/v2/payments" || gotMethod != http.MethodPost || gotType != "application/json" {
			t.Errorf("request = %s %s (%s)", gotMethod, gotPath, gotType)
		}
		if got.Amount != 6000 || got.PhoneNumber != "254722000001" || got.ChannelID != "CH-9" ||
			got.ExternalReference != "WC-1" || got.Provider != "m-pesa" {
			t.Errorf("body = %+v", got)
		}
		if !resp.Success || resp.CheckoutRequestID != "ws_CO_1" || resp.Reference != "ref-1" {
			t.Errorf("response = %+v", resp)
		}
	})

	t.Run("non-2xx keeps PayHero body in the error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"error":"invalid channel_id"}`)
		}))
		defer srv.Close()

		_, err := NewClient(srv.URL, "k", nil).STKPush(context.Background(), STKPushRequest{Amount: 100, PhoneNumber: "254700000000", ChannelID: "bad"})
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("err = %v, want *APIError", err)
		}
		if apiErr.StatusCode != 422 || !strings.Contains(apiErr.Body, "invalid channel_id") || apiErr.Retryable() {
			t.Errorf("APIError = %+v retryable=%v", apiErr, apiErr.Retryable())
		}
		if !strings.Contains(err.Error(), "invalid channel_id") {
			t.Errorf("error text %q must carry the body", err)
		}
	})

	t.Run("server errors are retryable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
		defer srv.Close()
		_, err := NewClient(srv.URL, "k", nil).STKPush(context.Background(), STKPushRequest{Amount: 1})
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() {
			t.Errorf("err = %v, want a retryable APIError", err)
		}
	})

	t.Run("unreachable is ErrUnavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		_, err := NewClient(url, "k", &http.Client{Timeout: 300 * time.Millisecond}).STKPush(context.Background(), STKPushRequest{Amount: 1})
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("err = %v, want ErrUnavailable", err)
		}
	})

	t.Run("unconfigured and invalid amounts never reach the network", func(t *testing.T) {
		if _, err := NewClient("", "", nil).STKPush(context.Background(), STKPushRequest{Amount: 1}); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("err = %v, want ErrNotConfigured", err)
		}
		if NewClient("http://x", "", nil).Configured() {
			t.Error("a client without a key is not configured")
		}
		if _, err := NewClient("http://x", "k", nil).STKPush(context.Background(), STKPushRequest{Amount: 0}); err == nil {
			t.Error("a zero amount must be refused")
		}
	})
}

func TestSTKPushGivesUpOnAHungServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	// A plain http.Client with no timeout of its own: only the client call
	// deadline can end this request.
	c := NewClient(srv.URL, "k", &http.Client{})
	c.timeout = 150 * time.Millisecond

	start := time.Now()
	_, err := c.STKPush(context.Background(), STKPushRequest{Amount: 1})
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want ErrUnavailable wrapping context.DeadlineExceeded", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("returned after %s; a hung PayHero must not hold the caller", took)
	}
}

func TestCallTimeoutIsThirtySeconds(t *testing.T) {
	if callTimeout != 30*time.Second || NewClient("http://x", "k", nil).timeout != callTimeout {
		t.Errorf("call timeout = %s, want 30s", callTimeout)
	}
}

func req(t *testing.T, target, body string, headers map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestVerifyWebhook(t *testing.T) {
	const body = `{"a":1}`
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(body))
	goodSig := hex.EncodeToString(mac.Sum(nil))

	tests := []struct {
		name    string
		r       *http.Request
		ip      string
		cfg     VerifyConfig
		wantErr error
	}{
		{"token header", req(t, "/w", body, map[string]string{TokenHeader: "s3cret"}), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, nil},
		{"token query parameter", req(t, "/w?token=s3cret", body, nil), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, nil},
		{"hmac signature of the body", req(t, "/w", body, map[string]string{SignatureHeader: goodSig}), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, nil},
		{"uppercase signature", req(t, "/w", body, map[string]string{SignatureHeader: strings.ToUpper(goodSig)}), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, nil},
		{"wrong token", req(t, "/w?token=nope", body, nil), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, ErrUnauthorized},
		{"no credentials", req(t, "/w", body, nil), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, ErrUnauthorized},
		{"tampered signature", req(t, "/w", body, map[string]string{SignatureHeader: goodSig[:len(goodSig)-1] + "0"}), "1.2.3.4", VerifyConfig{Secret: "s3cret"}, ErrUnauthorized},
		{"ip in allowlist", req(t, "/w", body, nil), "41.90.0.7", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}}, nil},
		{"exact ip", req(t, "/w", body, nil), "8.8.8.8", VerifyConfig{AllowedIPs: []string{"8.8.8.8"}}, nil},
		{"ip outside allowlist", req(t, "/w", body, nil), "6.6.6.6", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}}, ErrUnauthorized},
		{"garbage client ip", req(t, "/w", body, nil), "not-an-ip", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}}, ErrUnauthorized},
		{"both configured: right ip, wrong token", req(t, "/w?token=bad", body, nil), "41.90.0.7", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}, Secret: "s3cret"}, ErrUnauthorized},
		{"both configured: right token, wrong ip", req(t, "/w?token=s3cret", body, nil), "6.6.6.6", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}, Secret: "s3cret"}, ErrUnauthorized},
		{"both configured and both right", req(t, "/w?token=s3cret", body, nil), "41.90.0.7", VerifyConfig{AllowedIPs: []string{"41.90.0.0/24"}, Secret: "s3cret"}, nil},
		{"nothing configured refuses everything", req(t, "/w", body, nil), "1.2.3.4", VerifyConfig{}, ErrVerificationNotConfigured},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWebhook(tc.r, []byte(body), tc.ip, tc.cfg)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestParseCollectionsPayload(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    CollectionsWebhookPayload
		wantErr bool
	}{
		{"nested success (STK)", `{"forward_url":"","response":{"Amount":6000,"CheckoutRequestID":"ws_CO_1","ExternalReference":"WC-1","MpesaReceiptNumber":"NLJ7RT61SV","Phone":"+254722000001","ResultCode":0,"ResultDesc":"ok","Status":"Success"}}`,
			CollectionsWebhookPayload{Success: true, MpesaReceipt: "NLJ7RT61SV", Amount: "6000.00", Msisdn: "+254722000001", AccountReference: "WC-1", CheckoutRequestID: "ws_CO_1", ResultDescription: "ok"}, false},
		{"flat success (organic paybill)", `{"mpesa_receipt":"QWE123","amount":"2500.5","msisdn":"254733000001","account_reference":"BANK-1","payer_name":"MARY WANJIKU","channel_id":"CH-9"}`,
			CollectionsWebhookPayload{Success: true, MpesaReceipt: "QWE123", Amount: "2500.50", Msisdn: "254733000001", AccountReference: "BANK-1", PayerName: "MARY WANJIKU", ChannelID: "CH-9"}, false},
		{"cancelled push", `{"response":{"ExternalReference":"WC-2","ResultCode":1032,"ResultDesc":"Request cancelled by user","Status":"Failed"}}`,
			CollectionsWebhookPayload{Success: false, AccountReference: "WC-2", ResultDescription: "Request cancelled by user"}, false},
		{"success without a receipt", `{"response":{"Amount":100,"ResultCode":0,"Status":"Success"}}`, CollectionsWebhookPayload{}, true},
		{"failure without any reference", `{"response":{"ResultCode":1,"Status":"Failed"}}`, CollectionsWebhookPayload{}, true},
		{"not json", `hello`, CollectionsWebhookPayload{}, true},
		{"json but not an object", `[1,2]`, CollectionsWebhookPayload{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCollectionsPayload([]byte(tc.body))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidPayload) {
					t.Errorf("err = %v, want ErrInvalidPayload", err)
				}
				return
			}
			got.Raw = nil
			if !reflect.DeepEqual(*got, tc.want) {
				t.Errorf("got %+v\nwant %+v", *got, tc.want)
			}
		})
	}
}

func TestParseSubscriptionsPayload(t *testing.T) {
	ok, err := ParseSubscriptionsPayload([]byte(`{"response":{"Amount":"2500","ExternalReference":"INV-7","MpesaReceipt":"SUB123","Phone":"254700000000","ResultCode":"0"}}`))
	if err != nil || !ok.Success || ok.Reference != "INV-7" || ok.Amount != "2500.00" || ok.MpesaReceipt != "SUB123" {
		t.Fatalf("success = %+v, %v", ok, err)
	}
	failed, err := ParseSubscriptionsPayload([]byte(`{"response":{"ExternalReference":"INV-8","ResultCode":1,"ResultDesc":"insufficient funds"}}`))
	if err != nil || failed.Success || failed.ResultDescription != "insufficient funds" {
		t.Fatalf("failure = %+v, %v", failed, err)
	}
	for _, bad := range []string{`{"response":{"Amount":1,"MpesaReceiptNumber":"X"}}`, `{}`, `nope`} {
		if _, err := ParseSubscriptionsPayload([]byte(bad)); !errors.Is(err, ErrInvalidPayload) {
			t.Errorf("%s: err = %v, want ErrInvalidPayload", bad, err)
		}
	}
}

func TestRegisterChannel(t *testing.T) {
	t.Run("posts to /api/v2/payment_channels and decodes the channel", func(t *testing.T) {
		var gotPath, gotMethod string
		var got RegisterChannelRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotMethod = r.URL.Path, r.Method
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = io.WriteString(w, `{"id":13236,"channel_type":"bank","transaction_type":"deposit","account_id":9,"short_code":522522,"account_number":"1322334437","description":"KCB test","is_active":true,"balance_plain":"0.00"}`)
		}))
		defer srv.Close()

		c := NewClient(srv.URL, "Basic k", nil)
		ch, err := c.RegisterChannel(context.Background(), RegisterChannelRequest{
			ChannelType: ChannelTypeBank, AccountID: 9, ShortCode: 522522, AccountNumber: "1322334437", Description: "KCB test",
		})
		if err != nil {
			t.Fatal(err)
		}
		if gotPath != "/api/v2/payment_channels" || gotMethod != http.MethodPost {
			t.Errorf("request = %s %s", gotMethod, gotPath)
		}
		if got.ChannelType != "bank" || got.ShortCode != 522522 || got.AccountNumber != "1322334437" {
			t.Errorf("body = %+v", got)
		}
		if ch.ID != 13236 || !ch.IsActive {
			t.Errorf("channel = %+v", ch)
		}
	})

	t.Run("rejects an unknown channel type before the network call", func(t *testing.T) {
		if _, err := NewClient("http://x", "k", nil).RegisterChannel(context.Background(), RegisterChannelRequest{ChannelType: "crypto"}); err == nil {
			t.Error("expected an error for an invalid channel_type")
		}
	})
}

func TestListChannels(t *testing.T) {
	t.Run("bare array", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/v2/payment_channels" {
				t.Errorf("request = %s %s", r.Method, r.URL.Path)
			}
			_, _ = io.WriteString(w, `[{"id":1,"is_active":true},{"id":2,"is_active":false}]`)
		}))
		defer srv.Close()

		chs, err := NewClient(srv.URL, "k", nil).ListChannels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(chs) != 2 || chs[0].ID != 1 || chs[1].IsActive {
			t.Errorf("channels = %+v", chs)
		}
	})

	t.Run("wrapped in data", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"data":[{"id":7,"is_active":true}]}`)
		}))
		defer srv.Close()

		chs, err := NewClient(srv.URL, "k", nil).ListChannels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(chs) != 1 || chs[0].ID != 7 {
			t.Errorf("channels = %+v", chs)
		}
	})

	// Confirmed live against a real PayHero account on 2026-09-28: the
	// actual response wraps the array under "payment_channels" (with a
	// "pagination" object alongside it), and short_code — despite being
	// documented as a number — comes back as a quoted string.
	t.Run("wrapped in payment_channels, with a string short_code", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"payment_channels":[{"id":13236,"channel_type":"bank","account_id":12450,"short_code":"522522","account_number":"1322238537","is_active":true,"balance_plain":null}],"pagination":{"count":1}}`)
		}))
		defer srv.Close()

		chs, err := NewClient(srv.URL, "k", nil).ListChannels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(chs) != 1 || chs[0].ID != 13236 || chs[0].ShortCode != 522522 || chs[0].AccountID != 12450 {
			t.Errorf("channels = %+v", chs)
		}
	})
}
