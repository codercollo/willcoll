package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// openPlatformModels connects the app and admin pools of the throwaway
// willcoll_platform database and returns the wired models with both connections.
func openPlatformModels(t *testing.T) (models Models, appConn, adminConn *sql.DB) {
	t.Helper()
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
	appConn, adminConn = open(dsns[0]), open(dsns[1])
	return NewModels(appConn, adminConn, 5*time.Second), appConn, adminConn
}

// TestUnclaimedReceiptsIntegration runs the unclaimed-channel money record
// against a real willcoll_platform (C3.2): written idempotently (also under
// concurrent redelivery), stored in full, listed with the phone masked, never
// editable by the product's app role, and resolved exactly once with a note.
func TestUnclaimedReceiptsIntegration(t *testing.T) {
	models, appConn, adminConn := openPlatformModels(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	receipt, channel := "UNC"+suffix, "UNC-CH-"+suffix // the channel has no payment_channels row, by design
	const msisdn = "254722123456"
	raw := json.RawMessage(`{"response":{"MpesaReceiptNumber":"` + receipt + `","Phone":"` + msisdn + `"}}`)
	amount := moneyfmt.FromCents(125050)

	// Concurrent redeliveries record the money once.
	const deliveries = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	recorded := 0
	for range deliveries {
		wg.Go(func() {
			ok, err := models.Unclaimed.Record(ctx, receipt, channel, ProductRentals, amount, msisdn, raw)
			if err != nil {
				t.Errorf("Record: %v", err)
				return
			}
			if ok {
				mu.Lock()
				recorded++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if recorded != 1 {
		t.Fatalf("%d of %d concurrent deliveries reported recording the row, want exactly 1", recorded, deliveries)
	}
	var rows int
	if err := adminConn.QueryRow(`SELECT count(*) FROM unclaimed_receipts WHERE mpesa_receipt = $1`, receipt).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows for one receipt = %d (err %v), want 1", rows, err)
	}

	// Stored in full: the payload, the whole phone, the exact amount, open.
	var gotRaw []byte
	var gotPhone, gotStatus, gotChannel, gotAmount string
	if err := adminConn.QueryRow(`SELECT raw_payload, msisdn, status, channel_id, amount::text FROM unclaimed_receipts WHERE mpesa_receipt = $1`, receipt).
		Scan(&gotRaw, &gotPhone, &gotStatus, &gotChannel, &gotAmount); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gotRaw), receipt) || gotPhone != msisdn || gotStatus != UnclaimedOpen || gotChannel != channel || gotAmount != "1250.50" {
		t.Errorf("stored row = payload %s phone %s status %s channel %s amount %s", gotRaw, gotPhone, gotStatus, gotChannel, gotAmount)
	}

	// A payment with no receipt number is still recorded once, not lost and not duplicated.
	noReceipt := json.RawMessage(`{"response":{"Phone":"` + msisdn + `","note":"` + suffix + `"}}`)
	for i, want := range []bool{true, false} {
		ok, err := models.Unclaimed.Record(ctx, "", channel, ProductRentals, amount, msisdn, noReceipt)
		if err != nil || ok != want {
			t.Fatalf("Record without a receipt, delivery %d: recorded=%v err=%v, want %v", i+1, ok, err, want)
		}
	}

	// Listed for the System page with the phone masked and the payload left out.
	open, err := models.Unclaimed.ListOpen(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var mine *UnclaimedReceipt
	for i := range open {
		if open[i].MpesaReceipt == receipt {
			mine = &open[i]
		}
	}
	if mine == nil {
		t.Fatalf("the open receipt is not in the open list (%d rows)", len(open))
	}
	if strings.Contains(mine.Phone, "123456") || mine.Phone == "" || mine.Amount != amount || mine.ChannelID != channel {
		t.Errorf("listed receipt = %+v, want a masked phone, the amount and the channel", mine)
	}
	stats, err := models.Unclaimed.Stats(ctx)
	if err != nil || stats.Open < 2 || stats.OpenAmount.Cents() < 2*amount.Cents() {
		t.Fatalf("stats = %+v (err %v), want at least these two open receipts", stats, err)
	}

	// The product's app role can record but never edit or delete received money.
	if _, err := appConn.Exec(`UPDATE unclaimed_receipts SET status = 'resolved' WHERE mpesa_receipt = $1`, receipt); err == nil {
		t.Error("the app role could UPDATE unclaimed_receipts")
	}
	if _, err := appConn.Exec(`DELETE FROM unclaimed_receipts WHERE mpesa_receipt = $1`, receipt); err == nil {
		t.Error("the app role could DELETE from unclaimed_receipts")
	}

	// Resolving needs a note, and the database enforces it too.
	admin := uuid.New()
	if _, err := models.Unclaimed.Resolve(ctx, mine.ID, admin, "   "); err == nil {
		t.Error("resolved with a blank note")
	}
	if got, err := models.Unclaimed.Get(ctx, mine.ID); err != nil || got.Status != UnclaimedOpen {
		t.Fatalf("after a refused resolve: %+v (err %v), want still open", got, err)
	}

	resolved, err := models.Unclaimed.Resolve(ctx, mine.ID, admin, "  replayed after the channel was claimed  ")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != UnclaimedResolved || resolved.ResolvedBy == nil || *resolved.ResolvedBy != admin ||
		resolved.ResolvedAt == nil || resolved.ResolutionNote == nil || *resolved.ResolutionNote != "replayed after the channel was claimed" {
		t.Errorf("resolved = %+v", resolved)
	}

	// Once: the second resolve finds nothing open and the first note stands.
	if _, err := models.Unclaimed.Resolve(ctx, mine.ID, uuid.New(), "overwrite"); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("second resolve err = %v, want ErrRecordNotFound", err)
	}
	if got, _ := models.Unclaimed.Get(ctx, mine.ID); got == nil || *got.ResolutionNote != "replayed after the channel was claimed" || *got.ResolvedBy != admin {
		t.Errorf("after a second resolve: %+v, want the first resolution", got)
	}

	// A redelivery of a resolved receipt does not reopen it.
	if ok, err := models.Unclaimed.Record(ctx, receipt, channel, ProductRentals, amount, msisdn, raw); err != nil || ok {
		t.Errorf("redelivery after resolve: recorded=%v err=%v, want false/nil", ok, err)
	}
	open, _ = models.Unclaimed.ListOpen(ctx, 1000)
	for _, r := range open {
		if r.MpesaReceipt == receipt {
			t.Error("the resolved receipt is still in the open list")
		}
	}
	if _, err := models.Unclaimed.Get(ctx, uuid.New()); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("Get unknown err = %v, want ErrRecordNotFound", err)
	}
}
