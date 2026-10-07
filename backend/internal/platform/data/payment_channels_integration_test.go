package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestChannelReleaseIntegration runs ChannelModel.Release's SQL against a
// real willcoll_platform (build-phases.txt 3i-d): a released channel can be
// claimed by someone else, a claim cannot be released by a non-owner, and a
// channel whose payment_inbox holds receipts keeps its claim (the foreign
// key is what refuses, and Release names that failure).
func TestChannelReleaseIntegration(t *testing.T) {
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

	m := &Manager{FirmName: "Channel Firm", Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@channels.example.com", Phone: "+254700000000", Status: ManagerStatusPending}
	if err := m.Password.Set("correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if err := models.Managers.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(`DELETE FROM payment_inbox WHERE channel_id LIKE 'REL-%'`)
		_, _ = appConn.Exec(`DELETE FROM managers WHERE id = $1`, m.ID)
	})

	suffix := uuid.NewString()[:8]
	free, used := "REL-FREE-"+suffix, "REL-USED-"+suffix
	ownerA, ownerB := uuid.New(), uuid.New()
	register := func(channel string, owner uuid.UUID) error {
		_, err := models.Channels.Register(ctx, m.ID, ProductRentals, channel, &owner, "https://example.test/cb")
		return err
	}

	if err := register(free, ownerA); err != nil {
		t.Fatal(err)
	}
	if err := register(free, ownerB); !errors.Is(err, ErrChannelAlreadyRegistered) {
		t.Fatalf("second owner err = %v, want ErrChannelAlreadyRegistered", err)
	}

	// A non-owner's release is a no-op, not a theft.
	if err := models.Channels.Release(ctx, m.ID, ProductRentals, free, ownerB); err != nil {
		t.Fatal(err)
	}
	if got, err := models.Channels.Get(ctx, m.ID, free); err != nil || got.OwnerID == nil || *got.OwnerID != ownerA {
		t.Fatalf("claim after a non-owner's release = %+v, %v; want still ownerA's", got, err)
	}

	// The real owner's release frees it for someone else; releasing again is
	// harmless.
	if err := models.Channels.Release(ctx, m.ID, ProductRentals, free, ownerA); err != nil {
		t.Fatal(err)
	}
	if err := models.Channels.Release(ctx, m.ID, ProductRentals, free, ownerA); err != nil {
		t.Fatalf("releasing twice: %v", err)
	}
	if _, err := models.Channels.Get(ctx, m.ID, free); !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("Get after release err = %v, want ErrRecordNotFound", err)
	}
	if err := register(free, ownerB); err != nil {
		t.Fatalf("claiming a released channel: %v", err)
	}

	// Receipts recorded against a channel pin its claim.
	if err := register(used, ownerA); err != nil {
		t.Fatal(err)
	}
	if err := models.Inbox.Claim(ctx, "REL"+suffix, used, ProductRentals, moneyfmt.FromCents(100000), "254700000001", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := models.Channels.Release(ctx, m.ID, ProductRentals, used, ownerA); !errors.Is(err, ErrChannelHasPayments) {
		t.Fatalf("release with payment history err = %v, want ErrChannelHasPayments", err)
	}
	if _, err := models.Channels.Get(ctx, m.ID, used); err != nil {
		t.Fatalf("claim must survive a refused release: %v", err)
	}
}
