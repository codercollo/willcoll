package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
)

// readPeriod parses a "YYYY-MM" period, tolerating the ".pdf" suffix the
// invoice URLs carry (system-design.txt 4.6). ok is false if it is invalid.
func readPeriod(s string) (moneyfmt.Period, bool) {
	s = strings.TrimSuffix(s, ".pdf")
	// The rent endpoints also take the first-of-month form, YYYY-MM-01.
	if len(s) == 10 && strings.HasSuffix(s, "-01") {
		s = s[:7]
	}
	p, err := moneyfmt.ParsePeriod(s)
	return p, err == nil
}

// readPeriodQuery reads the required ?period= parameter, recording a
// validation error if it is missing or malformed.
func (app *application) readPeriodQuery(qs url.Values, v *validator.Validator) moneyfmt.Period {
	p, ok := readPeriod(qs.Get("period"))
	v.Check(ok, "period", "must be a month in the format YYYY-MM")
	return p
}

// writePDF sends a rendered document. Documents are rendered on demand and
// never stored, and hold a tenant's financial data, so they are not cached.
func (app *application) writePDF(c *gin.Context, filename string, pdfBytes []byte) {
	c.Header("Content-Disposition", `inline; filename="`+filename+`"`)
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}

// isSerializationFailure reports a Postgres 40001: the store already retried
// the transaction and gave up, so the client should simply try again.
func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

// billingErrorResponse maps the billing runs' sentinel errors to responses.
func (app *application) billingErrorResponse(c *gin.Context, err error) {
	var sanityErr *data.SanityCheckError
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.notFoundResponse(c)
	case errors.Is(err, data.ErrEditConflict), isSerializationFailure(err):
		app.editConflictResponse(c)
	case errors.Is(err, data.ErrTooManyCodes):
		app.errorResponse(c, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, data.ErrInvalidCode):
		app.errorResponse(c, http.StatusUnauthorized, err.Error())
	case errors.As(err, &sanityErr):
		app.codedErrorResponse(c, http.StatusUnprocessableEntity, err.Error(), "sanity_failed",
			envelope{"failures": sanityErr.Result.Failures})
	case errors.Is(err, data.ErrPeriodNotConfirmed):
		app.codedErrorResponse(c, http.StatusConflict, err.Error(), "period_not_confirmed", nil)
	case errors.Is(err, data.ErrPeriodStale):
		app.codedErrorResponse(c, http.StatusConflict, err.Error(), "period_stale", nil)
	case errors.Is(err, data.ErrReadingLocked),
		errors.Is(err, data.ErrPaymentAlreadyAllocated),
		errors.Is(err, data.ErrNothingToConfirm),
		errors.Is(err, data.ErrRunAlreadyGenerated),
		errors.Is(err, data.ErrAlreadyReversed),
		errors.Is(err, data.ErrNotReversible),
		errors.Is(err, data.ErrDuplicatePayment):
		app.errorResponse(c, http.StatusConflict, err.Error())
	case errors.Is(err, data.ErrPaymentParticularsMissing):
		app.codedErrorResponse(c, http.StatusUnprocessableEntity, err.Error(), "payment_particulars_missing", nil)
	case errors.Is(err, data.ErrNoUnitsBilledForGarbage):
		app.codedErrorResponse(c, http.StatusUnprocessableEntity, err.Error(), "no_units_billed_for_garbage", nil)
	case errors.Is(err, data.ErrPlanTooSmall):
		app.codedErrorResponse(c, http.StatusUnprocessableEntity, err.Error(), "plan_too_small", nil)
	case errors.Is(err, data.ErrUnitNotInProperty),
		errors.Is(err, data.ErrInvalidReading),
		errors.Is(err, data.ErrNothingToGenerate),
		errors.Is(err, data.ErrGarbageNotEnabled),
		errors.Is(err, data.ErrNoActiveLease),
		errors.Is(err, data.ErrInvalidAmount),
		errors.Is(err, data.ErrPeriodTooFar),
		errors.Is(err, data.ErrReasonRequired),
		errors.Is(err, data.ErrPropertyNotLinked),
		errors.Is(err, data.ErrNoPlan),
		errors.Is(err, data.ErrInvalidInput):
		app.errorResponse(c, http.StatusUnprocessableEntity, err.Error())
	default:
		app.serverErrorResponse(c, err)
	}
}
