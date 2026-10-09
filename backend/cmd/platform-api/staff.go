package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/platform/data"
	"github.com/codercollo/willcoll/backend/internal/shared/validator"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Staff accounts (system-design.txt A.1, build-phases.txt 3f-1): sub-logins
// under a manager, invited and managed by an owner (requireOwner — the
// manager's own login is always the implicit owner).

// staffInviteTokenTTL is how long an emailed invite code stays valid.
const staffInviteTokenTTL = 3 * 24 * time.Hour

// inviteStaffHandler handles POST /v1/staff: {"name", "email", "phone",
// "role", "module_access": [...], "property_access": {...}}. Creates the staff row invited and
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
		// PropertyAccess: {"rentals": ["<property id>", ...]}; absent or an
		// empty list = all properties of that product (D2).
		PropertyAccess map[string][]uuid.UUID `json:"property_access"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	staff := &data.Staff{
		Name: input.Name, Email: input.Email, Phone: input.Phone,
		Role: input.Role, ModuleAccess: input.ModuleAccess,
		PropertyAccess: data.NormalizePropertyAccess(input.PropertyAccess),
	}
	if staff.ModuleAccess == nil {
		staff.ModuleAccess = []string{}
	}

	v := validator.New()
	if data.ValidateStaff(v, staff); !v.Valid() {
		app.failedValidationResponse(c, v.Errors)
		return
	}

	// Only the firm login grants the owner role (D3). Before step-up, like every
	// permission check: someone who may never do this is not asked for a password.
	if staff.Role == data.RoleOwner && app.staffActorMayNotChangeOwner(c) {
		return
	}

	// Inviting grants a role, so it needs a recent password re-entry (D5). Checked
	// here, after the permission checks above, and not as route middleware: whoever
	// may never invite this role (D3) gets a plain 403, not a password prompt.
	if !app.stepUpSatisfied(c) {
		app.stepUpRequiredResponse(c)
		return
	}

	ctx := c.Request.Context()
	// module_access can only name products the firm actually holds (M8).
	if errs, err := app.moduleAccessErrors(ctx, tenantID, staff.ModuleAccess); err != nil {
		app.serverErrorResponse(c, err)
		return
	} else if len(errs) > 0 {
		app.failedValidationResponse(c, errs)
		return
	}
	// The invite and its owner alert commit together (C4.0). The staff id is
	// only assigned by the insert, so the event is keyed by the invitee's email.
	inviteAlert := app.ownerAlertHook(c, "staff.invited", staff.Email, "A staff member was invited",
		staff.Name+" ("+staff.Email+") as "+staff.Role+", access to "+strings.Join(staff.ModuleAccess, ", "),
		app.staffLink())
	if err := app.models.Staff.Invite(ctx, tenantID, staff, inviteAlert); err != nil {
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
		// PropertyAccess replaces the whole value when present ({} clears it).
		PropertyAccess *map[string][]uuid.UUID `json:"property_access"`
	}
	if err := app.readJSON(c, &input); err != nil {
		app.badRequestResponse(c, err)
		return
	}

	before := *staff // what the sessions were issued against
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
	if input.PropertyAccess != nil {
		staff.PropertyAccess = data.NormalizePropertyAccess(*input.PropertyAccess)
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

	// Only the firm login grants or revokes the owner role (D3), a staff owner
	// included. Permission first, then step-up (below), then M8.
	if before.Role != staff.Role && (before.Role == data.RoleOwner || staff.Role == data.RoleOwner) &&
		app.staffActorMayNotChangeOwner(c) {
		return
	}

	// module_access can only gain products the firm actually holds (M8); a
	// product the member already has is not re-checked, so an unrelated edit
	// never fails because the firm's module lapsed.
	var added []string
	for _, module := range staff.ModuleAccess {
		if !slices.Contains(before.ModuleAccess, module) {
			added = append(added, module)
		}
	}
	if errs, err := app.moduleAccessErrors(ctx, tenantID, added); err != nil {
		app.serverErrorResponse(c, err)
		return
	} else if len(errs) > 0 {
		app.failedValidationResponse(c, errs)
		return
	}

	// Changing what a member may do, or removing them, needs step-up (D5); a
	// rename or phone edit does not. Checked before anything is saved.
	reason := authorityChange(before, *staff, statusChangedToDisabled)
	if reason != "" && !app.stepUpSatisfied(c) {
		app.stepUpRequiredResponse(c)
		return
	}

	// The change and its owner alerts commit together (C4.0): a crash can't
	// leave an authority change the owner was never told about.
	alerts := app.staffChangeAlerts(c, before, *staff, statusChangedToDisabled)
	if err := app.models.Staff.Update(ctx, tenantID, staff, alerts...); err != nil {
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

	// Sign the staff member out everywhere when their authority changes: they
	// are disabled (removed), demoted or otherwise change role, or their
	// module or property scope changes. Authorization is also re-read on every
	// request, so this is the second layer: no session issued under the old
	// authority survives, and the person re-authenticates under the new one.
	if reason != "" {
		if _, err := app.revokeStaffSessions(ctx, staff.ID); err != nil {
			app.logger.Error("revoking staff member's sessions", "staff_id", staff.ID, "reason", reason, "error", err)
		}
	}
	detail := "updated " + staff.Email + " (role " + staff.Role + ", status " + staff.Status + ")"
	if reason != "" {
		detail += "; signed out everywhere: " + reason
	}
	app.audit(c, "", "staff.updated", "staff", &staff.ID, detail)

	if err := app.writeJSON(c, http.StatusOK, envelope{"staff": staff}, nil); err != nil {
		app.serverErrorResponse(c, err)
	}
}

// staffActorMayNotChangeOwner answers 403 and reports true when the caller is a
// staff member (even a staff owner): granting or revoking the owner role is the
// firm login's alone (authorization.md D3). The firm login can never be locked
// out by this: it is not a staff row and cannot be demoted or disabled.
func (app *application) staffActorMayNotChangeOwner(c *gin.Context) bool {
	if contextGetStaff(c).IsAnonymous() {
		return false
	}
	app.logDenied(c, "owner role (firm login only)")
	app.notPermittedResponse(c)
	return true
}

// moduleAccessErrors reports (as validation errors, 422) every product in
// modules the firm does not hold: M8. A held product is one the firm is
// trialing or has active in manager_modules; requested and suspended ones are
// not. A product that does not exist at all is rejected earlier by
// ValidateStaff.
func (app *application) moduleAccessErrors(ctx context.Context, tenantID uuid.UUID, modules []string) (map[string]string, error) {
	var missing []string
	for _, product := range modules {
		m, err := app.models.Modules.Get(ctx, tenantID, product)
		switch {
		case err == nil && (m.Status == data.ModuleStatusActive || m.Status == data.ModuleStatusTrialing):
		case err == nil || errors.Is(err, data.ErrRecordNotFound):
			missing = append(missing, product)
		default:
			return nil, err
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	return map[string]string{"module_access": "the firm does not have an active " + strings.Join(missing, ", ") + " module"}, nil
}

// staffLink is where the owner sees the staff list.
func (app *application) staffLink() string { return app.config.frontendURL + "/staff" }

// staffChangeAlerts is the owner alert for every authority change (D5, C3.7), as
// hooks for StaffModel.Update to run in its transaction: a role change, a
// change of product (module_access) or property scope, and a removal
// (disabling). One alert per kind of change, so a single save that changes two
// things sends two clearly-headlined emails.
func (app *application) staffChangeAlerts(c *gin.Context, before, after data.Staff, disabled bool) []data.TxHook {
	who := after.Name + " (" + after.Email + ")"
	id := after.ID.String()
	if disabled {
		// a removed member's other changes are moot
		return []data.TxHook{app.ownerAlertHook(c, "staff.removed", id, "A staff member was removed",
			who+", was "+before.Role+"; signed out everywhere", app.staffLink())}
	}
	var hooks []data.TxHook
	if before.Role != after.Role {
		hooks = append(hooks, app.ownerAlertHook(c, "staff.role", id, "A staff member's role was changed",
			who+": "+before.Role+" to "+after.Role, app.staffLink()))
	}
	if !sameStrings(before.ModuleAccess, after.ModuleAccess) {
		hooks = append(hooks, app.ownerAlertHook(c, "staff.modules", id, "A staff member's product access was changed",
			who+": "+accessList(before.ModuleAccess)+" to "+accessList(after.ModuleAccess), app.staffLink()))
	}
	if !data.PropertyAccessEqual(before.PropertyAccess, after.PropertyAccess) {
		hooks = append(hooks, app.ownerAlertHook(c, "staff.properties", id, "A staff member's property access was changed",
			who+": "+propertyScopeSummary(before.PropertyAccess)+" to "+propertyScopeSummary(after.PropertyAccess), app.staffLink()))
	}
	return hooks
}

func accessList(modules []string) string {
	if len(modules) == 0 {
		return "no products"
	}
	sorted := slices.Clone(modules)
	slices.Sort(sorted)
	return strings.Join(sorted, ", ")
}

// propertyScopeSummary says how many properties a member is limited to, per
// product; ids are not repeated in an email.
func propertyScopeSummary(access map[string][]uuid.UUID) string {
	access = data.NormalizePropertyAccess(access)
	if len(access) == 0 {
		return "all properties"
	}
	products := make([]string, 0, len(access))
	for product := range access {
		products = append(products, product)
	}
	slices.Sort(products)
	parts := make([]string, 0, len(products))
	for _, product := range products {
		n := len(access[product])
		noun := "properties"
		if n == 1 {
			noun = "property"
		}
		parts = append(parts, fmt.Sprintf("%d %s in %s", n, noun, product))
	}
	return "limited to " + strings.Join(parts, ", ")
}

// authorityChange names why a staff update must end the member's sessions, or
// returns "" when it need not (a name or phone edit, an unchanged scope).
func authorityChange(before, after data.Staff, disabled bool) string {
	switch {
	case disabled:
		return "disabled"
	case before.Role != after.Role:
		return "role changed"
	case !sameStrings(before.ModuleAccess, after.ModuleAccess):
		return "module access changed"
	case !data.PropertyAccessEqual(before.PropertyAccess, after.PropertyAccess):
		return "property access changed"
	}
	return ""
}

func sameStrings(a, b []string) bool {
	as, bs := slices.Clone(a), slices.Clone(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
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
