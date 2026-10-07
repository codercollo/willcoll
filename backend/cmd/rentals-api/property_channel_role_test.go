package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/rentals/db/sqlc"
	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"
)

// The payment channel decides where tenants' money lands, so it is owner-only
// wherever it can be set: not just POST /properties/:id/payment-channel but
// also the channel field on POST/PATCH /properties, which are accountant-level
// for everything else (system-design.txt A.1).

func serveAsStaff(handler gin.HandlerFunc, role, method, body string, params gin.Params) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Params = params
	contextSetTenantID(c, testTenantID)
	contextSetStaff(c, &platformdata.Staff{Role: role})
	handler(c)
	return w
}

func TestCreatePropertyChannelIsOwnerOnly(t *testing.T) {
	body := `{"landlord_id":"` + testLandlordID.String() + `","name":"Runda Arcade","location":"Runda","slug":"runda-arcade","payhero_channel_id":"1234"}`
	for _, role := range []string{platformdata.RoleViewer, platformdata.RoleOfficer, platformdata.RoleAccountant} {
		t.Run(role, func(t *testing.T) {
			app, _ := newPropertyTestApp(t) // no querier expectations: a refusal must not reach the database
			w := serveAsStaff(app.createPropertyHandler, role, http.MethodPost, body, nil)
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body)
			}
		})
	}
}

func TestUpdatePropertyChannelIsOwnerOnly(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	t.Run("accountant changing the channel is refused before any save", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(testPropertyRow(), nil)

		w := serveAsStaff(app.updatePropertyHandler, platformdata.RoleAccountant, http.MethodPatch, `{"payhero_channel_id":"1234"}`, params)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body)
		}
	})

	t.Run("accountant clearing the channel is refused too", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		row := testPropertyRow()
		channel := "1234"
		row.PayheroChannelID = &channel
		q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)

		w := serveAsStaff(app.updatePropertyHandler, platformdata.RoleAccountant, http.MethodPatch, `{"payhero_channel_id":""}`, params)
		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403; body: %s", w.Code, w.Body)
		}
	})

	// Past the guard the save runs; the stubbed version conflict (409) proves it got there.
	for name, body := range map[string]string{
		"accountant editing something else":        `{"name":"Kiwi Place"}`,
		"accountant echoing the unchanged channel": `{"name":"Kiwi Place","payhero_channel_id":"1234"}`,
	} {
		t.Run(name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			row := testPropertyRow()
			channel := "1234"
			row.PayheroChannelID = &channel
			q.EXPECT().GetProperty(gomock.Any(), gomock.Any()).Return(row, nil)
			q.EXPECT().LandlordBelongsToTenant(gomock.Any(), gomock.Any()).Return(true, nil)
			q.EXPECT().UpdateProperty(gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, sqlc.UpdatePropertyParams) (int32, error) { return 0, sql.ErrNoRows })

			w := serveAsStaff(app.updatePropertyHandler, platformdata.RoleAccountant, http.MethodPatch, body, params)
			if w.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409 (past the guard); body: %s", w.Code, w.Body)
			}
		})
	}
}
