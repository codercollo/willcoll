package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Bad list parameters are rejected with 422 before any query runs: the
// mock Querier has no expectations, so a database call would fail the test.
func TestListQueryParamValidation(t *testing.T) {
	propertyParams := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	tests := []struct {
		name     string
		handler  func(app *application) gin.HandlerFunc
		target   string
		params   gin.Params
		wantBody string
	}{
		{"bad landlord_id", func(a *application) gin.HandlerFunc { return a.listPropertiesHandler },
			"/v1/properties?landlord_id=nope", nil, `"landlord_id": "must be a valid UUID"`},
		{"property sort not on safelist", func(a *application) gin.HandlerFunc { return a.listPropertiesHandler },
			"/v1/properties?sort=slug", nil, `"sort": "invalid sort value"`},
		{"bad unit status", func(a *application) gin.HandlerFunc { return a.listUnitsForPropertyHandler },
			"/v1/properties/x/units?status=demolished", propertyParams, `"status": "must be vacant or occupied"`},
		{"ledger sort not on safelist", func(a *application) gin.HandlerFunc { return a.showUnitLedgerHandler },
			"/v1/units/x/ledger/rent?sort=amount", gin.Params{{Key: "id", Value: testPropertyID.String()}, {Key: "type", Value: "rent"}},
			`"sort": "invalid sort value"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _ := newPropertyTestApp(t)
			w := serveAsTenant(tt.handler(app), http.MethodGet, tt.target, "", tt.params)

			if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), tt.wantBody) {
				t.Errorf("status = %d body = %s; want 422 containing %s", w.Code, w.Body, tt.wantBody)
			}
		})
	}
}
