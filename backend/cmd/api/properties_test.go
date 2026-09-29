package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
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
	testTenantID   = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	testLandlordID = uuid.MustParse("00000000-0000-0000-0000-0000000000aa")
	testPropertyID = uuid.MustParse("00000000-0000-0000-0000-0000000000bb")
)

// newPropertyTestApp returns an app whose Properties model runs against a
// mock Store, and the mock Querier that each tenant transaction hands to
// the model.
func newPropertyTestApp(t *testing.T) (*application, *mock.MockQuerier) {
	t.Helper()

	ctrl := gomock.NewController(t)
	store := mock.NewMockStore(ctrl)
	querier := mock.NewMockQuerier(ctrl)

	store.EXPECT().
		ExecTenantTx(gomock.Any(), testTenantID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, fn func(sqlc.Querier) error) error {
			return fn(querier)
		}).
		AnyTimes()
	store.EXPECT().
		ExecTenantTxExclusive(gomock.Any(), testTenantID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uuid.UUID, _ string, fn func(sqlc.Querier) error) error {
			return fn(querier)
		}).
		AnyTimes()

	app := &application{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		models: data.NewModelsFromStore(store, 0),
	}
	return app, querier
}

// serveAsTenant runs handler with the test tenant already authenticated.
func serveAsTenant(handler gin.HandlerFunc, method, target, body string, params gin.Params) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Params = params
	contextSetTenantID(c, testTenantID)
	handler(c)
	return w
}

func testPropertyRow() sqlc.Property {
	return sqlc.Property{
		ID:                   testPropertyID,
		TenantID:             testTenantID,
		LandlordID:           testLandlordID,
		Name:                 "Runda Arcade",
		Location:             "Runda",
		Slug:                 "runda-arcade",
		GarbageEnabled:       true,
		GarbageFee:           moneyfmt.FromCents(30000),
		WaterRatePerUnit:     moneyfmt.FromCents(15000),
		ManagementFeePercent: 5,
		CreatedAt:            time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		Version:              1,
	}
}

