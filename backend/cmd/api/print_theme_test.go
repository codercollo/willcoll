package main

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/mock"
	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"
)

func TestUpdatePropertyPrintThemeHandler(t *testing.T) {
	get := sqlc.GetPropertyParams{TenantID: testTenantID, ID: testPropertyID}
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}

	tests := []struct {
		name       string
		body       string
		setup      func(q *mock.MockQuerier)
		wantStatus int
		wantBody   string
	}{
		{"saved", `{"print_theme":{"header_text":"KIWI PLACE","accent_color":"#1F4E79"}}`,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), get).Return(testPropertyRow(), nil)
				q.EXPECT().SetPropertyPrintTheme(gomock.Any(), gomock.Any()).Return(int32(2), nil)
			}, http.StatusOK, `"header_text": "KIWI PLACE"`},
		{"null clears", `{"print_theme":null}`,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), get).Return(testPropertyRow(), nil)
				q.EXPECT().SetPropertyPrintTheme(gomock.Any(), sqlc.SetPropertyPrintThemeParams{
					TenantID: testTenantID, ID: testPropertyID, Version: 1, PrintTheme: nil,
				}).Return(int32(2), nil)
			}, http.StatusOK, `"slug"`},
		{"bad accent rejected before any query", `{"print_theme":{"accent_color":"navy"}}`,
			func(*mock.MockQuerier) {}, http.StatusUnprocessableEntity, `"accent_color"`},
		{"unknown property", `{"print_theme":{}}`,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), get).Return(sqlc.Property{}, sql.ErrNoRows)
			}, http.StatusNotFound, ""},
		{"version moved on", `{"print_theme":{}}`,
			func(q *mock.MockQuerier) {
				q.EXPECT().GetProperty(gomock.Any(), get).Return(testPropertyRow(), nil)
				q.EXPECT().SetPropertyPrintTheme(gomock.Any(), gomock.Any()).Return(int32(0), sql.ErrNoRows)
			}, http.StatusConflict, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, q := newPropertyTestApp(t)
			tc.setup(q)
			w := serveAsTenant(app.updatePropertyPrintThemeHandler, http.MethodPut, "/", tc.body, params)
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantStatus, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Errorf("body %s missing %q", w.Body, tc.wantBody)
			}
		})
	}
}
