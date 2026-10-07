package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Login and logout (system-design.txt A.4, ADR-0001, ADR-0007). Willcoll
// signs managers and the Super Admin into SCS server-side sessions, so a
// session can be revoked instantly by deleting its row. Every product API
// verifies the same session via PLATFORM_DB_DSN; only platform-api ever
// creates or destroys one.

// loginInput is the body of both login endpoints.
type loginInput struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

func (app *application) readLogin(c *gin.Context) (loginInput, bool) {
	var input loginInput

	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return input, false
	}

	v := validator.New()
	v.Struct(input)
	v.Check(len(input.Password) <= 72, "password", "must not be more than 72 bytes long")
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return input, false
	}

	return input, true
}

// createSessionHandler handles POST /v1/sessions: a manager OR a staff
// member signs in with email and password (build-phases.txt 3f-1: staff
// share this one endpoint, told apart by principal_type in the session).
// A manager account with this email always wins over a staff one — one
// person is never both under the same login. Pending (unactivated)
// managers may sign in; each product's own dashboard routes then answer
// 403 until they activate. Suspended managers and disabled/invited staff
// get no session.
func (app *application) createSessionHandler(c *gin.Context) {
	input, ok := app.readLogin(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	manager, err := app.models.Managers.GetByEmail(ctx, input.Email)
	switch {
	case err == nil:
		app.loginAsManager(c, manager, input.Password)
	case errors.Is(err, data.ErrRecordNotFound):
		app.loginAsStaffOrFail(c, input)
	default:
		app.serverErrorResponse(c, err)
	}
}

func (app *application) loginAsManager(c *gin.Context, manager *data.Manager, plaintext string) {
	match, err := manager.Password.Matches(plaintext)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(c)
		return
	}

	if manager.Status == data.ManagerStatusSuspended {
		app.accountSuspendedResponse(c)
		return
	}

	if err := app.loginPrincipal(c, principalManager, manager.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"manager": manager}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

func (app *application) loginAsStaffOrFail(c *gin.Context, input loginInput) {
	ctx := c.Request.Context()

	staff, err := app.models.Staff.FindActiveByEmail(ctx, input.Email)
	if err != nil {
		if !errors.Is(err, data.ErrRecordNotFound) {
			app.serverErrorResponse(c, err)
			return
		}
		data.SpendPasswordCheck(input.Password)
		app.invalidCredentialsResponse(c)
		return
	}

	match, err := staff.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(c)
		return
	}

	// A suspended firm's staff get no session either (build-phases.txt
	// 3i-b): suspension revokes their existing sessions, and this stops
	// them minting a new one. (Every API refuses them on the next request
	// regardless — requireActivatedManager reads the firm's status — but
	// there's no reason to hand out a session that can't be used.)
	manager, err := app.models.Managers.Get(ctx, staff.ManagerID)
	switch {
	case errors.Is(err, data.ErrRecordNotFound):
		app.invalidCredentialsResponse(c)
		return
	case err != nil:
		app.serverErrorResponse(c, err)
		return
	case manager.Status == data.ManagerStatusSuspended:
		app.accountSuspendedResponse(c)
		return
	}

	if err := app.loginPrincipal(c, principalStaff, staff.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// createAdminSessionHandler handles POST /v1/admin/sessions: the Super
// Admin signs in with the credentials from .env (ADMIN_EMAIL /
// ADMIN_PASSWORD). A separate endpoint from manager login, so a manager
// and the admin can never be confused for one another.
func (app *application) createAdminSessionHandler(c *gin.Context) {
	input, ok := app.readLogin(c)
	if !ok {
		return
	}

	admin, err := app.models.Admins.GetByEmail(c.Request.Context(), input.Email)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			data.SpendPasswordCheck(input.Password)
			app.invalidCredentialsResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	match, err := admin.Password.Matches(input.Password)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if !match {
		app.invalidCredentialsResponse(c)
		return
	}

	if err := app.loginPrincipal(c, principalAdmin, admin.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"admin": admin}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// showSessionHandler handles GET /v1/sessions: who is signed in. Every
// product's frontend role-guard calls it to decide between its dashboard,
// the admin pages and the login page.
//
// KNOWN GAP (build-phases.txt Phase 3 step 3c-iii): the old rentals-api
// response also included an "access" key (trial/subscription paywall
// state, via BillingModel). Billing hasn't moved to platform-api yet, so
// that key is dropped here for now — each product API exposes its own
// paywall state from its own dashboard/account endpoint in the meantime.
func (app *application) showSessionHandler(c *gin.Context) {
	var body envelope

	if admin, ok := contextGetAdmin(c); ok {
		body = envelope{"principal_type": principalAdmin, "admin": admin}
	} else if staff := contextGetStaff(c); !staff.IsAnonymous() {
		body = envelope{"principal_type": principalStaff, "staff": staff, "manager": contextGetManager(c)}
	} else if manager := contextGetManager(c); !manager.IsAnonymous() {
		body = envelope{"principal_type": principalManager, "manager": manager}
	} else {
		app.authenticationRequiredResponse(c)
		return
	}

	// ?product=rentals adds that product's paywall standing (trial
	// countdown / subscribed / expired) for the signed-in firm, which each
	// product's dashboard shows as its trial banner (build-phases.txt 3i-c;
	// it left the session response with the move to platform-api).
	if product := app.readString(c.Request.URL.Query(), "product", ""); product != "" {
		if !validator.PermittedValue(product, data.ProductRentals, data.ProductEstates, data.ProductWaste) {
			app.failedValidationResponse(c, map[string]string{"product": "must be rentals, estates or waste"})
			return
		}
		if _, isAdmin := contextGetAdmin(c); !isAdmin {
			access, err := app.accessFor(c.Request.Context(), contextGetManager(c).ID, product)
			if err != nil {
				app.serverErrorResponse(c, err)
				return
			}
			body["access"] = access
		}
	}

	if err := app.writeJSON(c, http.StatusOK, body, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// deleteSessionHandler handles DELETE /v1/sessions: sign out, manager or
// admin. The session row is deleted and the cookie cleared — since the
// cookie's Domain spans every willcoll host (ADR-0007), this signs the
// principal out of every product at once.
func (app *application) deleteSessionHandler(c *gin.Context) {
	if err := app.logoutPrincipal(c); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"message": "you have been signed out"}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// revokeManagerSessions destroys every session signed in as managerID and
// reports how many there were. SCS stores sessions as opaque encoded
// blobs, so it walks them with SessionManager.Iterate rather than
// filtering in SQL; fine for a platform of this size.
func (app *application) revokeManagerSessions(ctx context.Context, managerID uuid.UUID) (int, error) {
	sm := app.sessionManager
	revoked := 0

	err := sm.Iterate(ctx, func(sessionCtx context.Context) error {
		if sm.GetString(sessionCtx, sessionKeyPrincipalType) != principalManager ||
			sm.GetString(sessionCtx, sessionKeyPrincipalID) != managerID.String() {
			return nil
		}
		revoked++
		return sm.Destroy(sessionCtx)
	})
	return revoked, err
}

// revokeStaffSessions destroys every session signed in as staffID: an
// owner disabling a staff member signs them out immediately
// (build-phases.txt 3f-1), the same way a password reset does for a
// manager.
func (app *application) revokeStaffSessions(ctx context.Context, staffID uuid.UUID) (int, error) {
	sm := app.sessionManager
	revoked := 0

	err := sm.Iterate(ctx, func(sessionCtx context.Context) error {
		if sm.GetString(sessionCtx, sessionKeyPrincipalType) != principalStaff ||
			sm.GetString(sessionCtx, sessionKeyPrincipalID) != staffID.String() {
			return nil
		}
		revoked++
		return sm.Destroy(sessionCtx)
	})
	return revoked, err
}
