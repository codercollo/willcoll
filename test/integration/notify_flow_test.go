package integration

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll-sys/internal/branding"
	"github.com/codercollo/willcoll-sys/internal/notify"
	"github.com/google/uuid"
)

type smsSend struct {
	msisdn string
	body   string
}

type fakeSMSGateway struct {
	mu    sync.Mutex
	sends []smsSend
}

func (f *fakeSMSGateway) Send(_ context.Context, msisdn, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, smsSend{msisdn: msisdn, body: body})
	return nil
}

func (f *fakeSMSGateway) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sends))
	for _, s := range f.sends {
		out = append(out, s.body)
	}
	return out
}

func seedBrandOrg(t *testing.T, brandName, slug string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		id, brandName, brandName, slug+"-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	return id
}

func TestBrandedSMSTemplatesDifferOnlyByBrand(t *testing.T) {
	ctx := context.Background()
	orgA := seedBrandOrg(t, "Alpha Brand", "alpha")
	orgB := seedBrandOrg(t, "Beta Brand", "beta")

	fake := &fakeSMSGateway{}
	svc := notify.NewService(fake, branding.NewService(testPool), testPool)
	svc.Start()
	defer svc.Stop()

	if err := svc.SendTemplate(ctx, orgA, "254700000001", "tenant_welcome.tmpl", map[string]any{"RentDueDay": 5}); err != nil {
		t.Fatalf("send org A: %v", err)
	}
	if err := svc.SendTemplate(ctx, orgB, "254700000002", "tenant_welcome.tmpl", map[string]any{"RentDueDay": 5}); err != nil {
		t.Fatalf("send org B: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(fake.bodies()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	bodies := fake.bodies()
	if len(bodies) != 2 {
		t.Fatalf("expected 2 SMS sends, got %d", len(bodies))
	}

	var bodyA, bodyB string
	for _, body := range bodies {
		switch {
		case strings.Contains(body, "Alpha Brand"):
			bodyA = body
		case strings.Contains(body, "Beta Brand"):
			bodyB = body
		}
	}
	if bodyA == "" || bodyB == "" {
		t.Fatalf("missing branded bodies: %v", bodies)
	}
	if strings.Contains(bodyA, "Beta Brand") || strings.Contains(bodyB, "Alpha Brand") {
		t.Fatalf("brand leaked across Organizations: %v", bodies)
	}
}
