package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

// maxUnclaimedList bounds the System page's list; the stats carry the true
// count, so a long backlog is visible even when the list is cut.
const maxUnclaimedList = 100

// adminListUnclaimedReceiptsHandler handles GET /v1/admin/unclaimed-receipts:
// money received on channels nobody has claimed, oldest first (C3.2). The phone
// is masked and the raw payload is not returned.
func (app *application) adminListUnclaimedReceiptsHandler(c *gin.Context) {
	ctx := c.Request.Context()

	receipts, err := app.models.Unclaimed.ListOpen(ctx, maxUnclaimedList)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	stats, err := app.models.Unclaimed.Stats(ctx)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"unclaimed_receipts": receipts, "stats": stats}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminResolveUnclaimedReceiptHandler handles POST
// /v1/admin/unclaimed-receipts/:id/resolve with {"note": "..."}: the Super Admin
// records how the money was dealt with (the channel claimed and the payment
// replayed, refunded, ...). The note is required. The row itself keeps who and
// when; the audit_log entry lands in the firm that now claims the channel, when
// there is one (audit_log is per firm, and an unclaimed channel has none).
// Resolving twice is a 404: the first resolution is never overwritten.
func (app *application) adminResolveUnclaimedReceiptHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}
	admin, ok := contextGetAdmin(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		Note string `json:"note"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}
	note := strings.TrimSpace(input.Note)
	v := validator.New()
	v.Check(note != "", "note", "must be provided")
	v.Check(len(note) <= data.MaxUnclaimedNoteLen, "note", "must not be more than 1000 bytes long")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()
	receipt, err := app.models.Unclaimed.Resolve(ctx, id, admin.ID, note)
	if err != nil {
		if errors.Is(err, data.ErrRecordNotFound) {
			app.notFoundResponse(c)
			return
		}
		app.serverErrorResponse(c, err)
		return
	}

	app.logger.Info("unclaimed receipt resolved", "receipt_id", receipt.ID, "mpesa_receipt", receipt.MpesaReceipt,
		"channel_id", receipt.ChannelID, "admin_id", admin.ID)
	if firm, claimed, err := app.models.Unclaimed.ClaimantFirm(ctx, receipt.ChannelID); err != nil {
		app.logger.Error("unclaimed receipt: looking up the channel's firm for the audit entry", "receipt_id", receipt.ID, "error", err)
	} else if claimed {
		app.auditAdmin(c, firm, data.ProductRentals, "unclaimed_receipt.resolved", "unclaimed_receipt", &receipt.ID,
			"resolved receipt "+receipt.MpesaReceipt+" on channel "+receipt.ChannelID+": "+note)
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"unclaimed_receipt": receipt}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// unclaimedReceiptsStatus is the System page's view of unrecorded-claim money:
// how many receipts are still open, their total and how long the oldest has
// waited. An open receipt is money someone must act on.
func (app *application) unclaimedReceiptsStatus(ctx context.Context) envelope {
	stats, err := app.models.Unclaimed.Stats(ctx)
	if err != nil {
		app.logger.Error("system health: reading unclaimed receipts", "error", err)
		return envelope{"status": "unreachable"}
	}
	return envelope{
		"status": "ok", "open": stats.Open, "open_amount": stats.OpenAmount,
		"oldest_age_seconds": stats.OldestAgeSeconds,
	}
}
