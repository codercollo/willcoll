package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/mock/gomock"
)

var (
	testUnitA = uuid.MustParse("00000000-0000-0000-0000-0000000000c1")
	testUnitB = uuid.MustParse("00000000-0000-0000-0000-0000000000c2")
)

// serveAsManager runs handler as the signed-in test manager (whose id is
// the tenant id).
func serveAsManager(handler gin.HandlerFunc, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Params = params
	contextSetManager(c, &data.Manager{ID: testTenantID})
	handler(c)
	return w
}

func propParams(extra ...gin.Param) gin.Params {
	return append(gin.Params{{Key: "id", Value: testPropertyID.String()}}, extra...)
}

func money(s string) moneyfmt.Money {
	m, err := moneyfmt.Parse(s)
	if err != nil {
		panic(err)
	}
	return m
}

var sep2026 = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func gridRow(unit uuid.UUID, code string, locked bool) sqlc.ListWaterGridRow {
	return sqlc.ListWaterGridRow{
		UnitID: unit, UnitCode: code, TenantName: "TENANT " + code,
		PreviousReading: money("100"), Locked: locked,
	}
}

func expectGrid(q *mock.MockQuerier, rows ...sqlc.ListWaterGridRow) {
	q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil).AnyTimes()
	q.EXPECT().ListWaterGrid(gomock.Any(), sqlc.ListWaterGridParams{
		TenantID: testTenantID, PropertyID: testPropertyID, Period: sep2026,
	}).Return(rows, nil).AnyTimes()
}

func TestSaveWaterReadings(t *testing.T) {
	const target = "/?period=2026-09"
	body := func(unit uuid.UUID, cur string) string {
		return `{"readings":[{"unit_id":"` + unit.String() + `","current_reading":"` + cur + `"}]}`
	}

	tests := []struct {
		name       string
		body       string
		target     string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"saves with defaults from grid and property rate", body(testUnitA, "150"), target,
			func(q *mock.MockQuerier) {
				expectGrid(q, gridRow(testUnitA, "A1", false))
				// ReadingDate defaults to today (not asserted exactly:
				// it's real wall-clock time, not test-controlled).
				q.EXPECT().UpsertWaterReading(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, p sqlc.UpsertWaterReadingParams) (uuid.UUID, error) {
						want := sqlc.UpsertWaterReadingParams{
							TenantID: testTenantID, UnitID: testUnitA, Period: sep2026,
							PreviousReading: money("100"), CurrentReading: money("150"),
							RateSnapshot: testPropertyRow().WaterRatePerUnit, RecordedBy: testTenantID,
							ReadingDate: p.ReadingDate,
						}
						if p != want {
							t.Errorf("UpsertWaterReading params = %+v, want %+v", p, want)
						}
						return uuid.New(), nil
					})
			}, http.StatusOK, `"unit_code": "A1"`},
		{"current below previous", body(testUnitA, "90"), target,
			func(q *mock.MockQuerier) { expectGrid(q, gridRow(testUnitA, "A1", false)) },
			http.StatusUnprocessableEntity, "below the previous reading"},
		{"unit from another property", body(testUnitB, "150"), target,
			func(q *mock.MockQuerier) { expectGrid(q, gridRow(testUnitA, "A1", false)) },
			http.StatusUnprocessableEntity, "not an occupied unit"},
		{"already locked row", body(testUnitA, "150"), target,
			func(q *mock.MockQuerier) { expectGrid(q, gridRow(testUnitA, "A1", true)) },
			http.StatusConflict, "locked"},
		{"locked between read and write", body(testUnitA, "150"), target,
			func(q *mock.MockQuerier) {
				expectGrid(q, gridRow(testUnitA, "A1", false))
				q.EXPECT().UpsertWaterReading(gomock.Any(), gomock.Any()).Return(uuid.Nil, sql.ErrNoRows)
			}, http.StatusConflict, "locked"},
		{"missing current_reading", `{"readings":[{"unit_id":"` + testUnitA.String() + `"}]}`, target,
			func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "current_reading"},
		{"duplicate unit", `{"readings":[{"unit_id":"` + testUnitA.String() + `","current_reading":"1"},{"unit_id":"` + testUnitA.String() + `","current_reading":"2"}]}`, target,
			func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "only once"},
		{"bad period", body(testUnitA, "150"), "/?period=2026-13",
			func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "YYYY-MM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsManager(app.updateWaterReadingsHandler, http.MethodPut, tc.target, tc.body, propParams())
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Errorf("body %s missing %q", w.Body, tc.wantBody)
			}
		})
	}
}

