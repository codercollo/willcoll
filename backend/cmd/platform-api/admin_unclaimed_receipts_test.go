package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

// Unclaimed-channel receipts on the Super Admin's System page (C3.2): listed
// with the phone masked, resolved only with a note, resolved once, audited, and
// closed to every non-admin. The table behind the fake is in memory; its SQL is
// proven against Postgres in internal/platform/data's integration test.

type fakeUnclaimed struct {
	mu      sync.Mutex
	rows    []*sqlc.UnclaimedReceipt
	claimed map[string]uuid.UUID // channel id -> firm, for channels that have since been claimed
}

func (f *fakePlatform) withUnclaimed(t *testing.T, receipts ...sqlc.UnclaimedReceipt) *fakeUnclaimed {
	t.Helper()
	u := &fakeUnclaimed{claimed: map[string]uuid.UUID{}}
	for i := range receipts {
		r := receipts[i]
		u.rows = append(u.rows, &r)
	}
	find := func(id uuid.UUID) *sqlc.UnclaimedReceipt {
		for _, r := range u.rows {
			if r.ID == id {
				return r
			}
		}
		return nil
	}
	f.store.EXPECT().ListOpenUnclaimedReceipts(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, int32) ([]sqlc.UnclaimedReceipt, error) {
		u.mu.Lock()
		defer u.mu.Unlock()
		out := []sqlc.UnclaimedReceipt{}
		for _, r := range u.rows {
			if r.Status == "open" {
				out = append(out, *r)
			}
		}
		return out, nil
	}).AnyTimes()
	f.store.EXPECT().UnclaimedReceiptStats(gomock.Any()).DoAndReturn(func(context.Context) (sqlc.UnclaimedReceiptStatsRow, error) {
		u.mu.Lock()
		defer u.mu.Unlock()
		var row sqlc.UnclaimedReceiptStatsRow
		for _, r := range u.rows {
			if r.Status == "open" {
				row.OpenCount++
				row.OpenAmount = row.OpenAmount.Add(r.Amount)
				row.OldestAgeSeconds = 90
			}
		}
		return row, nil
	}).AnyTimes()
	f.store.EXPECT().ResolveUnclaimedReceipt(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a sqlc.ResolveUnclaimedReceiptParams) (sqlc.UnclaimedReceipt, error) {
		u.mu.Lock()
		defer u.mu.Unlock()
		r := find(a.ID)
		if r == nil || r.Status != "open" {
			return sqlc.UnclaimedReceipt{}, sql.ErrNoRows
		}
		now := time.Now()
		r.Status, r.ResolvedBy, r.ResolvedAt, r.ResolutionNote = "resolved", a.ResolvedBy, &now, a.ResolutionNote
		return *r, nil
	}).AnyTimes()
	f.store.EXPECT().GetPaymentChannelByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, ch string) (sqlc.PaymentChannel, error) {
		u.mu.Lock()
		defer u.mu.Unlock()
		firm, ok := u.claimed[ch]
		if !ok {
			return sqlc.PaymentChannel{}, sql.ErrNoRows
		}
		return sqlc.PaymentChannel{ChannelID: ch, ManagerID: firm, Product: "rentals"}, nil
	}).AnyTimes()
	return u
}

func openReceipt(receipt, channel, msisdn string, cents int64) sqlc.UnclaimedReceipt {
	return sqlc.UnclaimedReceipt{
		ID: uuid.New(), MpesaReceipt: receipt, ChannelID: channel, Product: "rentals",
		Amount: moneyfmt.FromCents(cents), Msisdn: msisdn, RawPayload: json.RawMessage(`{"secret":"full payload"}`),
		ReceivedAt: time.Now().Add(-time.Hour), Status: "open",
	}
}

func TestUnclaimedReceiptRoutesRequireAdmin(t *testing.T) {
	f := newFakePlatform(t)
	r := openReceipt("RCPT1", "CH-X", "254722000001", 100000)
	u := f.withUnclaimed(t, r)
	managerCookie := f.session(t, principalManager, fakeManagerID)
	staffCookie := f.session(t, principalStaff, fakeStaffID)

	routes := []struct{ method, path string }{
		{"GET", "/v1/admin/unclaimed-receipts"},
		{"POST", "/v1/admin/unclaimed-receipts/" + r.ID.String() + "/resolve"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			if w := f.do(rt.method, rt.path, `{"note":"x"}`, nil); w.Code != http.StatusUnauthorized {
				t.Errorf("anonymous: %d, want 401", w.Code)
			}
			if w := f.do(rt.method, rt.path, `{"note":"x"}`, managerCookie); w.Code != http.StatusForbidden {
				t.Errorf("manager: %d, want 403", w.Code)
			}
			if w := f.do(rt.method, rt.path, `{"note":"x"}`, staffCookie); w.Code != http.StatusForbidden {
				t.Errorf("staff: %d, want 403", w.Code)
			}
		})
	}
	if u.rows[0].Status != "open" {
		t.Error("a refused non-admin request resolved the receipt")
	}
}

