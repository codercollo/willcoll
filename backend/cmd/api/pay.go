package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
)

// The public tenant payment surface (system-design.txt 4.7). No login: the
// payer proves they hold a phone on the unit lease with an SMS code, and gets
// a short pay session for that one unit. Every route here sits behind the
// per-IP rate limit.

// payTarget resolves the pay URL to its manager, property and unit, writing a
// 404 (or 503 when the pay surface is switched off) if it cannot.
func (app *application) payTarget(c *gin.Context) (*data.PayTarget, bool) {
	if !app.paySessionsEnabled() {
		app.errorResponse(c, http.StatusServiceUnavailable, "payments are not available right now")
		return nil, false
	}
	target, err := app.models.Resolver.PayTarget(c.Request.Context(), c.Param("propertySlug"), c.Param("unitCode"))
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(c)
		} else {
			app.serverErrorResponse(c, err)
		}
		return nil, false
	}
	return target, true
}

// paySessionFor reads the pay-session token from "Authorization: Bearer ..."
// and checks it is for this unit. It writes a 401 if not.
func (app *application) paySessionFor(c *gin.Context, target *data.PayTarget) (*paySession, bool) {
	s, ok := app.paySession(c)
	if !ok {
		return nil, false
	}
	if s.UnitID != target.UnitID || s.TenantID != target.TenantID {
		app.invalidPaySession(c)
		return nil, false
	}
	return s, true
}

