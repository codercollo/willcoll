package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestUpdatePropertyElectricityHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	// testPropertyRow has electricity disabled at 0.00 (see properties_test.go).
	expectUpdate := func(q *mock.MockQuerier, wantEnabled bool, wantAmount moneyfmt.Money) {
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
				if arg.ElectricityEnabled != wantEnabled || arg.ElectricityDepositAmount != wantAmount {
					t.Errorf("saved enabled=%v amount=%s, want enabled=%v amount=%s",
						arg.ElectricityEnabled, arg.ElectricityDepositAmount, wantEnabled, wantAmount)
				}
				return arg.Version + 1, nil
			})
	}

	tests := []struct {
		name       string
		body       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{
			name: "enabling charges every active lease without a deposit yet",
			body: `{"enabled": true, "deposit_amount": "2000.00"}`,
			setup: func(q *mock.MockQuerier) {
				expectUpdate(q, true, moneyfmt.FromCents(200000))
				lease := sqlc.Lease{ID: uuid.New(), UnitID: testUnitA, TenantID: testTenantID, Status: "active"}
				q.EXPECT().ListActiveLeasesWithoutElectricityDeposit(gomock.Any(), gomock.Any()).Return([]sqlc.Lease{lease}, nil)
				q.EXPECT().SetLeaseElectricityDeposit(gomock.Any(), gomock.Any()).Return(nil)
				q.EXPECT().CreateTransactionHeader(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
				q.EXPECT().GetOrCreateLedgerAccount(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
				q.EXPECT().CreateCharge(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
				q.EXPECT().CreateLedgerEntry(gomock.Any(), gomock.Any()).Return(uuid.New(), nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `"leases_charged": 1`,
		},
		{
			name:       "disabling never charges anything",
			body:       `{"enabled": false}`,
			setup:      func(q *mock.MockQuerier) { expectUpdate(q, false, moneyfmt.Money{}) },
			wantStatus: http.StatusOK,
			wantBody:   `"leases_charged": 0`,
		},
		{
			name:       "enabled with a zero deposit is rejected",
			body:       `{"enabled": true}`,
			setup:      func(q *mock.MockQuerier) {},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   "must be greater than zero while electricity is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
			tt.setup(q)

			w := serveAsManager(app.updatePropertyElectricityHandler, http.MethodPatch, "/", tt.body, params)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body %s does not contain %s", w.Body, tt.wantBody)
			}
		})
	}
}