func TestListUnclaimedReceiptsMasksThePhoneAndOmitsThePayload(t *testing.T) {
	f := newFakePlatform(t)
	f.withUnclaimed(t, openReceipt("RCPT1", "CH-X", "254722000001", 150050))
	admin := f.session(t, principalAdmin, fakeAdminID)

	w := f.do(http.MethodGet, "/v1/admin/unclaimed-receipts", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, leak := range []string{"254722000001", "full payload", "raw_payload", "msisdn"} {
		if strings.Contains(body, leak) {
			t.Errorf("the list leaks %q: %s", leak, body)
		}
	}
	var got struct {
		Receipts []struct {
			MpesaReceipt string `json:"mpesa_receipt"`
			ChannelID    string `json:"channel_id"`
			Phone        string `json:"phone"`
			Status       string `json:"status"`
		} `json:"unclaimed_receipts"`
		Stats struct {
			Open int `json:"open"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Receipts) != 1 || got.Receipts[0].MpesaReceipt != "RCPT1" || got.Receipts[0].ChannelID != "CH-X" ||
		got.Receipts[0].Status != "open" || got.Receipts[0].Phone == "" || got.Stats.Open != 1 {
		t.Errorf("list = %+v", got)
	}
}

func TestResolveUnclaimedReceipt(t *testing.T) {
	f := newFakePlatform(t)
	r := openReceipt("RCPT1", "CH-X", "254722000001", 100000)
	u := f.withUnclaimed(t, r)
	admin := f.session(t, principalAdmin, fakeAdminID)
	path := "/v1/admin/unclaimed-receipts/" + r.ID.String() + "/resolve"

	// A note is required: blank and missing are refused, and nothing changes.
	for _, body := range []string{`{}`, `{"note":"   "}`, `{"note":"` + strings.Repeat("x", 1001) + `"}`} {
		if w := f.do(http.MethodPost, path, body, admin); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %.30q: %d, want 422", body, w.Code)
		}
	}
	if u.rows[0].Status != "open" {
		t.Fatal("a refused resolve closed the receipt")
	}
	if w := f.do(http.MethodPost, "/v1/admin/unclaimed-receipts/"+uuid.NewString()+"/resolve", `{"note":"x"}`, admin); w.Code != http.StatusNotFound {
		t.Errorf("unknown receipt: %d, want 404", w.Code)
	}

	// The channel is still unclaimed: resolved, with who and why on the row, but
	// no firm to put an audit_log entry under.
	if w := f.do(http.MethodPost, path, `{"note":"  refunded by M-Pesa reversal  "}`, admin); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	row := u.rows[0]
	if row.Status != "resolved" || row.ResolvedBy == nil || *row.ResolvedBy != fakeAdminID || row.ResolutionNote == nil || *row.ResolutionNote != "refunded by M-Pesa reversal" {
		t.Errorf("row after resolve = %+v", row)
	}
	if got := f.auditActions(); len(got) != 0 {
		t.Errorf("audit rows for a channel with no firm = %v, want none", got)
	}

	// Resolving again is a 404 and leaves the first resolution alone.
	if w := f.do(http.MethodPost, path, `{"note":"overwritten"}`, admin); w.Code != http.StatusNotFound {
		t.Errorf("second resolve: %d, want 404", w.Code)
	}
	if *u.rows[0].ResolutionNote != "refunded by M-Pesa reversal" {
		t.Errorf("the first resolution was overwritten: %q", *u.rows[0].ResolutionNote)
	}

	// It no longer shows as open.
	w := f.do(http.MethodGet, "/v1/admin/unclaimed-receipts", "", admin)
	var list struct {
		Receipts []any `json:"unclaimed_receipts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Receipts) != 0 {
		t.Errorf("open list after resolve = %s (err %v)", w.Body, err)
	}
}

// When the channel has since been claimed, the resolution lands in that firm's
// audit log as the Super Admin's action.
func TestResolveUnclaimedReceiptIsAuditedForTheClaimingFirm(t *testing.T) {
	f := newFakePlatform(t)
	r := openReceipt("RCPT2", "CH-NOW-CLAIMED", "254722000002", 50000)
	u := f.withUnclaimed(t, r)
	u.claimed["CH-NOW-CLAIMED"] = fakeManagerID
	admin := f.session(t, principalAdmin, fakeAdminID)

	if w := f.do(http.MethodPost, "/v1/admin/unclaimed-receipts/"+r.ID.String()+"/resolve", `{"note":"replayed after claim"}`, admin); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	got := f.auditActions()
	if len(got) != 1 || got[0] != "admin:unclaimed_receipt.resolved" {
		t.Fatalf("audit = %v, want one admin:unclaimed_receipt.resolved", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if a := f.audit[0]; a.ManagerID != fakeManagerID || a.EntityID == nil || *a.EntityID != r.ID || !strings.Contains(a.Summary, "replayed after claim") {
		t.Errorf("audit row = %+v", a)
	}
}

// The System page shows how much unclaimed money is open.
func TestUnclaimedReceiptsStatusOnTheSystemPage(t *testing.T) {
	f := newFakePlatform(t)
	f.withUnclaimed(t, openReceipt("A", "CH-1", "254722000001", 100000), openReceipt("B", "CH-2", "254722000002", 250000))

	got := f.app.unclaimedReceiptsStatus(context.Background())
	if got["status"] != "ok" || got["open"] != 2 || got["open_amount"] != moneyfmt.FromCents(350000) {
		t.Errorf("status = %+v, want 2 open totalling 3500.00", got)
	}
}
