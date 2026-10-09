package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Super Admin oversight (build-phases.txt 3i-b), all behind requireAdmin.
// Moved here from cmd/rentals-api with the same response shapes where they
// overlap. Every read comes from willcoll_platform only (firm, modules,
// per-product subscriptions, staff count, module_usage counts) — never a
// product database. app.models.Oversight runs as willcoll_platform_admin
// (BYPASSRLS): the Super Admin sees every firm, and never writes a
// manager's tenant data.

// adminListManagersHandler handles GET /v1/admin/managers: every firm with
// its status, latest subscription, modules, per-product subscriptions,
// staff count and reported usage. Query parameters: status
// (pending|active|suspended), sort (created_at|firm_name, "-" for
// descending; default newest first), page, page_size.
func (app *application) adminListManagersHandler(c *gin.Context) {
	qs := c.Request.URL.Query()
	v := validator.New()

	status := app.readString(qs, "status", "")
	if status != "" {
		v.Check(validator.PermittedValue(status, data.ManagerStatusPending, data.ManagerStatusActive, data.ManagerStatusSuspended),
			"status", "must be pending, active or suspended")
	}

	filters := data.Filters{
		Page:         app.readInt(qs, "page", 1, v),
		PageSize:     app.readInt(qs, "page_size", 20, v),
		Sort:         app.readString(qs, "sort", "-created_at"),
		SortSafelist: []string{"created_at", "-created_at", "firm_name", "-firm_name"},
	}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	managers, metadata, err := app.models.Oversight.ListManagers(c.Request.Context(), status, filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"managers": managers, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminShowManagerHandler handles GET /v1/admin/managers/:id.
func (app *application) adminShowManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	manager, err := app.models.Oversight.GetManager(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminSuspendManagerHandler handles POST /v1/admin/managers/:id/suspend.
// The firm is suspended and every session it holds — the manager's AND
// every staff member's — is destroyed on the spot: the instant revocation
// that ADR 0001 chose server-side sessions for. The session table is
// shared (ADR-0007), so this one revocation signs the firm out of every
// product API at once; even a session somehow outliving it is refused by
// requireActivatedManager, in every binary, on its next request, because
// the status is read from willcoll_platform each time. Suspending twice is
// harmless.
func (app *application) adminSuspendManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	reason, ok := app.optionalReason(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	if err := app.models.Oversight.SuspendManager(ctx, id); err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	revoked, err := app.revokeFirmSessions(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	manager, err := app.models.Oversight.GetManager(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	summary := "suspended; " + strconv.Itoa(revoked) + " sessions revoked" + reasonSuffix(reason)
	app.auditAdmin(c, id, "", "manager.suspended", "manager", &id, summary)
	app.auditAdminAction(c, &id, "manager.suspended", "manager", &id, summary)

	err = app.writeJSON(c, http.StatusOK, envelope{"manager": manager, "sessions_revoked": revoked}, nil)
	if err != nil {
		app.serverErrorResponse(c, err)
	}
}

// adminReinstateManagerHandler handles POST /v1/admin/managers/:id/reinstate:
// lift a suspension. The firm goes back to active, or to pending if it
// never activated. The manager and their staff sign in again as usual (a
// suspension destroyed their sessions; reinstating doesn't restore them).
// Reinstating a firm that isn't suspended is harmless.
func (app *application) adminReinstateManagerHandler(c *gin.Context) {
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	reason, ok := app.optionalReason(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	if err := app.models.Oversight.ReinstateManager(ctx, id); err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.notFoundResponse(c)
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	manager, err := app.models.Oversight.GetManager(ctx, id)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	summary := "reinstated" + reasonSuffix(reason)
	app.auditAdmin(c, id, "", "manager.reinstated", "manager", &id, summary)
	app.auditAdminAction(c, &id, "manager.reinstated", "manager", &id, summary)

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// maxAdminReasonLen bounds a Super Admin's free-text reason.
const maxAdminReasonLen = 500

// optionalReason reads the optional {"reason": "..."} body of a Super Admin
// action. A request with no body has no reason; a body that is not JSON, or a
// reason over maxAdminReasonLen, is answered (400/422) and ok is false.
func (app *application) optionalReason(c *gin.Context) (reason string, ok bool) {
	if c.Request.ContentLength == 0 {
		return "", true
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return "", false
	}
	reason = strings.TrimSpace(input.Reason)
	if len(reason) > maxAdminReasonLen {
		app.failedValidationResponse(c, map[string]string{"reason": "must not be more than 500 bytes long"})
		return "", false
	}
	return reason, true
}

// reasonSuffix renders a reason for an audit summary ("" when there is none).
func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// revokeFirmSessions destroys every session signed in as the manager or as
// any of their staff, and reports how many there were. SCS stores sessions
// as opaque encoded blobs, so it walks them with SessionManager.Iterate
// (one pass for the whole firm) rather than filtering in SQL; fine for a
// platform of this size.
func (app *application) revokeFirmSessions(ctx context.Context, managerID uuid.UUID) (int, error) {
	staffIDs, err := app.models.Oversight.ListStaffIDs(ctx, managerID)
	if err != nil {
		return 0, err
	}
	staff := make(map[string]struct{}, len(staffIDs))
	for _, id := range staffIDs {
		staff[id.String()] = struct{}{}
	}

	sm := app.sessionManager
	revoked := 0
	err = sm.Iterate(ctx, func(sessionCtx context.Context) error {
		principalID := sm.GetString(sessionCtx, sessionKeyPrincipalID)
		switch sm.GetString(sessionCtx, sessionKeyPrincipalType) {
		case principalManager:
			if principalID != managerID.String() {
				return nil
			}
		case principalStaff:
			if _, ok := staff[principalID]; !ok {
				return nil
			}
		default:
			return nil
		}
		revoked++
		return sm.Destroy(sessionCtx)
	})
	return revoked, err
}

// adminListSubscriptionsHandler handles GET /v1/admin/subscriptions. Query
// parameters: product (rentals|estates|waste), status
// (trialing|active|past_due|cancelled), sort (current_period_end, "-" for
// descending; default soonest-ending first), page, page_size.
func (app *application) adminListSubscriptionsHandler(c *gin.Context) {
	qs := c.Request.URL.Query()
	v := validator.New()

	product := app.readString(qs, "product", "")
	if product != "" {
		v.Check(validator.PermittedValue(product, data.ProductRentals, data.ProductEstates, data.ProductWaste),
			"product", "must be rentals, estates or waste")
	}
	status := app.readString(qs, "status", "")
	if status != "" {
		v.Check(validator.PermittedValue(status, data.SubscriptionTrialing, data.SubscriptionActive,
			data.SubscriptionPastDue, data.SubscriptionCancelled),
			"status", "must be trialing, active, past_due or cancelled")
	}

	filters := data.Filters{
		Page:         app.readInt(qs, "page", 1, v),
		PageSize:     app.readInt(qs, "page_size", 20, v),
		Sort:         app.readString(qs, "sort", "current_period_end"),
		SortSafelist: []string{"current_period_end", "-current_period_end"},
	}
	if data.ValidateFilters(v, filters); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	subs, metadata, err := app.models.Oversight.ListSubscriptions(c.Request.Context(), product, status, filters)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"subscriptions": subs, "metadata": metadata}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}
