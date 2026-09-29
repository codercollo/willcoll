package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"
)

// A unit's status follows its lease; PATCH /v1/units/:id refuses to set it.
func TestUpdateUnitHandlerRejectsStatus(t *testing.T) {
	app, q := newPropertyTestApp(t)
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	q.EXPECT().GetUnit(gomock.Any(), gomock.Any()).Return(sqlc.Unit{
		ID: testPropertyID, TenantID: testTenantID, UnitCode: "3C", Status: data.UnitStatusOccupied, Version: 1,
	}, nil)
	// No UpdateUnit expectation: the request must fail before any write.

	w := serveAsTenant(app.updateUnitHandler, http.MethodPatch, "/", `{"status": "vacant"}`, params)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `unknown key \"status\"`) {
		t.Errorf("status = %d body = %s; want 400 unknown key status", w.Code, w.Body)
	}
}
