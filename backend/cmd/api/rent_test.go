package main

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
)

func mustDate(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

var testEntryID = uuid.MustParse("00000000-0000-0000-0000-0000000000e1")

func unitParams() gin.Params { return gin.Params{{Key: "id", Value: testUnitA.String()}} }

func TestUpdateRentScheduleHandler(t *testing.T) {
	leaseRows := []sqlc.ListActiveLeasesForPropertyRow{
		{UnitID: testUnitA, UnitCode: "A1", LeaseID: uuid.New(), TenantName: "JOHN"},
	}
	body := func(unit uuid.UUID, amount string) string {
		return `[{"unit_id":"` + unit.String() + `","rent_amount":"` + amount + `"}]`
	}

	tests := []struct {
		name       string
		body       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"saved", body(testUnitA, "6000.00"),
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil).AnyTimes()
				q.EXPECT().ListActiveLeasesForProperty(gomock.Any(), gomock.Any()).Return(leaseRows, nil)
				q.EXPECT().UpdateLeaseRentAmount(gomock.Any(), gomock.Any()).Return(int32(2), nil)
				q.EXPECT().ListRentOverview(gomock.Any(), gomock.Any()).Return(nil, nil)
			}, http.StatusOK, `"rent"`},
		{"unit without an active lease", body(testUnitB, "6000.00"),
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				q.EXPECT().ListActiveLeasesForProperty(gomock.Any(), gomock.Any()).Return(leaseRows, nil)
			}, http.StatusUnprocessableEntity, "no active lease"},
		{"zero rent", body(testUnitA, "0"),
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				q.EXPECT().ListActiveLeasesForProperty(gomock.Any(), gomock.Any()).Return(leaseRows, nil)
			}, http.StatusUnprocessableEntity, "greater than zero"},
		{"empty body array", `[]`, func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "at least one"},
		{"missing rent_amount", `[{"unit_id":"` + testUnitA.String() + `"}]`, func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "rent_amount"},
		{"duplicate unit", `[{"unit_id":"` + testUnitA.String() + `","rent_amount":"1"},{"unit_id":"` + testUnitA.String() + `","rent_amount":"2"}]`,
			func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "only once"},
		{"not an array", `{"unit_id":"x"}`, func(*mock.MockQuerier) {}, http.StatusBadRequest, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsManager(app.updateRentScheduleHandler, http.MethodPut, "/", tc.body, propParams())
			if w.Code != tc.wantStatus || !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("status %d (want %d), body %s (want %q)", w.Code, tc.wantStatus, w.Body, tc.wantBody)
			}
		})
	}
}

