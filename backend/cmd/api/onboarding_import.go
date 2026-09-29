package main

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// Onboarding import: POST /v1/properties/:id/onboarding/import brings a client's
// existing building into Willcoll from one spreadsheet: units, their tenants,
// the deposits already held and the arrears owed at go-live. Send multipart
// with the file in "file", or the CSV as a text/csv body. ?dry_run=true checks
// the file and reports what would happen without writing. ?as_at=YYYY-MM-DD is
// the day the position is stated for (default today, Kenya time).
//
// Either every row is imported or none is; a 422 lists every problem by file
// row so the whole sheet can be fixed in one pass.

// onboardColumns are the columns a file may have. Header names are matched
// case-insensitively with spaces treated as underscores, and a few natural
// spellings are accepted for the commonest columns.
var onboardColumns = map[string]string{
	"unit_code": "unit_code", "unit": "unit_code", "house_no": "unit_code", "house": "unit_code",
	"meter_number": "meter_number", "meter": "meter_number",
	"tenant_name": "tenant_name", "tenant": "tenant_name", "name": "tenant_name",
	"phone": "phone", "phone_number": "phone", "mobile": "phone",
	"rent": "rent", "monthly_rent": "rent", "rent_amount": "rent",
	"start_date": "start_date", "lease_start": "start_date",
	"rent_deposit": "rent_deposit", "water_deposit": "water_deposit",
	"rent_arrears": "rent_arrears", "water_arrears": "water_arrears", "garbage_arrears": "garbage_arrears",
	"co_payer_name": "co_payer_name", "co_payer_phone": "co_payer_phone",
}

const onboardHelp = "unit_code, meter_number, tenant_name, phone, rent, start_date, rent_deposit, water_deposit, rent_arrears, water_arrears, garbage_arrears, co_payer_name, co_payer_phone"

var (
	moneyNoise   = regexp.MustCompile(`(?i)^(ksh?s?\.?|kes)\s*`)
	kenyanLocal  = regexp.MustCompile(`^0([17]\d{8})$`)
	kenyanNoPlus = regexp.MustCompile(`^254([17]\d{8})$`)
)

// importAsAt reads ?as_at, defaulting to today in Kenya.
func importAsAt(c *gin.Context, now time.Time) (time.Time, error) {
	nairobi := time.FixedZone("EAT", 3*3600)
	today := time.Date(now.In(nairobi).Year(), now.In(nairobi).Month(), now.In(nairobi).Day(), 0, 0, 0, 0, time.UTC)
	raw := strings.TrimSpace(c.Query("as_at"))
	if raw == "" {
		return today, nil
	}
	d, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errors.New("as_at must be a date like 2026-10-01")
	}
	if d.Before(today.AddDate(-1, 0, 0)) || d.After(today.AddDate(0, 0, 31)) {
		return time.Time{}, errors.New("as_at must be within the last year or the next 31 days")
	}
	return d, nil
}

