package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/gin-gonic/gin"
)

const maxImportBytes = 1 << 20 // 1 MB is thousands of units

// importUnitsHandler handles POST /v1/properties/:id/units/import: bulk-add
// units from a CSV (system-design.txt 4.6, 6.1). Send multipart/form-data with
// the file in the "file" field, or the CSV as a text/csv body.
//
// The first row is a header. "unit_code" is required; "meter_number" is
// optional; the columns may be in any order, and other columns are refused so
// a misnamed one is not silently ignored. Either every row is imported or none
// is: a 422 lists every problem by row so the whole sheet can be fixed in one
// pass. With ?dry_run=true the file is checked and nothing is written.
func (app *application) importUnitsHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	raw, err := app.readCSVUpload(c)
	if err != nil {
		app.badRequestResponse(c, err)
		return
	}

	rows, problems := parseUnitsCSV(raw)
	if len(problems) > 0 {
		app.importRejectedResponse(c, problems)
		return
	}

	dryRun := c.Request.URL.Query().Get("dry_run") == "true"
	units, err := app.models.Units.ImportUnits(c.Request.Context(), tenantID, propertyID, rows, dryRun)
	if err != nil {
		var ie *data.ImportError
		switch {
		case errors.As(err, &ie):
			app.importRejectedResponse(c, ie.Rows)
		case errors.Is(err, data.ErrPropertyNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrDuplicateUnitCode):
			app.editConflictResponse(c) // lost a race with another import: try again
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	status := http.StatusCreated
	if dryRun {
		status = http.StatusOK
	}
	body := envelope{"imported": len(units), "dry_run": dryRun, "units": units}
	if dryRun {
		body = envelope{"valid_rows": len(rows), "dry_run": true}
	}
	if err := app.writeJSON(c, status, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func (app *application) importRejectedResponse(c *gin.Context, problems []data.ImportRowError) {
	app.errorResponse(c, http.StatusUnprocessableEntity, gin.H{
		"message": "the import has errors; nothing was imported",
		"rows":    problems,
	})
}

// readCSVUpload returns the CSV bytes from a multipart "file" field or a raw
// body, refusing anything over the size limit.
func (app *application) readCSVUpload(c *gin.Context) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportBytes+4096)

	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		file, _, err := c.Request.FormFile("file")
		if err != nil {
			return nil, errors.New(`send the CSV in a multipart "file" field`)
		}
		defer func() { _ = file.Close() }()
		return readLimited(file)
	}
	return readLimited(c.Request.Body)
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return nil, errors.New("the file could not be read or is too large")
	}
	if len(b) > maxImportBytes {
		return nil, errors.New("the file is larger than 1 MB")
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("the file is empty")
	}
	return b, nil
}

// parseUnitsCSV reads the header and rows, returning every problem found so
// they can all be reported at once. Blank lines are skipped; a UTF-8 byte
// order mark (Excel adds one) is ignored.
func parseUnitsCSV(raw []byte) ([]data.ImportUnit, []data.ImportRowError) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1 // ragged rows are reported by row, not as one parse error
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, []data.ImportRowError{{Row: 1, Field: "header", Message: "the first row must be a header such as unit_code,meter_number"}}
	}

	col := map[string]int{}
	var problems []data.ImportRowError
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(h))
		switch name {
		case "unit_code", "meter_number":
			if _, dup := col[name]; dup {
				problems = append(problems, data.ImportRowError{Row: 1, Field: name, Message: "appears twice in the header"})
			}
			col[name] = i
		default:
			problems = append(problems, data.ImportRowError{Row: 1, Field: name, Message: "is not a known column (use unit_code and meter_number)"})
		}
	}
	if _, ok := col["unit_code"]; !ok {
		problems = append(problems, data.ImportRowError{Row: 1, Field: "unit_code", Message: "the header must have a unit_code column"})
	}
	if len(problems) > 0 {
		return nil, problems
	}

	var rows []data.ImportUnit
	seen := map[string]int{}
	rowNo := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		// The reader skips blank lines without counting them, so take the real
		// line from it: an error must point at the row as it appears in the file.
		if line, _ := r.FieldPos(0); line > 0 {
			rowNo = line
		} else {
			rowNo++
		}
		if err != nil {
			problems = append(problems, data.ImportRowError{Row: rowNo, Field: "row", Message: "is not valid CSV"})
			continue
		}
		if len(rows)+len(problems) >= data.MaxImportUnits+50 {
			break
		}

		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if allBlank(rec) {
			continue
		}

		code := get("unit_code")
		switch {
		case code == "":
			problems = append(problems, data.ImportRowError{Row: rowNo, Field: "unit_code", Message: "must be provided"})
			continue
		case len(code) > 50:
			problems = append(problems, data.ImportRowError{Row: rowNo, Field: "unit_code", Message: "must not be more than 50 bytes long"})
			continue
		}
		if first, dup := seen[code]; dup {
			problems = append(problems, data.ImportRowError{Row: rowNo, Field: "unit_code", Message: "repeats row " + itoa(first)})
			continue
		}
		seen[code] = rowNo

		u := data.ImportUnit{Row: rowNo, UnitCode: code}
		if meter := get("meter_number"); meter != "" {
			u.MeterNumber = &meter
		}
		rows = append(rows, u)
	}

	if len(rows) == 0 && len(problems) == 0 {
		problems = append(problems, data.ImportRowError{Row: 2, Field: "unit_code", Message: "the file has a header but no units"})
	}
	if len(rows) > data.MaxImportUnits {
		problems = append(problems, data.ImportRowError{Row: rowNo, Field: "file", Message: "has more than 500 units; split it into smaller files"})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rows, nil
}

func allBlank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
