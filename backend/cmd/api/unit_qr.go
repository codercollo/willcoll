package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yeqown/go-qrcode/v2"
	"github.com/yeqown/go-qrcode/writer/standard"
)

// Lease-bound unit QR codes (migration 000028). A sticker on the unit door
// encodes ONLY a URL on the branded public domain: /q/<token>. The token
// identifies the unit and never authenticates the tenant, who still verifies
// their phone by SMS on the pay page. Nothing about rent, the unit or the
// tenant is in the QR.

// qrEnabled reports whether QR_BASE_URL is set. Without it generation fails
// closed with "not available", exactly like the payment configuration.
func (app *application) qrEnabled() bool { return app.config.qr.baseURL != "" }

func (app *application) qrURL(scanCode string) string {
	return app.config.qr.baseURL + "/q/" + scanCode
}

// unitQRResponse is a code as the manager sees it: the token and URL are only
// ever sent to the firm that owns the lease.
type unitQRResponse struct {
	data.UnitQR
	Token string `json:"token"`
	URL   string `json:"url"`
}

func (app *application) newUnitQRResponse(qr *data.UnitQR) unitQRResponse {
	return unitQRResponse{UnitQR: *qr, Token: qr.Token, URL: app.qrURL(qr.ScanCode)}
}

func (app *application) qrErrorResponse(c *gin.Context, err error) {
	switch {
	case errors.Is(err, data.ErrLeaseNotFound), errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(c)
	case errors.Is(err, data.ErrQRLeaseNotActive):
		app.errorResponse(c, http.StatusConflict, "this lease has ended, so it has no unit code")
	default:
		app.serverErrorResponse(c, err)
	}
}

// qrRequest is what every manager QR route needs: the firm, the manager
// acting, and the lease. ok is false once the error response has been written.
type qrRequest struct {
	tenantID, managerID, leaseID uuid.UUID
}

func (app *application) readQRRequest(c *gin.Context) (qrRequest, bool) {
	tenantID, hasTenant := contextGetTenantID(c)
	manager := contextGetManager(c)
	if !hasTenant || manager.IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return qrRequest{}, false
	}
	if !app.qrEnabled() {
		app.errorResponse(c, http.StatusServiceUnavailable, "unit QR codes are not available right now")
		return qrRequest{}, false
	}
	leaseID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return qrRequest{}, false
	}
	return qrRequest{tenantID: tenantID, managerID: manager.ID, leaseID: leaseID}, true
}

// showUnitQRHandler handles GET /v1/leases/:id/qr: the lease's live code,
// created on first use. Token, URL, short code and scan stats.
func (app *application) showUnitQRHandler(c *gin.Context) {
	r, ok := app.readQRRequest(c)
	if !ok {
		return
	}
	qr, err := app.models.UnitQR.GetOrCreate(c.Request.Context(), r.tenantID, r.managerID, r.leaseID)
	if err != nil {
		app.qrErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"qr": app.newUnitQRResponse(qr)}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showUnitQRImageHandler handles GET /v1/leases/:id/qr.png: the printable
// sticker — "UNIT <label>" above the QR, the property name, monthly rent
// and short code below it. ?download=1 asks the browser to save it, named
// <property-slug>-unit-<label>-qr.png. Error correction level Q and a
// four-module quiet zone, so the QR itself still scans when printed small.
func (app *application) showUnitQRImageHandler(c *gin.Context) {
	r, ok := app.readQRRequest(c)
	if !ok {
		return
	}
	sticker, err := app.models.UnitQR.StickerData(c.Request.Context(), r.tenantID, r.managerID, r.leaseID)
	if err != nil {
		app.qrErrorResponse(c, err)
		return
	}
	png, err := buildStickerPNG(sticker.UnitLabel, sticker.PropertyName, "Rent: Ksh "+sticker.RentAmount.Display(), sticker.QR.ShortCode, app.qrURL(sticker.QR.ScanCode))
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	disposition := "inline"
	if c.Query("download") != "" {
		disposition = "attachment"
	}
	filename := sticker.PropertySlug + "-unit-" + sticker.UnitLabel + "-qr.png"
	c.Header("Content-Disposition", disposition+"; filename=\""+filename+"\"")
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "image/png", png)
}

