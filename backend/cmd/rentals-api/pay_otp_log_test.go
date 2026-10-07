package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/sms"
	"go.uber.org/mock/gomock"
)

// smsFake is a stand-in for Africa's Talking that records what the real
// sms.Client posts to it.
type smsFake struct {
	mu   sync.Mutex
	sent []string
	code int
}

func (f *smsFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	f.sent = append(f.sent, r.Form.Get("to")+"|"+r.Form.Get("message"))
	code := f.code
	f.mu.Unlock()
	if code != 0 {
		http.Error(w, "boom", code)
		return
	}
	_, _ = fmt.Fprintf(w, `{"SMSMessageData":{"Recipients":[{"statusCode":101,"number":%q}]}}`, r.Form.Get("to"))
}

func TestRequestPayOTPThroughRealSMSClient(t *testing.T) {
	const phone = "+254722000001"

	setup := func(t *testing.T, env string) (*payTestApp, *smsFake, *bytes.Buffer) {
		fake := &smsFake{}
		srv := httptest.NewServer(fake)
		t.Cleanup(srv.Close)
		p := newPayTestApp(t)
		logs := &bytes.Buffer{}
		p.app.logger = slog.New(slog.NewTextHandler(logs, nil))
		p.app.sms = sms.NewClient("dev", "dev", srv.URL, nil)
		p.app.config.env = env
		p.expectTarget()
		return p, fake, logs
	}
	post := func(p *payTestApp, body string) int {
		w := do(p.app.requestPayOTPHandler, "POST", "/", body, payParams("runda", "A1"), nil)
		p.app.wg.Wait()
		return w.Code
	}
	allowIssue := func(p *payTestApp) {
		p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{"0722 000 001"}, nil)
		p.q.EXPECT().CountRecentPayOTPs(gomock.Any(), gomock.Any()).Return(int32(0), nil)
		p.q.EXPECT().CreatePayOTP(gomock.Any(), gomock.Any()).Return(nil)
		p.q.EXPECT().PurgePayOTPs(gomock.Any(), gomock.Any()).Return(nil)
	}

	t.Run("matching phone sends exactly one SMS and logs the code in development", func(t *testing.T) {
		p, fake, logs := setup(t, "development")
		allowIssue(p)
		if code := post(p, `{"phone":"0722000001"}`); code != http.StatusAccepted {
			t.Fatalf("status %d", code)
		}
		if len(fake.sent) != 1 || !strings.HasPrefix(fake.sent[0], phone+"|") {
			t.Fatalf("fake received %v", fake.sent)
		}
		if !strings.Contains(logs.String(), "OTP for A1 "+phone+": sent (code ") {
			t.Errorf("log = %s", logs)
		}
	})

	t.Run("non-matching phone sends nothing, still 202, and says why in development", func(t *testing.T) {
		p, fake, logs := setup(t, "development")
		p.q.EXPECT().ListUnitPayPhones(gomock.Any(), gomock.Any()).Return([]string{phone}, nil)
		if code := post(p, `{"phone":"0799999999"}`); code != http.StatusAccepted {
			t.Fatalf("status %d", code)
		}
		if len(fake.sent) != 0 {
			t.Fatalf("fake received %v", fake.sent)
		}
		if !strings.Contains(logs.String(), "OTP for A1: phone not on active lease, not sent") {
			t.Errorf("log = %s", logs)
		}
	})

	t.Run("outside development nothing about codes is logged", func(t *testing.T) {
		p, fake, logs := setup(t, "production")
		allowIssue(p)
		post(p, `{"phone":"0722000001"}`)
		if len(fake.sent) != 1 || strings.Contains(logs.String(), "OTP for") || strings.Contains(logs.String(), "code") {
			t.Errorf("sent %v, log = %s", fake.sent, logs)
		}
	})

	t.Run("a send failure is an ERROR in every environment, without the code", func(t *testing.T) {
		for _, env := range []string{"development", "production"} {
			p, fake, logs := setup(t, env)
			fake.code = http.StatusInternalServerError
			allowIssue(p)
			if code := post(p, `{"phone":"0722000001"}`); code != http.StatusAccepted {
				t.Fatalf("%s: status %d", env, code)
			}
			if !strings.Contains(logs.String(), "level=ERROR msg=\"pay OTP SMS send failed\"") {
				t.Errorf("%s: log = %s", env, logs)
			}
			if env == "production" && strings.Contains(logs.String(), "code ") {
				t.Errorf("code leaked in production: %s", logs)
			}
		}
	})
}