func (app *application) paySession(c *gin.Context) (*paySession, bool) {
	if !app.paySessionsEnabled() {
		app.errorResponse(c, http.StatusServiceUnavailable, "payments are not available right now")
		return nil, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer"))
	s, err := app.verifyPaySession(token, time.Now())
	if err != nil {
		app.invalidPaySession(c)
		return nil, false
	}
	return s, true
}

func (app *application) invalidPaySession(c *gin.Context) {
	app.errorResponse(c, http.StatusUnauthorized, "verify your phone number to continue")
}

// showPayPageHandler handles GET /v1/pay/:propertySlug/:unitCode: what the
// pay page shows before anyone has proved who they are.
func (app *application) showPayPageHandler(c *gin.Context) {
	target, ok := app.payTarget(c)
	if !ok {
		return
	}
	info, err := app.models.PayAccess.Info(c.Request.Context(), *target)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"unit": info}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// requestPayOTPHandler handles POST /v1/pay/:propertySlug/:unitCode/otp:
// text a one-time code to a phone on the lease. The answer is the same for
// any number, so the endpoint cannot be used to find out who lives where.
func (app *application) requestPayOTPHandler(c *gin.Context) {
	target, ok := app.payTarget(c)
	if !ok {
		return
	}

	var input struct {
		Phone string `json:"phone"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(strings.TrimSpace(input.Phone) != "", "phone", "must be provided")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	code, issued, err := app.models.PayAccess.IssueOTP(c.Request.Context(), *target, input.Phone, app.otpHash, app.config.pay.otpTTL)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}

	if issued {
		phone := reconciliation.NormalizePhone(input.Phone)
		app.background(func() { app.sendOTP(phone, code) })
	}
	if err := app.writeJSON(c, http.StatusAccepted, envelope{"message": "if that number is on this unit, a code has been sent"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func (app *application) sendOTP(phone, code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := app.sms.SendOTP(ctx, phone, code, app.config.pay.otpTTL); err != nil {
		app.logger.Error("pay OTP not sent", "error", err.Error())
		// Without SMS credentials a developer could never sign in; show the
		// code in the log outside production only.
		if app.config.env == "development" {
			app.logger.Info("development pay OTP", "phone", phone, "code", code)
		}
	}
}

// verifyPayOTPHandler handles POST /v1/pay/:propertySlug/:unitCode/otp/verify:
// trade a correct code for a short-lived, signed pay-session token.
func (app *application) verifyPayOTPHandler(c *gin.Context) {
	target, ok := app.payTarget(c)
	if !ok {
		return
	}

	var input struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(strings.TrimSpace(input.Phone) != "", "phone", "must be provided")
	v.Check(strings.TrimSpace(input.Code) != "", "code", "must be provided")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	phone, err := app.models.PayAccess.VerifyOTP(c.Request.Context(), *target, input.Phone, input.Code, app.otpHash)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}

	expires := time.Now().Add(app.config.pay.sessionTTL)
	token, err := app.signPaySession(paySession{TenantID: target.TenantID, UnitID: target.UnitID, Phone: phone}, expires)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"token": token, "expires_at": expires.UTC()}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showPayBalancesHandler handles GET /v1/pay/:propertySlug/:unitCode/balances
// (needs a pay session): what can be paid and what is owed. A deposit is only
// offered while something is still owed on it.
func (app *application) showPayBalancesHandler(c *gin.Context) {
	target, ok := app.payTarget(c)
	if !ok {
		return
	}
	if _, ok := app.paySessionFor(c, target); !ok {
		return
	}
	lines, err := app.models.PayAccess.Balances(c.Request.Context(), *target)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"balances": lines}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createPaymentIntentHandler handles POST /v1/pay/:propertySlug/:unitCode/intent
// (needs a pay session). Body: {"lines":[{"type","amount"}], "phone"}. It
// stores the exact split, then asks PayHero to prompt the phone through the
// property collections channel; the split is applied when the money arrives.
func (app *application) createPaymentIntentHandler(c *gin.Context) {
	target, ok := app.payTarget(c)
	if !ok {
		return
	}
	session, ok := app.paySessionFor(c, target)
	if !ok {
		return
	}

	var input struct {
		Lines []struct {
			Type   string          `json:"type"`
			Amount *moneyfmt.Money `json:"amount"`
		} `json:"lines"`
		Phone string `json:"phone"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	v := validator.New()
	v.Check(len(input.Lines) > 0, "lines", "must contain at least one line")
	v.Check(len(input.Lines) <= 5, "lines", "must not contain more than five lines")
	lines := make([]data.IntentLine, 0, len(input.Lines))
	for _, l := range input.Lines {
		v.Check(l.Amount != nil, "lines", "every line needs an amount")
		if l.Amount != nil {
			lines = append(lines, data.IntentLine{Type: l.Type, Amount: *l.Amount})
		}
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}
	phone := input.Phone
	if strings.TrimSpace(phone) == "" {
		phone = session.Phone // default to the phone that was verified
	}

	ctx := c.Request.Context()
	intent, err := app.models.PayAccess.CreateIntent(ctx, *target, phone, lines, app.config.pay.intentTTL)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}

	resp, err := app.payhero.STKPush(ctx, payhero.STKPushRequest{
		Amount:            intent.Shillings,
		PhoneNumber:       strings.TrimPrefix(reconciliation.NormalizePhone(phone), "+"),
		ChannelID:         intent.ChannelID,
		ExternalReference: intent.Reference,
		CustomerName:      intent.PropertyName + " " + intent.UnitCode,
		CallbackURL:       app.callbackURL("/v1/webhooks/payhero/collections"),
	})
	message := "check your phone and enter your M-Pesa PIN"
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			// A definite failure (bad request, PayHero down, unreachable): the
			// push was never accepted, so it's safe to say so.
			app.failPush(ctx, err, func(reason string) error {
				return app.models.PayAccess.FailIntent(ctx, target.TenantID, intent.ID, reason)
			})
			app.pushErrorResponse(c, err)
			return
		}
		// The call timed out waiting on PayHero's response, but the request
		// may well have already reached them and prompted the phone — we
		// genuinely don't know. Leave the intent pending (never FailIntent
		// here) rather than tell the tenant it failed: a resubmit of the
		// same payment reuses this same intent and reference (CreateIntent's
		// GetPendingIntentForUnit lookup), so at worst PayHero gets asked
		// twice for the one prompt, never charged twice.
		app.logger.Warn("STK push timed out; treating as ambiguous, not failed", "intent", intent.ID.String(), "error", err.Error())
		message = "check your phone; if no prompt arrives in a minute, try again"
	} else if resp.CheckoutRequestID != "" {
		if err := app.models.PayAccess.RecordCheckout(ctx, target.TenantID, intent.ID, resp.CheckoutRequestID); err != nil {
			// The push is already out; the callback still finds the intent by
			// its reference, so this is worth a log line and nothing more.
			app.logger.Warn("checkout id not recorded", "intent", intent.ID.String(), "error", err.Error())
		}
	}

	out := envelope{"intent": envelope{
		"id": intent.ID, "status": data.PaymentIntentStatusPending, "amount": intent.Amount, "expires_at": intent.ExpiresAt.UTC(),
	}, "message": message}
	if err := app.writeJSON(c, http.StatusAccepted, out, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showPaymentIntentStatusHandler handles GET /v1/pay/intents/:id/status (needs
// a pay session): poll until the callback completes the intent. The URL has no
// property slug, so the session names the tenant and unit it may read.
func (app *application) showPaymentIntentStatusHandler(c *gin.Context) {
	session, ok := app.paySession(c)
	if !ok {
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	status, err := app.models.PayAccess.Status(c.Request.Context(), session.TenantID, session.UnitID, id)
	if err != nil {
		app.billingErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"intent": status}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// callbackURL is where PayHero should post an outcome: this API public origin
// plus the webhook path, carrying the shared secret PayHero cannot sign with.
// Empty when no public origin is configured, in which case PayHero uses the
// callback set on the channel.
func (app *application) callbackURL(path string) string {
	base := app.config.payhero.callbackBaseURL
	if base == "" {
		return ""
	}
	u := base + path
	if app.config.payhero.webhookSecret != "" {
		u += "?" + payhero.TokenQueryParam + "=" + app.config.payhero.webhookSecret
	}
	return u
}

// failPush records a failed STK push against its intent or invoice, and logs
// PayHero reason (its body) which the client must never see.
func (app *application) failPush(ctx context.Context, err error, mark func(reason string) error) {
	app.logger.Error("STK push failed", "error", err.Error())
	if mErr := mark("payment request could not be sent"); mErr != nil {
		app.logger.Error("could not mark the push failed", "error", mErr.Error())
	}
}

// pushErrorResponse turns a PayHero failure into a client answer that hides
// PayHero internals.
func (app *application) pushErrorResponse(c *gin.Context, err error) {
	var apiErr *payhero.APIError
	switch {
	case errors.Is(err, payhero.ErrNotConfigured):
		app.errorResponse(c, http.StatusServiceUnavailable, "payments are not available right now")
	case errors.As(err, &apiErr) && !apiErr.Retryable():
		app.errorResponse(c, http.StatusBadGateway, "the payment could not be requested; check the phone number and try again")
	default:
		app.errorResponse(c, http.StatusBadGateway, "the payment service is not responding; please try again shortly")
	}
}