// rotateUnitQRHandler handles POST /v1/leases/:id/qr/rotate: the old sticker
// stops working at once and a new code is issued.
func (app *application) rotateUnitQRHandler(c *gin.Context) {
	r, ok := app.readQRRequest(c)
	if !ok {
		return
	}
	qr, err := app.models.UnitQR.Rotate(c.Request.Context(), r.tenantID, r.managerID, r.leaseID)
	if err != nil {
		app.qrErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusCreated, envelope{"qr": app.newUnitQRResponse(qr)}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// scanUnitQRHandler handles the public GET /q/:token. A live code counts the
// scan and sends the tenant (302) to the ordinary pay page for that unit,
// where they still verify by SMS. Anything else (never existed, rotated out, or
// an ended lease) lands on the same inactive page, so the response never
// reveals which.
//
// The Location is relative ("/pay/...", no scheme or host): a phone scanning
// the sticker hits the API directly (or through the frontend's same-origin
// proxy at /q), and app.config.frontendURL is the Nuxt dev server's own
// address (localhost:3000 by default) — absolute here would send the phone
// to an address only reachable from the machine running that dev server,
// not the phone's network. frontendURL is for email links only, where an
// absolute URL is unavoidable (there is no "current origin" in an email).
func (app *application) scanUnitQRHandler(c *gin.Context) {
	// A scan must never be cached (revocation has to apply at once) and the
	// token must not leak in a Referer header.
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Robots-Tag", "noindex")

	target, err := app.models.UnitQR.Resolve(c.Request.Context(), c.Param("token"))
	if err == nil {
		err = app.models.UnitQR.RecordScan(c.Request.Context(), target)
		if err != nil && !errors.Is(err, data.ErrQRInactive) {
			// A failed counter must not stop a tenant paying rent.
			app.logError(c, err)
			err = nil
		}
	}
	switch {
	case err == nil:
		c.Redirect(http.StatusFound, "/pay/"+url.PathEscape(target.PropertySlug)+"/"+url.PathEscape(target.UnitCode))
	case errors.Is(err, data.ErrQRInactive):
		c.Redirect(http.StatusFound, "/pay/inactive")
	default:
		app.serverErrorResponse(c, err)
	}
}

// bulkGenerateQRHandler handles POST /v1/properties/:id/qr/bulk:
// {lease_ids: [...]}. Get-or-creates one active code per lease (idempotent,
// never rotating an existing code) and streams back an A4 PDF of stickers,
// sorted naturally by unit (A1, A2, ..., A10, B1). The created/reused/
// skipped summary rides along as a response header (X-Bulk-Qr-Summary,
// JSON) — the PDF body has no room for it, and the frontend shows it after
// the download starts.
func (app *application) bulkGenerateQRHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	if !app.qrEnabled() {
		app.errorResponse(c, http.StatusServiceUnavailable, "unit QR codes are not available right now")
		return
	}
	propertyID, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	var input struct {
		LeaseIDs []uuid.UUID `json:"lease_ids"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(len(input.LeaseIDs) > 0, "lease_ids", "must contain at least one lease")
	v.Check(len(input.LeaseIDs) <= 500, "lease_ids", "must not contain more than 500 leases")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}
	result, err := app.models.UnitQR.BulkGetOrCreate(c.Request.Context(), tenantID, manager.ID, propertyID, input.LeaseIDs)
	if err != nil {
		app.qrErrorResponse(c, err)
		return
	}
	if len(result.Stickers) == 0 {
		app.errorResponse(c, http.StatusUnprocessableEntity, "none of the selected leases could be issued a code")
		return
	}

	pngs := make([][]byte, 0, len(result.Stickers))
	for _, s := range result.Stickers {
		png, err := buildStickerPNG(s.UnitLabel, s.PropertyName, "Rent: Ksh "+s.RentAmount.Display(), s.QR.ShortCode, app.qrURL(s.QR.ScanCode))
		if err != nil {
			app.serverErrorResponse(c, err)
			return
		}
		pngs = append(pngs, png)
	}
	pdfBytes, err := pdf.BuildQRStickerSheet(pngs)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	summary, err := json.Marshal(struct {
		Created int               `json:"created"`
		Reused  int               `json:"reused"`
		Skipped []data.BulkQRSkip `json:"skipped"`
	}{result.Created, result.Reused, result.Skipped})
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	c.Header("X-Bulk-Qr-Summary", string(summary))
	c.Header("Access-Control-Expose-Headers", "X-Bulk-Qr-Summary")
	app.writePDF(c, "unit-qr-stickers.pdf", pdfBytes)
}

// pngWriter lets the QR library write into memory.
type pngWriter struct{ bytes.Buffer }

func (*pngWriter) Close() error { return nil }

// renderQRPNG draws content as a PNG at error correction level Q: 16 px per
// module and a four-module quiet zone.
func renderQRPNG(content string) ([]byte, error) {
	code, err := qrcode.NewWith(content, qrcode.WithErrorCorrectionLevel(qrcode.ErrorCorrectionQuart))
	if err != nil {
		return nil, err
	}
	const module, quiet = 16, 4 * 16
	buf := &pngWriter{}
	w := standard.NewWithWriter(buf, standard.WithQRWidth(module), standard.WithBorderWidth(quiet), standard.WithBuiltinImageEncoder(standard.PNG_FORMAT))
	if err := code.Save(w); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
