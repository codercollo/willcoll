package main

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/mock/gomock"
)

func TestLeasePayerHandlers(t *testing.T) {
	leaseID := uuid.New()
	params := gin.Params{{Key: "id", Value: leaseID.String()}}
	lease := sqlc.Lease{ID: leaseID, PrimaryPhone: "+254722000001"}

	t.Run("adds a co-payer", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetLease(gomock.Any(), gomock.Any()).Return(lease, nil)
		q.EXPECT().ListLeasePayers(gomock.Any(), gomock.Any()).Return(nil, nil)
		q.EXPECT().CreateLeasePayer(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateLeasePayerParams) (sqlc.LeasePayer, error) {
				if a.Name != "MARY WANJIKU" || a.Phone == nil || *a.Phone != "+254733000001" {
					t.Errorf("payer = %+v (the name is trimmed)", a)
				}
				return sqlc.LeasePayer{ID: uuid.New(), Name: a.Name, Phone: a.Phone}, nil
			})
		w := serveAsTenant(app.addLeasePayerHandler, http.MethodPost, "/", `{"name":"  MARY WANJIKU ","phone":"+254733000001"}`, params)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "MARY WANJIKU") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("a repeated phone in another spelling is refused", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetLease(gomock.Any(), gomock.Any()).Return(lease, nil)
		q.EXPECT().ListLeasePayers(gomock.Any(), gomock.Any()).Return(nil, nil)
		w := serveAsTenant(app.addLeasePayerHandler, http.MethodPost, "/", `{"name":"X","phone":"+254722000001"}`, params)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "already on this lease") {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("validation runs before any query", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		for _, body := range []string{`{"name":""}`, `{"name":"X","phone":"not-a-phone"}`, `{"phone":"+254700000000"}`} {
			if w := serveAsTenant(app.addLeasePayerHandler, http.MethodPost, "/", body, params); w.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s: status %d", body, w.Code)
			}
		}
	})

	t.Run("an unknown lease is a 404", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetLease(gomock.Any(), gomock.Any()).Return(sqlc.Lease{}, sql.ErrNoRows)
		if w := serveAsTenant(app.addLeasePayerHandler, http.MethodPost, "/", `{"name":"X"}`, params); w.Code != http.StatusNotFound {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("removes a co-payer, and 404s on one that is not there", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		payerID := uuid.New()
		p := gin.Params{{Key: "id", Value: leaseID.String()}, {Key: "payerId", Value: payerID.String()}}
		gomock.InOrder(
			q.EXPECT().DeleteLeasePayer(gomock.Any(), gomock.Any()).Return(int64(1), nil),
			q.EXPECT().DeleteLeasePayer(gomock.Any(), gomock.Any()).Return(int64(0), nil),
		)
		if w := serveAsTenant(app.removeLeasePayerHandler, http.MethodDelete, "/", "", p); w.Code != http.StatusOK {
			t.Errorf("first removal: %d", w.Code)
		}
		if w := serveAsTenant(app.removeLeasePayerHandler, http.MethodDelete, "/", "", p); w.Code != http.StatusNotFound {
			t.Errorf("second removal: %d, want 404", w.Code)
		}
		bad := gin.Params{{Key: "id", Value: leaseID.String()}, {Key: "payerId", Value: "nope"}}
		if w := serveAsTenant(app.removeLeasePayerHandler, http.MethodDelete, "/", "", bad); w.Code != http.StatusNotFound {
			t.Errorf("malformed id: %d", w.Code)
		}
	})

	t.Run("the unit lease history", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{}, nil)
		q.EXPECT().ListLeasesForUnit(gomock.Any(), gomock.Any()).Return([]sqlc.Lease{{ID: leaseID, TenantName: "JOHN"}}, nil)
		q.EXPECT().ListLeasePayers(gomock.Any(), gomock.Any()).Return(nil, nil)
		w := serveAsTenant(app.listUnitLeasesHandler, http.MethodGet, "/", "", gin.Params{{Key: "id", Value: uuid.NewString()}})
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"payers": []`) {
			t.Errorf("status %d: %s (payers must be an empty array, never null)", w.Code, w.Body)
		}
	})
}
