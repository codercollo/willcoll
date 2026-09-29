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

func TestAdminPlans(t *testing.T) {
	planID := uuid.New()
	params := gin.Params{{Key: "id", Value: planID.String()}}
	existing := sqlc.SubscriptionPlan{ID: planID, Name: "Standard", Price: money("2500"), BillingInterval: "monthly"}

	t.Run("creates a plan", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().AdminCreateSubscriptionPlan(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.AdminCreateSubscriptionPlanParams) (sqlc.SubscriptionPlan, error) {
				if a.Name != "Starter" || a.Price != money("1500") || a.BillingInterval != "monthly" || !a.UnitCap.Valid || a.UnitCap.Int32 != 10 {
					t.Errorf("create = %+v", a)
				}
				return sqlc.SubscriptionPlan{ID: planID, Name: a.Name, Price: a.Price, BillingInterval: a.BillingInterval, UnitCap: a.UnitCap}, nil
			})
		w := do(p.app.adminCreatePlanHandler, http.MethodPost, "/", `{"name":" Starter ","price":"1500","billing_interval":"Monthly","unit_cap":10}`, nil, nil)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), "Starter") {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	rejected := map[string]struct{ body, want string }{
		"a price with cents":  {`{"name":"A","price":"1500.50","billing_interval":"monthly"}`, "whole number of shillings"},
		"a zero price":        {`{"name":"A","price":"0","billing_interval":"monthly"}`, "greater than zero"},
		"an unknown interval": {`{"name":"A","price":"100","billing_interval":"fortnightly"}`, "one of weekly"},
		"no name":             {`{"price":"100","billing_interval":"monthly"}`, "name"},
		"a zero unit cap":     {`{"name":"A","price":"100","billing_interval":"monthly","unit_cap":0}`, "unit_cap"},
	}
	for name, tc := range rejected {
		t.Run("refuses "+name, func(t *testing.T) {
			p := newPayTestApp(t) // no store call is expected
			w := do(p.app.adminCreatePlanHandler, http.MethodPost, "/", tc.body, nil, nil)
			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
		})
	}

	t.Run("a partial update keeps the other fields", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(existing, nil)
		p.store.EXPECT().AdminUpdateSubscriptionPlan(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.AdminUpdateSubscriptionPlanParams) (sqlc.SubscriptionPlan, error) {
				if a.Name != "Standard" || a.Price != money("3000") || a.BillingInterval != "monthly" {
					t.Errorf("update = %+v (only the price changes)", a)
				}
				return sqlc.SubscriptionPlan{ID: planID, Name: a.Name, Price: a.Price, BillingInterval: a.BillingInterval}, nil
			})
		w := do(p.app.adminUpdatePlanHandler, http.MethodPatch, "/", `{"price":"3000"}`, params, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("an unknown plan is a 404", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(sqlc.SubscriptionPlan{}, sql.ErrNoRows)
		if w := do(p.app.adminUpdatePlanHandler, http.MethodPatch, "/", `{"price":"3000"}`, params, nil); w.Code != http.StatusNotFound {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("an update cannot make a plan invalid", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(existing, nil)
		if w := do(p.app.adminUpdatePlanHandler, http.MethodPatch, "/", `{"price":"10.5"}`, params, nil); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("a test plan can be created in development", func(t *testing.T) {
		p := newPayTestApp(t) // development, per newPayTestApp
		p.store.EXPECT().AdminCreateSubscriptionPlan(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.AdminCreateSubscriptionPlanParams) (sqlc.SubscriptionPlan, error) {
				if !a.IsTest {
					t.Errorf("is_test not carried through: %+v", a)
				}
				return sqlc.SubscriptionPlan{ID: planID, Name: a.Name, Price: a.Price, BillingInterval: a.BillingInterval, IsTest: a.IsTest}, nil
			})
		w := do(p.app.adminCreatePlanHandler, http.MethodPost, "/", `{"name":"TEST","price":"10","billing_interval":"monthly","is_test":true}`, nil, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	for _, env := range []string{"staging", "production", ""} {
		t.Run("a test plan cannot be created outside development ("+env+")", func(t *testing.T) {
			p := newPayTestApp(t) // no store call is expected
			p.app.config.env = env
			w := do(p.app.adminCreatePlanHandler, http.MethodPost, "/", `{"name":"TEST","price":"10","billing_interval":"monthly","is_test":true}`, nil, nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
		})

		t.Run("a plan cannot be edited into a test plan outside development ("+env+")", func(t *testing.T) {
			p := newPayTestApp(t)
			p.app.config.env = env
			p.store.EXPECT().GetSubscriptionPlan(gomock.Any(), planID).Return(existing, nil)
			w := do(p.app.adminUpdatePlanHandler, http.MethodPatch, "/", `{"is_test":true}`, params, nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("status %d: %s", w.Code, w.Body)
			}
		})
	}

	t.Run("the list shows subscriber counts", func(t *testing.T) {
		p := newPayTestApp(t)
		p.store.EXPECT().AdminListSubscriptionPlans(gomock.Any()).Return([]sqlc.AdminListSubscriptionPlansRow{
			{ID: planID, Name: "Standard", Price: money("2500"), BillingInterval: "monthly", Subscribers: 4},
		}, nil)
		w := do(p.app.adminListPlansHandler, http.MethodGet, "/", "", nil, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"subscribers": 4`) {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})
}