// onboardingImportHandler handles POST /v1/properties/:id/onboarding/import.
func (app *application) onboardingImportHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	manager := contextGetManager(c)
	if !ok || manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	asAt, err := importAsAt(c, time.Now())
	if err != nil {
		app.failedValidationResponse(c, map[string]string{"as_at": err.Error()})
		return
	}
	raw, err := app.readCSVUpload(c)
	if err != nil {
		app.badRequestResponse(c, err)
		return
	}
	rows, problems := parseOnboardCSV(raw, asAt)
	if len(problems) > 0 {
		app.importRejectedResponse(c, problems)
		return
	}

	dryRun := c.Query("dry_run") == "true"
	summary, err := app.models.Leases.ImportOnboarding(c.Request.Context(), tenantID, manager.ID, propertyID, asAt, rows, dryRun)
	if err != nil {
		var ie *data.ImportError
		switch {
		case errors.As(err, &ie):
			app.importRejectedResponse(c, ie.Rows)
		case errors.Is(err, data.ErrPropertyNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	status := http.StatusCreated
	body := envelope{"imported": summary, "as_at": asAt.Format("2006-01-02")}
	if dryRun {
		status = http.StatusOK
		body = envelope{"dry_run": true, "summary": summary, "as_at": asAt.Format("2006-01-02")}
	}
	if err := app.writeJSON(c, status, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// parseOnboardCSV reads the header and rows and returns every problem it finds,
// so all of them can be reported at once. Blank lines are skipped, and a UTF-8
// byte order mark (Excel adds one) is ignored.
func parseOnboardCSV(raw []byte, asAt time.Time) ([]data.OnboardRow, []data.ImportRowError) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return nil, []data.ImportRowError{{Row: 1, Field: "header", Message: "the first row must be a header such as: " + onboardHelp}}
	}

	col := map[string]int{}
	var problems []data.ImportRowError
	for i, h := range header {
		name := strings.ToLower(strings.Join(strings.Fields(strings.ReplaceAll(h, "_", " ")), "_"))
		canonical, known := onboardColumns[name]
		if !known {
			problems = append(problems, data.ImportRowError{Row: 1, Field: strings.TrimSpace(h), Message: "is not a known column. Use: " + onboardHelp})
			continue
		}
		if _, dup := col[canonical]; dup {
			problems = append(problems, data.ImportRowError{Row: 1, Field: canonical, Message: "appears twice in the header"})
		}
		col[canonical] = i
	}
	if _, ok := col["unit_code"]; !ok {
		problems = append(problems, data.ImportRowError{Row: 1, Field: "unit_code", Message: "the header must have a unit_code column"})
	}
	if len(problems) > 0 {
		return nil, problems
	}

	var rows []data.OnboardRow
	seen := map[string]int{}
	rowNo := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if line, _ := r.FieldPos(0); line > 0 {
			rowNo = line
		} else {
			rowNo++
		}
		if err != nil {
			problems = append(problems, data.ImportRowError{Row: rowNo, Field: "row", Message: "is not valid CSV"})
			continue
		}
		if len(rows)+len(problems) >= data.MaxOnboardRows+50 {
			break
		}
		if allBlank(rec) {
			continue
		}
		row, rowProblems := parseOnboardRow(rec, col, rowNo, asAt)
		problems = append(problems, rowProblems...)
		if row.UnitCode != "" {
			if first, dup := seen[row.UnitCode]; dup {
				problems = append(problems, data.ImportRowError{Row: rowNo, Field: "unit_code", Message: "repeats row " + itoa(first)})
			} else {
				seen[row.UnitCode] = rowNo
			}
		}
		rows = append(rows, row)
	}

	if len(rows) == 0 && len(problems) == 0 {
		problems = append(problems, data.ImportRowError{Row: 2, Field: "unit_code", Message: "the file has a header but no units"})
	}
	if len(rows) > data.MaxOnboardRows {
		problems = append(problems, data.ImportRowError{Row: rowNo, Field: "file", Message: "has more than 500 rows; split it into smaller files"})
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return rows, nil
}

func parseOnboardRow(rec []string, col map[string]int, rowNo int, asAt time.Time) (data.OnboardRow, []data.ImportRowError) {
	var problems []data.ImportRowError
	bad := func(field, msg string) {
		problems = append(problems, data.ImportRowError{Row: rowNo, Field: field, Message: msg})
	}
	get := func(name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	money := func(name string, required bool) moneyfmt.Money {
		s := get(name)
		if s == "" {
			if required {
				bad(name, "must be provided")
			}
			return moneyfmt.Money{}
		}
		m, err := parseLenientMoney(s)
		switch {
		case err != nil:
			bad(name, "must be an amount like 12000 or 12000.50")
		case m.IsNegative():
			bad(name, "must not be negative")
		case required && !m.IsPositive():
			bad(name, "must be greater than 0")
		}
		return m
	}

	row := data.OnboardRow{Row: rowNo, UnitCode: get("unit_code"), TenantName: get("tenant_name")}
	switch {
	case row.UnitCode == "":
		bad("unit_code", "must be provided")
	case len(row.UnitCode) > 50:
		bad("unit_code", "must not be more than 50 bytes long")
	}
	if meter := get("meter_number"); meter != "" {
		row.MeterNumber = &meter
	}

	tenantFields := []string{"phone", "rent", "start_date", "rent_deposit", "water_deposit", "rent_arrears", "water_arrears", "garbage_arrears", "co_payer_name", "co_payer_phone"}
	if row.TenantName == "" {
		// A vacant unit: it may not carry half a tenant.
		for _, f := range tenantFields {
			if get(f) != "" {
				bad(f, "has a value but tenant_name is empty. Add the tenant's name, or clear this cell for a vacant unit")
			}
		}
		return row, problems
	}
	if len(row.TenantName) > 200 {
		bad("tenant_name", "must not be more than 200 bytes long")
	}

	phone, ok := normalizeKenyanPhone(get("phone"))
	switch {
	case get("phone") == "":
		bad("phone", "must be provided (the number the tenant pays from)")
	case !ok:
		bad("phone", "must be a Kenyan number like 0722 000 000 or +254722000000")
	default:
		row.Phone = phone
	}
	row.Rent = money("rent", true)
	row.RentDeposit = money("rent_deposit", false)
	row.WaterDeposit = money("water_deposit", false)
	row.RentArrears = money("rent_arrears", false)
	row.WaterArrears = money("water_arrears", false)
	row.GarbageArrears = money("garbage_arrears", false)

	if s := get("start_date"); s != "" {
		d, err := parseFlexibleDate(s)
		switch {
		case err != nil:
			bad("start_date", "must be a date like 2025-07-01 or 01/07/2025")
		case d.After(asAt):
			bad("start_date", "cannot be after the as-at date")
		default:
			row.StartDate = &d
		}
	}

	if name := get("co_payer_name"); name != "" {
		row.CoPayerName = &name
		if s := get("co_payer_phone"); s != "" {
			p, ok := normalizeKenyanPhone(s)
			switch {
			case !ok:
				bad("co_payer_phone", "must be a Kenyan number like 0722 000 000 or +254722000000")
			case p == row.Phone:
				bad("co_payer_phone", "is the same as the tenant's phone")
			default:
				row.CoPayerPhone = &p
			}
		}
	} else if get("co_payer_phone") != "" {
		bad("co_payer_name", "must be provided when there is a co_payer_phone")
	}
	return row, problems
}

// normalizeKenyanPhone accepts the ways numbers are written on paper (0722 000
// 000, 254722000000, +254 722 000 000) and returns E.164.
func normalizeKenyanPhone(s string) (string, bool) {
	s = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(s))
	switch {
	case kenyanLocal.MatchString(s):
		s = "+254" + kenyanLocal.FindStringSubmatch(s)[1]
	case kenyanNoPlus.MatchString(s):
		s = "+" + s
	}
	return s, validator.PhoneRX.MatchString(s) && strings.HasPrefix(s, "+254")
}

// parseLenientMoney reads an amount as people write it in a sheet: "18,000",
// "Ksh 18000", "18 000.50". The API itself stays strict; a spreadsheet is not.
func parseLenientMoney(s string) (moneyfmt.Money, error) {
	s = moneyNoise.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.NewReplacer(",", "", " ", "", " ", "").Replace(s)
	return moneyfmt.Parse(s)
}

// parseFlexibleDate accepts ISO (2025-07-01) and the day-first form used on
// Kenyan paperwork (01/07/2025 or 1-7-2025).
func parseFlexibleDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006"} {
		if d, err := time.Parse(layout, s); err == nil {
			return d, nil
		}
	}
	return time.Time{}, errors.New("not a date")
}
