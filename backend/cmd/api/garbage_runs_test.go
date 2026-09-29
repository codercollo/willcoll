package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
)

func garbageUnits(codes ...string) []sqlc.ListGarbageUnitsForPeriodRow {
	rows := make([]sqlc.ListGarbageUnitsForPeriodRow, len(codes))
	for i, code := range codes {
		rows[i] = sqlc.ListGarbageUnitsForPeriodRow{UnitID: uuid.New(), UnitCode: code, TenantName: "TENANT " + code, PriorBalance: money("300"), Billed: true}
	}
	return rows
}

// garbageUnitsUnbilled is occupied units whose leases have garbage billing
// turned off: the 2026-09-27 regression, garbage_billed defaulting false.
func garbageUnitsUnbilled(codes ...string) []sqlc.ListGarbageUnitsForPeriodRow {
	rows := garbageUnits(codes...)
	for i := range rows {
		rows[i].Billed = false
	}
	return rows
}

func TestGenerateGarbageCharges(t *testing.T) {
	const target = "/?period=2026-09"

	// enabled returns the shared test property with garbage billing on at
	// KSh 300, or switched off.
	property := func(enabled bool, fee string) sqlc.Property {
		p := testPropertyRow()
		p.GarbageEnabled = enabled
		p.GarbageFee = money(fee)
		return p
	}

	tests := []struct {
		name       string
		target     string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"bills every occupied unit the fixed fee", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(true, "300"), nil)
				q.EXPECT().ListGarbageUnitsForPeriod(gomock.Any(), gomock.Any()).Return(garbageUnits("A1", "A2", "A3"), nil)
				q.EXPECT().CreateGarbageRun(gomock.Any(), sqlc.CreateGarbageRunParams{
					TenantID: testTenantID, PropertyID: testPropertyID, Period: sep2026, FeeSnapshot: money("300"),
				}).Return(uuid.New(), nil)
				q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, p sqlc.CreateTransactionHeaderParams) (uuid.UUID, error) {
						if p.Type != data.TxTypeGarbageRun || p.IdempotencyKey != "garbage-run:"+testPropertyID.String()+":2026-09" {
							t.Errorf("unexpected header %+v", p)
						}
						return uuid.New(), nil
					})
				q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(3)
				q.EXPECT().CreateCharge(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(3)
				q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(3)
			}, http.StatusCreated, `"total": "900.00"`},
		{"garbage billing switched off", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(false, "300"), nil)
			}, http.StatusUnprocessableEntity, "not enabled"},
		{"zero fee posts nothing", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(true, "0"), nil)
			}, http.StatusUnprocessableEntity, "nothing to generate"},
		{"no occupied units", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(true, "300"), nil)
				q.EXPECT().ListGarbageUnitsForPeriod(gomock.Any(), gomock.Any()).Return(nil, nil)
			}, http.StatusUnprocessableEntity, "nothing to generate"},
		{"occupied units exist but none has garbage billing on", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(true, "300"), nil)
				q.EXPECT().ListGarbageUnitsForPeriod(gomock.Any(), gomock.Any()).Return(garbageUnitsUnbilled("A1", "A2"), nil)
			}, http.StatusUnprocessableEntity, "turn it on in the lease"},
		{"second run for the period is refused", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(property(true, "300"), nil)
				q.EXPECT().ListGarbageUnitsForPeriod(gomock.Any(), gomock.Any()).Return(garbageUnits("A1"), nil)
				q.EXPECT().CreateGarbageRun(gomock.Any(), gomock.Any()).
					Return(uuid.Nil, &pgconn.PgError{Code: "23505", ConstraintName: "garbage_runs_property_id_period_key"})
			}, http.StatusConflict, "already generated"},
		{"bad period", "/", func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "YYYY-MM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsManager(app.generateGarbageChargesHandler, http.MethodPost, tc.target, "", propParams())
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Errorf("body %s missing %q", w.Body, tc.wantBody)
			}
		})
	}
}

func TestPreviewGarbageCharges(t *testing.T) {
	app, q := newPropertyTestApp(t)
	p := testPropertyRow()
	p.GarbageEnabled, p.GarbageFee = true, money("300")
	q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(p, nil)
	q.EXPECT().ListGarbageUnitsForPeriod(gomock.Any(), gomock.Any()).Return(garbageUnits("A1"), nil)
	q.EXPECT().GarbageRunExists(gomock.Any(), gomock.Any()).Return(true, nil)

	w := serveAsManager(app.previewGarbageChargesHandler, http.MethodGet, "/?period=2026-09", "", propParams())
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	for _, want := range []string{`"already_generated": true`, `"total_due": "600.00"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("body %s missing %q", w.Body, want)
		}
	}
}

func TestShowGarbageInvoicesPDF(t *testing.T) {
	row := sqlc.ListGarbageInvoicesRow{
		UnitID: testUnitA, UnitCode: "A1", TenantName: "JOHN KAMAU", Period: sep2026,
		BillDate: sep2026, Fee: money("300"), PriorBalance: money("600"),
	}
	setup := func(q *mock.MockQuerier, enabled bool, rows []sqlc.ListGarbageInvoicesRow) {
		p := testPropertyRow()
		p.GarbageEnabled = enabled
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(p, nil)
		q.EXPECT().GetLandlord(gomock.Any(), gomock.Any()).Return(testLandlordRowWithBank(), nil)
		if enabled {
			q.EXPECT().ListGarbageInvoices(gomock.Any(), gomock.Any()).Return(rows, nil)
		}
	}
	period := gin.Param{Key: "period", Value: "2026-09.pdf"}

	t.Run("renders a garbage-only bill", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, true, []sqlc.ListGarbageInvoicesRow{row})
		w := serveAsManager(app.showGarbageInvoicesPDFHandler, http.MethodGet, "/", "", propParams(period))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "%PDF") {
			t.Fatalf("status %d: %.200s", w.Code, w.Body)
		}
		for _, want := range []string{"JOHN KAMAU", "600.00", "900.00"} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("PDF missing %q", want)
			}
		}
		if strings.Contains(strings.ToLower(w.Body.String()), "meter") {
			t.Error("garbage bill must not carry meter fields")
		}
	})
	t.Run("garbage disabled is 422", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, false, nil)
		w := serveAsManager(app.showGarbageInvoicesPDFHandler, http.MethodGet, "/", "", propParams(period))
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d, want 422", w.Code)
		}
	})
	t.Run("never billed is 404", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setup(q, true, nil)
		w := serveAsManager(app.showGarbageInvoicesPDFHandler, http.MethodGet, "/", "", propParams(period))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", w.Code)
		}
	})
}
