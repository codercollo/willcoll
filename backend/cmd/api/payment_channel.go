package main

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/internal/reconciliation"
	"github.com/codercollo/willcoll/backend/internal/validator"
	"github.com/gin-gonic/gin"
)

// registerPaymentChannelHandler handles POST /v1/properties/:id/payment-channel
// (system-design.txt 4.6, "Get paid" onboarding card): registers where the
// landlord actually gets paid — a bank, paybill or till — as a PayHero
// settlement channel, and saves the returned channel id on the property.
// Idempotent: a channel already registered on the account with the same
// short_code + account_number is reused rather than duplicated, so retrying
// (a flaky connection, a double click) never creates a second channel.
func (app *application) registerPaymentChannelHandler(c *gin.Context) {
	manager := contextGetManager(c)
	tenantID, ok := contextGetTenantID(c)
	if manager.IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	property, err := app.models.Properties.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	var input struct {
		Type string `json:"type"` // bank | paybill | till
		// Bank is the bank's name (from payhero.Banks), required when Type
		// is "bank" — its paybill is looked up, never sent by the client.
		Bank string `json:"bank"`
		// ShortCode is the M-Pesa paybill or till number, required when
		// Type is "paybill" or "till".
		ShortCode *int64 `json:"short_code"`
		// AccountNumber is the landlord's bank account number (bank), or
		// the paybill/till account number (optional for paybill/till).
		AccountNumber string `json:"account_number"`
		// Description names the account for PayHero and is shown back to
		// the manager as "Name on this account".
		Description string `json:"description"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	v.Check(validator.PermittedValue(input.Type, payhero.ChannelTypeBank, payhero.ChannelTypePaybill, payhero.ChannelTypeTill),
		"type", "must be bank, paybill or till")
	v.Check(strings.TrimSpace(input.Description) != "", "description", "name this account so it's easy to recognise later")

	var shortCode int64
	switch input.Type {
	case payhero.ChannelTypeBank:
		v.Check(input.Bank != "", "bank", "choose the landlord's bank")
		v.Check(strings.TrimSpace(input.AccountNumber) != "", "account_number", "enter the landlord's bank account number")
		if input.Bank != "" {
			paybill, ok := payhero.BankPaybill(input.Bank)
			if !ok {
				v.AddError("bank", "not a bank we recognise")
			}
			shortCode = paybill
		}
	case payhero.ChannelTypePaybill, payhero.ChannelTypeTill:
		if input.ShortCode == nil || *input.ShortCode <= 0 {
			v.AddError("short_code", "enter the "+input.Type+" number")
		} else {
			shortCode = *input.ShortCode
		}
	}
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	accountID, err := strconv.ParseInt(app.config.payhero.accountID, 10, 64)
	if err != nil {
		app.errorResponse(c, http.StatusServiceUnavailable, "payment channel registration is not set up yet; enter an existing channel ID manually instead")
		return
	}

	ctx := c.Request.Context()

	// Reuse a channel already registered with the same destination, rather
	// than asking PayHero to create a duplicate.
	existing, err := app.payhero.ListChannels(ctx)
	if err != nil {
		app.paymentChannelErrorResponse(c, err)
		return
	}
	var channel *payhero.Channel
	for i := range existing {
		if existing[i].IsActive && int64(existing[i].ShortCode) == shortCode && existing[i].AccountNumber == input.AccountNumber {
			channel = &existing[i]
			break
		}
	}
	reused := channel != nil
	if channel == nil {
		channel, err = app.payhero.RegisterChannel(ctx, payhero.RegisterChannelRequest{
			ChannelType: input.Type, AccountID: accountID, ShortCode: shortCode,
			AccountNumber: input.AccountNumber, Description: input.Description,
		})
		if err != nil {
			app.paymentChannelErrorResponse(c, err)
			return
		}
	}

	channelID := strconv.FormatInt(channel.ID, 10)
	property.PayheroChannelID = &channelID
	if err := app.models.Properties.Update(ctx, property); err != nil {
		app.propertyWriteErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{
		"channel": envelope{"id": channel.ID, "account_number": channel.AccountNumber, "reused": reused},
		"property": property,
	}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showPaymentChannelStatusHandler handles
// GET /v1/properties/:id/payment-channel/status: checks the property's
// saved channel id against PayHero's live list, so the "Get paid" card can
// show Connected/Problem/Not connected without the manager having to send a
// test payment to find out.
func (app *application) showPaymentChannelStatusHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if contextGetManager(c).IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	property, err := app.models.Properties.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if property.PayheroChannelID == nil || *property.PayheroChannelID == "" {
		_ = app.writeJSON(c, http.StatusOK, envelope{"status": "missing"}, nil)
		return
	}
	wantID, err := strconv.ParseInt(*property.PayheroChannelID, 10, 64)
	if err != nil {
		_ = app.writeJSON(c, http.StatusOK, envelope{"status": "missing"}, nil)
		return
	}

	channels, err := app.payhero.ListChannels(c.Request.Context())
	if err != nil {
		app.paymentChannelErrorResponse(c, err)
		return
	}
	for _, ch := range channels {
		if ch.ID == wantID {
			status := "inactive"
			if ch.IsActive {
				status = "active"
			}
			if err := app.writeJSON(c, http.StatusOK, envelope{
				"status": status, "channel": envelope{"id": ch.ID, "channel_type": ch.ChannelType, "account_number": ch.AccountNumber},
			}, nil); err != nil {
				app.serverErrorResponse(c, err)
			}
			return
		}
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"status": "inactive"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// sendPaymentChannelTestHandler handles
// POST /v1/properties/:id/payment-channel/test: a real KES 10 STK push to
// the manager's own phone through the property's channel, so they can
// confirm money actually lands where they expect before telling tenants to
// pay. It carries no payment intent — a real KES 10 paid this way lands in
// the payment review queue unmatched (system-design.txt 3.8's "nobody
// identified" case) rather than against any tenant's balance, same as any
// other unexplained inbound payment.
func (app *application) sendPaymentChannelTestHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if contextGetManager(c).IsAnonymous() || !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	property, err := app.models.Properties.Get(c.Request.Context(), tenantID, id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	var input struct {
		Phone string `json:"phone"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	phone := reconciliation.NormalizePhone(input.Phone)
	v := validator.New()
	v.Check(property.PayheroChannelID != nil && *property.PayheroChannelID != "", "channel", "connect a payment channel first")
	v.Check(strings.HasPrefix(phone, "+254") && len(phone) == 13, "phone", "must be a Kenyan mobile number")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ref := "TEST-" + randomToken()
	resp, err := app.payhero.STKPush(c.Request.Context(), payhero.STKPushRequest{
		Amount: 10, PhoneNumber: strings.TrimPrefix(phone, "+"), ChannelID: *property.PayheroChannelID,
		ExternalReference: ref, CustomerName: property.Name + " test", CallbackURL: app.callbackURL("/v1/webhooks/payhero/collections"),
	})
	if err != nil {
		app.paymentChannelErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusAccepted, envelope{
		"message": "check your phone and enter your M-Pesa PIN", "reference": ref, "status": resp.Status,
	}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listPaymentChannelBanksHandler handles GET /v1/payment-channel-banks: the
// bank picker for the "Get paid" card's Step 2, so the frontend never hard
// codes bank paybills.
func (app *application) listPaymentChannelBanksHandler(c *gin.Context) {
	if contextGetManager(c).IsAnonymous() {
		app.authenticationRequiredResponse(c)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"banks": payhero.Banks}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// randomToken returns a short unguessable string for a one-off reference.
func randomToken() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:12]
}

// paymentChannelErrorResponse shows the manager PayHero's own reason in
// plain words — unlike the tenant-facing pay flow, a manager setting up
// payments needs to know exactly what went wrong (wrong account number,
// channel already claimed, PayHero balance issue...).
func (app *application) paymentChannelErrorResponse(c *gin.Context, err error) {
	var apiErr *payhero.APIError
	switch {
	case errors.Is(err, payhero.ErrNotConfigured):
		app.errorResponse(c, http.StatusServiceUnavailable, "payments are not available right now")
	case errors.As(err, &apiErr):
		app.errorResponse(c, http.StatusBadGateway, "PayHero said: "+apiErr.Body)
	default:
		app.errorResponse(c, http.StatusBadGateway, "could not reach PayHero right now; please try again")
	}
}
