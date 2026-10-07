package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestInboxClaimIntegration runs InboxModel.Claim's SQL against a real
// willcoll_platform (system-design.txt A.6 / E.5): a receipt delivered by
// many concurrent duplicate webhooks is claimed once and leaves exactly one
// payment_inbox row; a different product presenting the same receipt is
// rejected as the wrong product; and once posted, further deliveries are
// acknowledged without reprocessing.
func TestInboxClaimIntegration(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_PLATFORM_DB_DSN", "WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		c, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Ping(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	appConn, adminConn := open(dsns[0]), open(dsns[1])
	models := NewModels(appConn, adminConn, 5*time.Second)
	ctx := context.Background()

	m := &Manager{FirmName: "Inbox Firm", Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@inbox.example.com", Phone: "+254700000000", Status: ManagerStatusPending}
	if err := m.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := models.Managers.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(`DELETE FROM payment_inbox WHERE channel_id LIKE 'INB-%'`)
		_, _ = appConn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID)
	})

	suffix := uuid.NewString()[:8]
	channel, receipt := "INB-CH-"+suffix, "INB"+suffix
	owner := uuid.New()
	if _, err := models.Channels.Register(ctx, m.ID, ProductRentals, channel, &owner, "https://example.test/cb"); err != nil {
		t.Fatal(err)
	}
	amount := moneyfmt.FromCents(250000)
	claim := func(product string) error {
		return models.Inbox.Claim(ctx, receipt, channel, product, amount, "254700000001", json.RawMessage(`{}`))
	}

	// Concurrent duplicate deliveries: every one proceeds (a retry of a
	// 'claimed' row is allowed to), none errors, and only one row exists.
	const deliveries = 20
	errs := make(chan error, deliveries)
	var wg sync.WaitGroup
	for i := 0; i < deliveries; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- claim(ProductRentals)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent duplicate Claim: %v", err)
		}
	}
	var rows int
	if err := adminConn.QueryRow(`SELECT count(*) FROM payment_inbox WHERE mpesa_receipt = $1`, receipt).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("payment_inbox rows for one receipt = %d, want exactly 1", rows)
	}

	// Another product presenting the same receipt is the wrong product.
	if err := claim(ProductEstates); !errors.Is(err, ErrWrongProduct) {
		t.Fatalf("other product's Claim err = %v, want ErrWrongProduct", err)
	}

	// Once posted: acknowledged, never reprocessed.
	if err := models.Inbox.MarkPosted(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if err := claim(ProductRentals); !errors.Is(err, ErrAlreadyPosted) {
		t.Fatalf("Claim after post err = %v, want ErrAlreadyPosted", err)
	}
	if err := models.Inbox.MarkPosted(ctx, receipt); err != nil {
		t.Fatalf("MarkPosted twice: %v", err)
	}

	// A channel with no claim in payment_channels (the FK fails) is reported as
	// ErrChannelUnclaimed, not a generic database error.
	err := models.Inbox.Claim(ctx, "INBX"+suffix, "INB-UNCLAIMED-"+suffix, ProductRentals, amount, "254700000001", json.RawMessage(`{}`))
	if !errors.Is(err, ErrChannelUnclaimed) {
		t.Fatalf("Claim on an unclaimed channel err = %v, want ErrChannelUnclaimed", err)
	}
}
