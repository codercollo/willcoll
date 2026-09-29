package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/gin-gonic/gin"
	"go.uber.org/mock/gomock"
)

func TestParseUnitsCSV(t *testing.T) {
	t.Run("reads units in any column order, trims, skips blank lines, ignores a BOM", func(t *testing.T) {
		rows, problems := parseUnitsCSV([]byte("\xef\xbb\xbfMeter_Number, Unit_Code\nM-1, G1 \n\n,SHOP NO.2\n"))
		if len(problems) != 0 || len(rows) != 2 {
			t.Fatalf("rows %+v problems %+v", rows, problems)
		}
		if rows[0].UnitCode != "G1" || rows[0].MeterNumber == nil || *rows[0].MeterNumber != "M-1" || rows[0].Row != 2 {
			t.Errorf("row 0 = %+v", rows[0])
		}
		if rows[1].UnitCode != "SHOP NO.2" || rows[1].MeterNumber != nil || rows[1].Row != 4 {
			t.Errorf("row 1 = %+v (the row number must count the blank line)", rows[1])
		}
	})

	bad := map[string]struct {
		csv      string
		wantRows []int
		wantText string
	}{
		"no unit_code column":   {"meter_number\nM1\n", []int{1}, "must have a unit_code"},
		"an unknown column":     {"unit_code,floor\nG1,2\n", []int{1}, "not a known column"},
		"a repeated column":     {"unit_code,unit_code\nG1,G1\n", []int{1}, "appears twice"},
		"a blank code":          {"unit_code\nG1\n ,x\n", []int{3}, "must be provided"},
		"a duplicate in file":   {"unit_code\nG1\nG2\nG1\n", []int{4}, "repeats row 2"},
		"an over-long code":     {"unit_code\n" + strings.Repeat("x", 51) + "\n", []int{2}, "50 bytes"},
		"a header and no units": {"unit_code\n", []int{2}, "no units"},
	}
	for name, tc := range bad {
		t.Run(name, func(t *testing.T) {
			rows, problems := parseUnitsCSV([]byte(tc.csv))
			if rows != nil || len(problems) == 0 {
				t.Fatalf("accepted: rows %+v", rows)
			}
			got := problems[0]
			found := false
			for _, p := range problems {
				found = found || (p.Row == tc.wantRows[0] && strings.Contains(p.Message, tc.wantText))
			}
			if !found {
				t.Errorf("problems %+v, want row %v containing %q", problems, tc.wantRows, tc.wantText)
			}
			_ = got
		})
	}

	t.Run("reports every problem at once", func(t *testing.T) {
		_, problems := parseUnitsCSV([]byte("unit_code,meter_number\nG1,\n,x\nG1,\n" + strings.Repeat("y", 60) + ",\n"))
		if len(problems) < 3 {
			t.Errorf("problems = %+v, want the blank, the duplicate and the long code together", problems)
		}
	})

	t.Run("more than 500 units is refused", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("unit_code\n")
		for i := 0; i < 501; i++ {
			b.WriteString("U" + itoa(i) + "\n")
		}
		if _, problems := parseUnitsCSV([]byte(b.String())); len(problems) == 0 || !strings.Contains(problems[0].Message, "more than 500") {
			t.Errorf("problems = %+v", problems)
		}
	})
}

func TestImportUnitsHandler(t *testing.T) {
	params := gin.Params{{Key: "id", Value: testPropertyID.String()}}
	t.Run("a dry run writes nothing", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().PropertyIsActive(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().ListUnitCodes(gomock.Any(), gomock.Any()).Return([]string{"G9"}, nil)
		w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/?dry_run=true", "unit_code\nG1\nG2\n", params)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"valid_rows": 2`) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("imports every row, all vacant", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().PropertyIsActive(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().ListUnitCodes(gomock.Any(), gomock.Any()).Return(nil, nil)
		q.EXPECT().CreateUnit(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ any, a sqlc.CreateUnitParams) (sqlc.Unit, error) {
				if a.Status != "vacant" || a.PropertyID != testPropertyID {
					t.Errorf("unit = %+v", a)
				}
				return sqlc.Unit{UnitCode: a.UnitCode, Status: a.Status, PropertyID: a.PropertyID}, nil
			}).Times(2)
		w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/", "unit_code,meter_number\nG1,M1\nG2,\n", params)
		if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"imported": 2`) {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("clashes with existing units are listed by row and nothing is written", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().PropertyIsActive(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().ListUnitCodes(gomock.Any(), gomock.Any()).Return([]string{"G2", "G3"}, nil)
		w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/", "unit_code\nG1\nG2\nG3\n", params)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"row": 3`) || !strings.Contains(w.Body.String(), `"row": 4`) ||
			!strings.Contains(w.Body.String(), "already exists") {
			t.Errorf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("another manager's property is a 404", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().PropertyIsActive(gomock.Any(), gomock.Any()).Return(false, nil)
		if w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/", "unit_code\nG1\n", params); w.Code != http.StatusNotFound {
			t.Errorf("status %d", w.Code)
		}
	})

	t.Run("a multipart upload works like a raw body", func(t *testing.T) {
		app, q := newPropertyTestApp(t)
		q.EXPECT().PropertyIsActive(gomock.Any(), gomock.Any()).Return(true, nil)
		q.EXPECT().ListUnitCodes(gomock.Any(), gomock.Any()).Return(nil, nil)

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, _ := mw.CreateFormFile("file", "units.csv")
		_, _ = fw.Write([]byte("unit_code\nG1\n"))
		_ = mw.Close()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/?dry_run=true", &body)
		c.Request.Header.Set("Content-Type", mw.FormDataContentType())
		c.Params = params
		contextSetTenantID(c, testTenantID)
		app.importUnitsHandler(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("empty and oversized files are 400s", func(t *testing.T) {
		app, _ := newPropertyTestApp(t)
		if w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/", "   \n", params); w.Code != http.StatusBadRequest {
			t.Errorf("empty: status %d", w.Code)
		}
		big := "unit_code\n" + strings.Repeat("x\n", maxImportBytes)
		if w := serveAsTenant(app.importUnitsHandler, http.MethodPost, "/", big, params); w.Code != http.StatusBadRequest {
			t.Errorf("oversized: status %d", w.Code)
		}
	})
}