func TestGenerateWaterCharges(t *testing.T) {
	draft := func(unit uuid.UUID, code, amount string) sqlc.ListWaterReadingsForRunRow {
		return sqlc.ListWaterReadingsForRunRow{ID: uuid.New(), UnitID: unit, UnitCode: code, Amount: money(amount)}
	}
	const target = "/?period=2026-09"

	tests := []struct {
		name       string
		target     string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"posts a debit per positive reading and locks every draft", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				q.EXPECT().ListWaterReadingsForRun(gomock.Any(), gomock.Any()).Return([]sqlc.ListWaterReadingsForRunRow{
					draft(testUnitA, "A1", "1500.00"), draft(testUnitB, "A2", "0.00"),
				}, nil)
				q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ any, p sqlc.CreateTransactionHeaderParams) (uuid.UUID, error) {
						if p.Type != data.TxTypeWaterRun || p.IdempotencyKey != "water-run:"+testPropertyID.String()+":2026-09" {
							t.Errorf("unexpected header %+v", p)
						}
						return uuid.New(), nil
					})
				// Only the positive reading is charged; the zero one is just locked.
				q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)
				q.EXPECT().CreateCharge(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)
				q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil).Times(1)
				q.EXPECT().LockWaterReading(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(2)
				q.EXPECT().ListWaterGrid(gomock.Any(), gomock.Any()).Return([]sqlc.ListWaterGridRow{
					gridRow(testUnitA, "A1", true), gridRow(testUnitB, "A2", true), gridRow(uuid.New(), "A3", false),
				}, nil)
			}, http.StatusCreated, `"total": "1500.00"`},
		{"second run for the period is refused", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				q.EXPECT().ListWaterReadingsForRun(gomock.Any(), gomock.Any()).Return([]sqlc.ListWaterReadingsForRunRow{draft(testUnitA, "A1", "10.00")}, nil)
				q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).
					Return(uuid.Nil, &pgconn.PgError{Code: "23505", ConstraintName: "transaction_headers_idempotency_key_key"})
			}, http.StatusConflict, "already generated"},
		{"nothing drafted", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				q.EXPECT().ListWaterReadingsForRun(gomock.Any(), gomock.Any()).Return(nil, nil)
			}, http.StatusUnprocessableEntity, "nothing to generate"},
		{"already locked readings are not billed twice", target,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
				locked := draft(testUnitA, "A1", "10.00")
				locked.Locked = true
				q.EXPECT().ListWaterReadingsForRun(gomock.Any(), gomock.Any()).Return([]sqlc.ListWaterReadingsForRunRow{locked}, nil)
			}, http.StatusUnprocessableEntity, "nothing to generate"},
		{"bad period", "/", func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, "YYYY-MM"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsManager(app.generateWaterChargesHandler, http.MethodPost, tc.target, "", propParams())
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Errorf("body %s missing %q", w.Body, tc.wantBody)
			}
		})
	}
}

func TestShowWaterInvoicesPDF(t *testing.T) {
	invoiceRow := sqlc.ListWaterInvoicesRow{
		UnitID: testUnitA, UnitCode: "A1", TenantName: "JOHN KAMAU", Period: sep2026,
		ReadingDate: sep2026.AddDate(0, 0, 27), PreviousReading: money("100"), CurrentReading: money("110"),
		UnitsConsumed: money("10"), Rate: money("150"), Amount: money("1500"), PriorBalance: money("3600"),
	}
	setupHappy := func(q *mock.MockQuerier, rows []sqlc.ListWaterInvoicesRow) {
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
		q.EXPECT().GetLandlord(gomock.Any(), gomock.Any()).Return(testLandlordRowWithBank(), nil)
		q.EXPECT().ListWaterInvoices(gomock.Any(), gomock.Any()).Return(rows, nil)
	}
	periodParam := gin.Param{Key: "period", Value: "2026-09.pdf"}

	t.Run("renders a PDF with the carried-forward balance", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setupHappy(q, []sqlc.ListWaterInvoicesRow{invoiceRow})
		w := serveAsManager(app.showWaterInvoicesPDFHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/pdf" {
			t.Fatalf("status %d type %q: %s", w.Code, w.Header().Get("Content-Type"), w.Body)
		}
		if !strings.HasPrefix(w.Body.String(), "%PDF") {
			t.Fatal("body is not a PDF")
		}
		for _, want := range []string{"JOHN KAMAU", "3,600.00", "5,100.00"} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("PDF missing %q", want)
			}
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("Cache-Control %q must forbid caching", cc)
		}
	})
	t.Run("nothing billed is a 404", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		setupHappy(q, nil)
		w := serveAsManager(app.showWaterInvoicesPDFHandler, http.MethodGet, "/", "", propParams(periodParam))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", w.Code)
		}
	})
	t.Run("malformed period is a 404", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		w := serveAsManager(app.showWaterInvoicesPDFHandler, http.MethodGet, "/", "", propParams(gin.Param{Key: "period", Value: "nonsense.pdf"}))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404", w.Code)
		}
	})
}