func TestGenerateRentChargesHandler(t *testing.T) {
	lease := func(code, rent string, start string) sqlc.ListActiveLeasesForPropertyRow {
		return sqlc.ListActiveLeasesForPropertyRow{
			UnitID: uuid.New(), UnitCode: code, LeaseID: uuid.New(), RentAmount: money(rent), StartDate: mustDate(start),
		}
	}

	t.Run("accepts YYYY-MM-01, bills each unit and skips the rest", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().ListActiveLeasesForProperty(gomock.Any(), gomock.Any()).Return([]sqlc.ListActiveLeasesForPropertyRow{
			lease("A1", "6000", "2026-01-01"),
			lease("A2", "8000", "2026-01-01"), // already billed for the period
			lease("A3", "4000", "2027-01-01"), // lease starts after the period
		}, nil)
		gomock.InOrder(
			q.EXPECT().InsertRentRun(gomock.Any(), gomock.Any()).Return(uuid.New(), nil),
			q.EXPECT().InsertRentRun(gomock.Any(), gomock.Any()).Return(uuid.Nil, sql.ErrNoRows),
		)
		q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, p sqlc.CreateTransactionHeaderParams) (uuid.UUID, error) {
				if p.Type != "RENT_RUN" || !strings.HasPrefix(p.IdempotencyKey, "rent-run:") {
					t.Errorf("header = %+v", p)
				}
				return uuid.New(), nil
			}).Times(1)
		q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)
		q.EXPECT().CreateCharge(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)
		q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)

		w := serveAsManager(app.generateRentChargesHandler, http.MethodPost, "/?period=2026-09-01", "", propParams())
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		for _, want := range []string{`"billed": 1`, `"skipped": 2`, `"total_amount": "6000.00"`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("body missing %q: %s", want, w.Body)
			}
		}
	})
	t.Run("a period beyond next month is refused before any query", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		w := serveAsManager(app.generateRentChargesHandler, http.MethodPost, "/?period=2099-01-01", "", propParams())
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "beyond next calendar month") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
	t.Run("missing period", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		w := serveAsManager(app.generateRentChargesHandler, http.MethodPost, "/", "", propParams())
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("retries exhausted is a 409, not a 500", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(sqlc.Property{}, &pgconn.PgError{Code: "40001"})
		w := serveAsManager(app.generateRentChargesHandler, http.MethodPost, "/?period=2026-09", "", propParams())
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
}

func TestShowRentOverviewHandler(t *testing.T) {
	app, q := newPropertyTestApp(t)
	q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
	q.EXPECT().ListRentOverview(gomock.Any(), gomock.Any()).Return([]sqlc.ListRentOverviewRow{
		{UnitID: uuid.New(), UnitCode: "A1", TenantName: "JOHN", Expected: money("6000"), Billed: true, Paid: money("2500"), Balance: money("3500")},
		{UnitID: uuid.New(), UnitCode: "A2", TenantName: "ACME", Expected: money("8000"), Billed: true, Paid: money("8000"), Balance: money("0")},
		{UnitID: uuid.New(), UnitCode: "A3", TenantName: "NEW", Expected: money("4000"), Billed: true, Paid: money("0"), Balance: money("4000")},
	}, nil)

	w := serveAsManager(app.showRentOverviewHandler, http.MethodGet, "/?period=2026-09", "", propParams())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	for _, want := range []string{`"status": "partial"`, `"status": "paid"`, `"status": "arrears"`, `"paid_this_period": "2500.00"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func expectManualPayment(q *mock.MockQuerier, allocatedBack string) {
	q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA}, nil)
	q.EXPECT().GetActiveLeaseByUnit(gomock.Any(), gomock.Any()).Return(sqlc.Lease{TenantName: "JOHN", PrimaryPhone: "+254722000000"}, nil)
	q.EXPECT().CreatePayment(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, p sqlc.CreatePaymentParams) (sqlc.Payment, error) {
			return sqlc.Payment{ID: uuid.New(), Source: p.Source, MpesaReceipt: p.MpesaReceipt, Amount: p.Amount}, nil
		})
	q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
	q.EXPECT().CreatePaymentAllocation(gomock.Any(), gomock.Any()).Return(nil)
	q.EXPECT().SumPaymentAllocations(gomock.Any(), gomock.Any()).Return(money(allocatedBack), nil)
}

func TestCreateRentPaymentHandler(t *testing.T) {
	const good = `{"amount":"2500.00","source":"bank","reference":"KCB-77","note":"part"}`

	t.Run("credits the ledger and marks the payment allocated", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetManualPaymentByReference(gomock.Any(), gomock.Any()).Return(uuid.Nil, sql.ErrNoRows)
		expectManualPayment(q, "2500")
		expectReceiptIssuance(q, testUnitA)
		q.EXPECT().SetPaymentMatch(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, p sqlc.SetPaymentMatchParams) error {
				if p.Status != "allocated" || p.MatchedUnitID == nil || *p.MatchedUnitID != testUnitA {
					t.Errorf("match = %+v", p)
				}
				return nil
			})
		w := serveAsManager(app.createRentPaymentHandler, http.MethodPost, "/", good, unitParams())
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"ledger_type": "RENT"`) || !strings.Contains(w.Body.String(), "MANUAL-") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
	t.Run("zero-sum mismatch aborts instead of posting a wrong split", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetManualPaymentByReference(gomock.Any(), gomock.Any()).Return(uuid.Nil, sql.ErrNoRows)
		expectManualPayment(q, "2000") // less than the 2,500 paid
		expectReceiptIssuance(q, testUnitA)
		// SetPaymentMatch must NOT be called.
		w := serveAsManager(app.createRentPaymentHandler, http.MethodPost, "/", good, unitParams())
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d, want 500 (rolled back): %s", w.Code, w.Body)
		}
	})
	t.Run("duplicate reference is a 409", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: testUnitA}, nil)
		q.EXPECT().GetManualPaymentByReference(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
		w := serveAsManager(app.createRentPaymentHandler, http.MethodPost, "/", good, unitParams())
		if w.Code != http.StatusConflict {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})
	for name, tc := range map[string]struct{ body, want string }{
		"bad source":     {`{"amount":"1","source":"mpesa"}`, "source"},
		"missing amount": {`{"source":"manual"}`, "amount"},
		"unknown ledger": {`{"amount":"1","source":"manual","ledger_type":"tax"}`, "ledger_type"},
		"unknown field":  {`{"amount":"1","source":"manual","x":1}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			app, _ := newPropertyTestApp(t)
			w := serveAsManager(app.createRentPaymentHandler, http.MethodPost, "/", tc.body, unitParams())
			if w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("body %s missing %q", w.Body, tc.want)
			}
		})
	}
}

func TestReverseLedgerEntryHandler(t *testing.T) {
	entryParams := gin.Params{{Key: "id", Value: testEntryID.String()}}
	entry := func(refType string, reversed bool) sqlc.GetLedgerEntryForReversalRow {
		return sqlc.GetLedgerEntryForReversalRow{
			ID: testEntryID, LedgerAccountID: uuid.New(), Direction: "CREDIT", Amount: money("2500"),
			ReferenceType: refType, UnitID: testUnitA, LedgerType: "RENT", Reversed: reversed,
		}
	}

	t.Run("appends a mirrored entry, never editing the original", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(entry("payment_allocation", false), nil)
		q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, p sqlc.CreateTransactionHeaderParams) (uuid.UUID, error) {
				if p.Type != "REVERSAL" || p.IdempotencyKey != "reversal:"+testEntryID.String() ||
					!strings.Contains(p.Description, "cheque bounced") || !strings.Contains(p.Description, testEntryID.String()) {
					t.Errorf("header = %+v", p)
				}
				return uuid.New(), nil
			})
		q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, p sqlc.CreateLedgerEntryParams) (uuid.UUID, error) {
				if p.Direction != "DEBIT" || p.ReferenceType != "reversal" || p.ReferenceID != testEntryID || p.Amount != money("2500") {
					t.Errorf("entry = %+v (want the CREDIT flipped to DEBIT, same amount, pointing at the original)", p)
				}
				return uuid.New(), nil
			})
		q.EXPECT().VoidReceipt(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, p sqlc.VoidReceiptParams) error {
				if p.VoidReason == nil || *p.VoidReason != "cheque bounced" {
					t.Errorf("void reason = %+v", p)
				}
				return nil
			})
		w := serveAsManager(app.reverseLedgerEntryHandler, http.MethodPost, "/", `{"reason":"cheque bounced"}`, entryParams)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "reversal_entry_id") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	tests := []struct {
		name       string
		body       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"already reversed", `{"reason":"x"}`, func(q *mock.MockQuerier) {
			q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(entry("charge", true), nil)
		}, http.StatusConflict, "already reversed"},
		{"a reversal cannot be reversed", `{"reason":"x"}`, func(q *mock.MockQuerier) {
			q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(entry("reversal", false), nil)
		}, http.StatusConflict, "cannot itself be reversed"},
		{"lost the race: unique idempotency key", `{"reason":"x"}`, func(q *mock.MockQuerier) {
			q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(entry("charge", false), nil)
			q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).
				Return(uuid.Nil, &pgconn.PgError{Code: "23505", ConstraintName: "transaction_headers_idempotency_key_key"})
		}, http.StatusConflict, "already reversed"},
		{"unknown entry", `{"reason":"x"}`, func(q *mock.MockQuerier) {
			q.EXPECT().GetLedgerEntryForReversal(gomock.Any(), gomock.Any()).Return(sqlc.GetLedgerEntryForReversalRow{}, sql.ErrNoRows)
		}, http.StatusNotFound, ""},
		{"reason required", `{"reason":""}`, func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "reason"},
		{"blank reason", `{"reason":"   "}`, func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "reason is required"},
		{"no body", ``, func(*mock.MockQuerier) {}, http.StatusBadRequest, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsManager(app.reverseLedgerEntryHandler, http.MethodPost, "/", tc.body, entryParams)
			if w.Code != tc.wantStatus || !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("status %d (want %d), body %s (want %q)", w.Code, tc.wantStatus, w.Body, tc.wantBody)
			}
		})
	}
}
