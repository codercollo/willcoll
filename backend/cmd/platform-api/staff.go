package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
)

// Staff accounts (system-design.txt A.1, build-phases.txt 3f-1): sub-logins
// under a manager, invited and managed by an owner (requireOwner — the
// manager's own login is always the implicit owner).

// staffInviteTokenTTL is how long an emailed invite code stays valid.
const staffInviteTokenTTL = 3 * 24 * time.Hour

// inviteStaffHandler handles POST /v1/staff: {"name", "email", "phone",
// "role", "module_access": [...]}. Creates the staff row invited and
// emails an activation link; the staff member sets their own password to
// activate (activateStaffHandler).
func (app *application) inviteStaffHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}

	var input struct {
		Name         string   `json:"name"`
		Email        string   `json:"email"`
		Phone        *string  `json:"phone"`
		Role         string   `json:"role"`
		ModuleAccess []string `json:"module_access"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	staff := &data.Staff{
		Name: input.Name, Email: input.Email, Phone: input.Phone,
		Role: input.Role, ModuleAccess: input.ModuleAccess,
	}
	if staff.ModuleAccess == nil {
		staff.ModuleAccess = []string{}
	}

	v := validator.New()
	if data.ValidateStaff(v, staff); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()
	if err := app.models.Staff.Invite(ctx, tenantID, staff); err != nil {
		switch {
		case errors.Is(err, data.ErrDuplicateStaffEmail):
			app.failedValidationResponse(c, map[string]string{"email": "a staff member with this email already exists"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	token, err := app.models.Tokens.NewForStaff(ctx, staff.ID, staffInviteTokenTTL, data.ScopeActivation)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	manager := contextGetManager(c)
	app.sendStaffInviteEmail(manager, staff, token)
	app.audit(c, "", "staff.invited", "staff", &staff.ID, "invited "+staff.Email+" as "+staff.Role)

	if err := app.writeJSON(c, http.StatusCreated, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// listStaffHandler handles GET /v1/staff.
func (app *application) listStaffHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	staff, err := app.models.Staff.List(c.Request.Context(), tenantID)
	if err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.writeJSON(c, http.StatusOK, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// updateStaffHandler handles PATCH /v1/staff/:id: {"name", "phone", "role",
// "module_access", "status"}, all optional. Setting status to "disabled"
// signs the staff member out of every device at once.
func (app *application) updateStaffHandler(c *gin.Context) {
	tenantID, ok := contextGetTenantID(c)
	if !ok {
		app.authenticationRequiredResponse(c)
		return
	}
	id, err := app.readIDParam(c)
	if err != nil {
		app.notFoundResponse(c)
		return
	}

	ctx := c.Request.Context()
	staff, err := app.models.Staff.Get(ctx, tenantID, id)
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
		Name         *string  `json:"name"`
		Phone        *string  `json:"phone"`
		Role         *string  `json:"role"`
		ModuleAccess []string `json:"module_access"`
		Status       *string  `json:"status"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	if input.Name != nil {
		staff.Name = strings.TrimSpace(*input.Name)
	}
	if input.Phone != nil {
		staff.Phone = input.Phone
	}
	if input.Role != nil {
		staff.Role = *input.Role
	}
	if input.ModuleAccess != nil {
		staff.ModuleAccess = input.ModuleAccess
	}
	statusChangedToDisabled := false
	if input.Status != nil {
		v := validator.New()
		v.Check(validator.PermittedValue(*input.Status, data.StaffActive, data.StaffDisabled), "status", "must be one of active, disabled")
		if !v.Valid() {
			app.failedValidationResponse(c, v.Errors)
			return
		}
		statusChangedToDisabled = *input.Status == data.StaffDisabled && staff.Status != data.StaffDisabled
		staff.Status = *input.Status
	}

	v := validator.New()
	if data.ValidateStaff(v, staff); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	if err := app.models.Staff.Update(ctx, tenantID, staff); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		case errors.Is(err, data.ErrDuplicateStaffEmail):
			app.failedValidationResponse(c, map[string]string{"email": "a staff member with this email already exists"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if statusChangedToDisabled {
		if _, err := app.revokeStaffSessions(ctx, staff.ID); err != nil {
			app.logger.Error("revoking disabled staff member's sessions", "staff_id", staff.ID, "error", err)
		}
	}
	app.audit(c, "", "staff.updated", "staff", &staff.ID, "updated "+staff.Email+" (role "+staff.Role+", status "+staff.Status+")")

	if err := app.writeJSON(c, http.StatusOK, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// activateStaffHandler handles PUT /v1/staff/activated: {"token": ...,
// "password": ...} from the invite email. A valid, unexpired token sets
// the password and flips the staff member to active.
func (app *application) activateStaffHandler(c *gin.Context) {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	v := validator.New()
	data.ValidateTokenPlaintext(v, input.Token)
	data.ValidateManagerPasswordPlaintext(v, input.Password)
	if !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	ctx := c.Request.Context()
	staff, err := app.models.Staff.GetForToken(ctx, data.ScopeActivation, input.Token)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrRecordNotFound):
			app.failedValidationResponse(c, map[string]string{"token": "invalid or expired activation token"})
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if staff.Status == data.StaffDisabled {
		app.accountSuspendedResponse(c)
		return
	}

	if err := staff.Password.Set(input.Password); err != nil {
		app.serverErrorResponse(c, err)
		return
	}
	if err := app.models.Staff.Activate(ctx, staff); err != nil {
		switch {
		case errors.Is(err, data.ErrEditConflict):
			app.editConflictResponse(c)
		default:
			app.serverErrorResponse(c, err)
		}
		return
	}

	if err := app.models.Tokens.DeleteAllForStaff(ctx, data.ScopeActivation, staff.ID); err != nil {
		app.serverErrorResponse(c, err)
		return
	}

	if err := app.writeJSON(c, http.StatusOK, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// sendStaffInviteEmail sends the invite email in the background. A failed
// send is logged; an owner can re-invite (deletes the disabled/invited row
// is not offered today — out of scope for 3f-1) to retry.
func (app *application) sendStaffInviteEmail(manager *data.Manager, staff *data.Staff, token *data.Token) {
	emailData := map[string]any{
		"FirmName":        manager.FirmName,
		"StaffName":       staff.Name,
		"Role":            staff.Role,
		"ActivationURL":   app.config.frontendURL + "/auth/activate-staff?token=" + token.Plaintext,
		"ActivationToken": token.Plaintext,
		"ExpiresIn":       humanDuration(staffInviteTokenTTL),
	}

	app.background(func() {
		if err := app.mailer.Send(staff.Email, "staff_invite.tmpl", emailData); err != nil {
			app.logger.Error("sending staff invite email", "staff_id", staff.ID, "error", err)
		}
	})
}