// expectReceiptIssuance mocks the side effects of issueReceipt (called once
// per allocation inside postPaymentAllocations): the property lookup, the
// idempotency check (no existing receipt), the counter lock/advance, and
// the receipt insert. Every payment-posting test now triggers this, so it
// is shared rather than repeated per test file.
func expectReceiptIssuance(q *mock.MockQuerier, unitID uuid.UUID) {
	q.EXPECT().GetUnitLedgerBalance(gomock.Any(), gomock.Any()).Return(moneyfmt.Money{}, nil).AnyTimes()
	q.EXPECT().GetUnitLedgerBalanceAsOf(gomock.Any(), gomock.Any()).Return(moneyfmt.Money{}, nil).AnyTimes()
	q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{ID: unitID, PropertyID: testPropertyID}, nil).AnyTimes()
	q.EXPECT().GetReceiptByAllocation(gomock.Any(), gomock.Any()).Return(sqlc.Receipt{}, sql.ErrNoRows).AnyTimes()
	q.EXPECT().LockReceiptCounter(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	q.EXPECT().AdvanceReceiptCounter(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	q.EXPECT().CreateReceipt(gomock.Any(), gomock.Any()).Return(sqlc.Receipt{}, nil).AnyTimes()
}

// testLandlordRowWithBank is testLandlordRow's bill-tests counterpart: bank
// details set, so payment particulars aren't blocked as missing. Tests for
// the "not linked to PayHero"/blank-particulars cases use a bare Landlord{}
// instead.
func testLandlordRowWithBank() sqlc.Landlord {
	name, number, bank := "Runda Arcade", "0123456789", "KCB"
	return sqlc.Landlord{
		ID: testLandlordID, Phone: "+254700000000",
		BankAccountName: &name, BankAccountNumber: &number, BankName: &bank,
	}
}

const validPropertyBody = `{
	"landlord_id": "00000000-0000-0000-0000-0000000000aa",
	"name": "Runda Arcade",
	"location": "Runda",
	"slug": "runda-arcade",
	"garbage_enabled": true,
	"garbage_fee": "300.00",
	"water_rate_per_unit": "150.00",
	"management_fee_percent": 5
}`

func TestCreatePropertyHandler(t *testing.T) {
	landlordOK := sqlc.LandlordBelongsToTenantParams{TenantID: testTenantID, ID: testLandlordID}

	tests := []struct {
		name       string
		body       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{
			name: "created",
			body: validPropertyBody,
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().LandlordBelongsToTenant(gomock.Any(), landlordOK).Return(true, nil)
				q.EXPECT().CreateProperty(gomock.Any(), sqlc.CreatePropertyParams{
					TenantID:             testTenantID,
					LandlordID:           testLandlordID,
					Name:                 "Runda Arcade",
					Location:             "Runda",
					Slug:                 "runda-arcade",
					GarbageEnabled:       true,
					GarbageFee:           moneyfmt.FromCents(30000),
					WaterRatePerUnit:     moneyfmt.FromCents(15000),
					ManagementFeePercent: 5,
				}).Return(testPropertyRow(), nil)
				q.EXPECT().CreateReceiptCounter(gomock.Any(), sqlc.CreateReceiptCounterParams{
					TenantID: testTenantID, PropertyID: testPropertyID,
				}).Return(nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   `"garbage_fee": "300.00"`,
		},
		{
			name: "another tenant's landlord",
			body: validPropertyBody,
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().LandlordBelongsToTenant(gomock.Any(), landlordOK).Return(false, nil)
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   `"landlord_id": "must be one of your landlords"`,
		},
		{
			name: "slug taken",
			body: validPropertyBody,
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().LandlordBelongsToTenant(gomock.Any(), landlordOK).Return(true, nil)
				q.EXPECT().CreateProperty(gomock.Any(), gomock.Any()).
					Return(sqlc.Property{}, &pgconn.PgError{Code: "23505", ConstraintName: "properties_slug_key"})
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   `"slug": "is already in use"`,
		},
		{
			name:       "invalid input never reaches the database",
			body:       `{"name": "", "slug": "Not A Slug", "garbage_fee": "-1"}`,
			setup:      func(q *mock.MockQuerier) {},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   `"slug": "must contain only lowercase letters, digits and single hyphens"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tt.setup(q)

			w := serveAsTenant(app.createPropertyHandler, http.MethodPost, "/v1/properties", tt.body, nil)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body %s does not contain %s", w.Body, tt.wantBody)
			}
		})
	}
}

func TestShowPropertyHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}
	getArgs := sqlc.GetPropertyParams{TenantID: testTenantID, ID: testPropertyID}

	t.Run("found", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), getArgs).Return(testPropertyRow(), nil)
		q.EXPECT().ListPropertySummaries(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()

		w := serveAsTenant(app.showPropertyHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; body: %s", w.Code, w.Body)
		}

		var got struct {
			Property map[string]any `json:"property"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Property["slug"] != "runda-arcade" || got.Property["water_rate_per_unit"] != "150.00" {
			t.Errorf("unexpected property: %v", got.Property)
		}
		if _, leaked := got.Property["tenant_id"]; leaked {
			t.Error("tenant_id must not be serialized")
		}
	})

	t.Run("not found", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), getArgs).Return(sqlc.Property{}, sql.ErrNoRows)

		w := serveAsTenant(app.showPropertyHandler, http.MethodGet, "/", "", params)
		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", w.Code)
		}
	})
}

func TestUpdatePropertyHandlerEditConflict(t *testing.T) {
	app, q := newPropertyTestApp(t)
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
	q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
	q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
			if arg.Name != "Kiwi Place" || arg.Version != 1 {
				t.Errorf("unexpected update args: %+v", arg)
			}
			return 0, sql.ErrNoRows // version moved on
		})

	w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", `{"name": "Kiwi Place"}`, params)
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409; body: %s", w.Code, w.Body)
	}
}

func TestDeletePropertyHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}
	leaseArgs := sqlc.PropertyHasActiveLeasesParams{TenantID: testTenantID, PropertyID: testPropertyID}
	archiveArgs := sqlc.ArchivePropertyParams{TenantID: testTenantID, ID: testPropertyID}

	tests := []struct {
		name       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{
			name: "archived",
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().PropertyHasActiveLeases(gomock.Any(), leaseArgs).Return(false, nil)
				q.EXPECT().ArchiveProperty(gomock.Any(), archiveArgs).Return(int64(1), nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `"message": "property successfully deleted"`,
		},
		{
			name: "missing or already archived",
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().PropertyHasActiveLeases(gomock.Any(), leaseArgs).Return(false, nil)
				q.EXPECT().ArchiveProperty(gomock.Any(), archiveArgs).Return(int64(0), nil)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "active leases block archiving",
			setup: func(q *mock.MockQuerier) {
				q.EXPECT().PropertyHasActiveLeases(gomock.Any(), leaseArgs).Return(true, nil)
				// ArchiveProperty must not be called.
			},
			wantStatus: http.StatusConflict,
			wantBody:   "still has active leases",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tt.setup(q)

			w := serveAsTenant(app.deletePropertyHandler, http.MethodDelete, "/", "", params)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body %s does not contain %s", w.Body, tt.wantBody)
			}
		})
	}
}

