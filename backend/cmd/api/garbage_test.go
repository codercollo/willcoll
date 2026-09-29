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
	"go.uber.org/mock/gomock"
)

func TestUpdatePropertyGarbageHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	// testPropertyRow has garbage enabled at 300.00.
	expectUpdate := func(q *mock.MockQuerier, wantEnabled bool, wantFee moneyfmt.Money) {
		q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, arg sqlc.UpdatePropertyParams) (int32, error) {
				if arg.GarbageEnabled != wantEnabled || arg.GarbageFee != wantFee {
					t.Errorf("saved enabled=%v fee=%s, want enabled=%v fee=%s",
						arg.GarbageEnabled, arg.GarbageFee, wantEnabled, wantFee)
				}
				if arg.Name != "Runda Arcade" || arg.WaterRatePerUnit != moneyfmt.FromCents(15000) {
					t.Errorf("unrelated fields changed: %+v", arg)
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
			name:       "fee only leaves billing enabled",
			body:       `{"fee": "350.00"}`,
			setup:      func(q *mock.MockQuerier) { expectUpdate(q, true, moneyfmt.FromCents(35000)) },
			wantStatus: http.StatusOK,
			wantBody:   `"garbage_fee": "350.00"`,
		},
		{
			name:       "disable only keeps the fee",
			body:       `{"enabled": false}`,
			setup:      func(q *mock.MockQuerier) { expectUpdate(q, false, moneyfmt.FromCents(30000)) },
			wantStatus: http.StatusOK,
			wantBody:   `"garbage_enabled": false`,
		},
		{
			name:       "empty body changes nothing",
			body:       `{}`,
			setup:      func(q *mock.MockQuerier) { expectUpdate(q, true, moneyfmt.FromCents(30000)) },
			wantStatus: http.StatusOK,
		},
		{
			name:       "enabled with a zero fee is rejected",
			body:       `{"fee": "0"}`,
			setup:      func(q *mock.MockQuerier) {},
			wantStatus: http.StatusUnprocessableEntity,
			wantBody:   "must be greater than zero while garbage billing is enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)
			tt.setup(q)

			w := serveAsTenant(app.updatePropertyGarbageHandler, http.MethodPatch, "/", tt.body, params)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", w.Code, tt.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("body %s does not contain %s", w.Body, tt.wantBody)
			}
		})
	}
}
