package sms

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const okBody = `{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"statusCode":101,"number":"+254722000001","status":"Success"}]}}`

func TestSendOTP(t *testing.T) {
	var form url.Values
	var gotKey, gotPath, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath, gotType = r.Header.Get("apiKey"), r.URL.Path, r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c := NewClient("willcoll", "at-key", srv.URL, nil)
	if err := c.SendOTP(context.Background(), "254722000001", "482913", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if gotKey != "at-key" || gotPath != "/version1/messaging" || gotType != "application/x-www-form-urlencoded" {
		t.Errorf("request: key %q path %q type %q", gotKey, gotPath, gotType)
	}
	if form.Get("username") != "willcoll" || form.Get("to") != "+254722000001" {
		t.Errorf("form = %v (the number must be sent in international + form)", form)
	}
	if msg := form.Get("message"); !strings.Contains(msg, "482913") || !strings.Contains(msg, "5 min") {
		t.Errorf("message = %q", msg)
	}
}

func TestSendPaymentConfirmation(t *testing.T) {
	var msg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f, _ := url.ParseQuery(string(b))
		msg = f.Get("message")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, okBody)
	}))
	defer srv.Close()

	err := NewClient("willcoll", "k", srv.URL, nil).SendPaymentConfirmation(context.Background(), "+254722000001",
		PaymentConfirmation{Property: "Runda Arcade", UnitCode: "A4", Amount: "6,000.00", Receipt: "NLJ7RT61SV"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"KES 6,000.00", "Runda Arcade A4", "NLJ7RT61SV"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
}

func TestMessageTemplates(t *testing.T) {
	if got := OTPMessage("123456", 30*time.Second); !strings.Contains(got, "1 min") {
		t.Errorf("a short ttl must round up to one minute: %q", got)
	}
	if got := ConfirmationMessage(PaymentConfirmation{Amount: "10.00"}); got != "Payment of KES 10.00 received. Thank you." {
		t.Errorf("minimal confirmation = %q", got)
	}
}

func TestSendGivesUpOnAHungServer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	c := NewClient("u", "k", srv.URL, &http.Client{})
	c.timeout = 150 * time.Millisecond
	start := time.Now()
	err := c.SendOTP(context.Background(), "+254700000000", "1", time.Minute)
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 3*time.Second {
		t.Errorf("err = %v after %s; want ErrUnavailable promptly", err, time.Since(start))
	}
}

func TestSendErrors(t *testing.T) {
	t.Run("http error keeps the body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, "The supplied authentication is invalid")
		}))
		defer srv.Close()
		err := NewClient("u", "bad", srv.URL, nil).SendOTP(context.Background(), "+254700000000", "1", time.Minute)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 || !strings.Contains(err.Error(), "authentication is invalid") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a 2xx with a failed recipient is still an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"SMSMessageData":{"Message":"Sent to 0/1","Recipients":[{"statusCode":403,"number":"+2547","status":"InvalidPhoneNumber"}]}}`)
		}))
		defer srv.Close()
		err := NewClient("u", "k", srv.URL, nil).SendOTP(context.Background(), "+2547", "1", time.Minute)
		if err == nil || !strings.Contains(err.Error(), "InvalidPhoneNumber") {
			t.Errorf("err = %v, want the recipient failure with its body", err)
		}
	})
	t.Run("no recipients at all is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"SMSMessageData":{"Message":"Insufficient balance","Recipients":[]}}`)
		}))
		defer srv.Close()
		err := NewClient("u", "k", srv.URL, nil).SendOTP(context.Background(), "+254700000000", "1", time.Minute)
		if err == nil || !strings.Contains(err.Error(), "Insufficient balance") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		u := srv.URL
		srv.Close()
		err := NewClient("u", "k", u, &http.Client{Timeout: 300 * time.Millisecond}).SendOTP(context.Background(), "+254700000000", "1", time.Minute)
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("err = %v, want ErrUnavailable", err)
		}
	})
	t.Run("unconfigured and blank recipient", func(t *testing.T) {
		if err := NewClient("", "", "", nil).SendOTP(context.Background(), "+254700000000", "1", time.Minute); !errors.Is(err, ErrNotConfigured) {
			t.Errorf("err = %v", err)
		}
		if err := NewClient("u", "k", "http://x", nil).SendOTP(context.Background(), "  ", "1", time.Minute); err == nil {
			t.Error("a blank recipient must be refused")
		}
	})
	t.Run("sandbox username selects the sandbox endpoint", func(t *testing.T) {
		if got := NewClient("sandbox", "k", "", nil).baseURL; got != sandboxURL {
			t.Errorf("baseURL = %s", got)
		}
	})
}