// TestUpdatePropertyPartialAmounts pins down PATCH semantics for an amount
// field (appendix 21.4): not sending it, or sending null, leaves the stored
// value alone; "0" is a deliberate zero; bad or negative amounts are refused
// before anything is written. The stored rate is 150.00.
func TestUpdatePropertyPartialAmounts(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}
	stored := testPropertyRow().WaterRatePerUnit

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantRate   moneyfmt.Money // what UpdateProperty must be given, when it is called
	}{
		{"omitted leaves it unchanged", `{"name":"Renamed"}`, http.StatusOK, stored},
		{"null leaves it unchanged", `{"water_rate_per_unit":null}`, http.StatusOK, stored},
		{"zero string is a real zero", `{"water_rate_per_unit":"0"}`, http.StatusOK, moneyfmt.FromCents(0)},
		{"zero number is a real zero", `{"water_rate_per_unit":0.00}`, http.StatusOK, moneyfmt.FromCents(0)},
		{"a number is read exactly", `{"water_rate_per_unit":175.10}`, http.StatusOK, moneyfmt.FromCents(17510)},
		{"negative is refused", `{"water_rate_per_unit":"-1"}`, http.StatusUnprocessableEntity, stored},
		{"sub-cent is refused", `{"water_rate_per_unit":"1.005"}`, http.StatusBadRequest, stored},
		{"not a number is refused", `{"water_rate_per_unit":"lots"}`, http.StatusBadRequest, stored},
		{"empty string is refused, not read as zero", `{"water_rate_per_unit":""}`, http.StatusBadRequest, stored},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
			if tc.wantStatus == http.StatusOK {
				q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
				q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
					DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
						if arg.WaterRatePerUnit != tc.wantRate {
							t.Errorf("stored rate = %s, want %s", arg.WaterRatePerUnit, tc.wantRate)
						}
						return 2, nil
					})
			}
			w := serveAsTenant(app.updatePropertyHandler, http.MethodPatch, "/", tc.body, params)
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
		})
	}
}

// TestPropertySummaries: the Properties card grid gets the landlord,
// occupancy, and rent collected against expected in the same response, for the
// asked-for month.
func TestPropertySummaries(t *testing.T) {
	setup := func(t *testing.T) (*application, *mock.MockQuerier) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().ListProperties(gomock.Any(), gomock.Any()).Return([]sqlc.ListPropertiesRow{
			{TotalRecords: 1, Property: testPropertyRow()},
		}, nil)
		return app, q
	}

	t.Run("list embeds the summary for ?period=", func(t *testing.T) {
		app, q := setup(t)
		q.EXPECT().ListPropertySummaries(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, a sqlc.ListPropertySummariesParams) ([]sqlc.ListPropertySummariesRow, error) {
				if !a.Period.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
					t.Errorf("summary period = %s", a.Period)
				}
				return []sqlc.ListPropertySummariesRow{{
					PropertyID: testPropertyID, LandlordName: "Wanjiku Estates", Occupied: 9, Vacant: 3,
					Expected: moneyfmt.FromCents(6000000), Collected: moneyfmt.FromCents(4500000),
				}}, nil
			})
		w := serveAsTenant(app.listPropertiesHandler, http.MethodGet, "/?period=2026-09", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		for _, want := range []string{`"landlord_name": "Wanjiku Estates"`, `"units_occupied": 9`, `"units_vacant": 3`,
			`"rent_expected": "60000.00"`, `"rent_collected": "45000.00"`, `"period": "2026-09"`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Errorf("response missing %s: %s", want, w.Body)
			}
		}
	})

	t.Run("a bad period is a 422 and nothing is queried for it", func(t *testing.T) {
		app, q := setup(t)
		_ = q
		w := serveAsTenant(app.listPropertiesHandler, http.MethodGet, "/?period=next-month", "", nil)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})
}
