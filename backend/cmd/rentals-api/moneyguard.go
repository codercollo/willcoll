package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/rentals/data"
	"github.com/codercollo/willcoll/backend/internal/shared/auth"
	"github.com/codercollo/willcoll/backend/internal/shared/httpx"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The money-field guard (docs/security/authorization.md D1, c4-matrix-fixes.md
// item 4). Anything that sets or changes an amount a tenant is charged (rent, a
// deposit, a service charge, a rate, a fee) needs accountant or above. A caretaker
// (officer) can edit labels, notes and meter numbers on a unit, and take
// readings, but cannot quietly lower a rent or a water rate.
//
// ONE rule, applied by every route an officer can reach that carries such a
// field: refuseMoneyChange. It compares each field the request SENDS with the
// value it HAS now:
//
//   - unchanged (an echo, as a full-object form sends) is always fine;
//   - changed, by someone below accountant: 403, and nothing is written, not a
//     partial apply (the guard runs before any write);
//   - changed, by an accountant: allowed where the route really owns the field
//     (the water grid's rate), and a 422 pointing at the right route where it does
//     not (the unit has no rent: it is on the lease).

// moneyFieldPermission is the capability "may set amounts": the permission that
// already gates the rent schedule, held by accountant and above.
const moneyFieldPermission = auth.RentScheduleWrite

// moneyFieldNames are the request keys that carry an amount a tenant is charged.
// A unit route accepts them only to run the guard; a key not listed here is
// unknown to those routes and rejected as usual.
var moneyFieldNames = []string{
	"rent_amount", "rent_deposit_amount", "water_deposit_amount", "electricity_deposit_amount",
	"service_charge", "water_rate", "water_rate_per_unit", "garbage_fee",
}

// moneyChange is one money field a request carries: what it sent, what is there now.
type moneyChange struct {
	Field   string
	Sent    moneyfmt.Money
	Current moneyfmt.Money
}

func (c moneyChange) changed() bool { return c.Sent != c.Current }

// refuseMoneyChange answers 403 and reports true when changes holds a CHANGED
// value and the caller is below accountant. It writes nothing itself: the caller
// returns before any write.
func (app *application) refuseMoneyChange(c *gin.Context, changes []moneyChange) bool {
	var fields []string
	for _, ch := range changes {
		if ch.changed() {
			fields = append(fields, ch.Field)
		}
	}
	if len(fields) == 0 || app.permitted(c, moneyFieldPermission) {
		return false
	}
	sort.Strings(fields)
	app.logDenied(c, moneyFieldPermission)
	app.errorResponse(c, http.StatusForbidden, "changing "+strings.Join(fields, ", ")+" needs an accountant or the owner")
	return true
}

// takeMoneyFields reads the request body, removes the money fields from it, and
// leaves the rest for the route's own strict decoding. A body that is not a JSON
// object is left untouched for the route to reject in its usual words. ok is
// false when a response has been written (a money field that is not an amount).
func (app *application) takeMoneyFields(c *gin.Context) (sent map[string]moneyfmt.Money, ok bool) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, httpx.MaxBytesLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		app.badRequestResponse(c, err)
		return nil, false
	}
	restore := func(b []byte) { c.Request.Body = io.NopCloser(bytes.NewReader(b)) }
	restore(raw)

	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, true
	}
	sent = map[string]moneyfmt.Money{}
	for _, name := range moneyFieldNames {
		value, present := fields[name]
		if !present {
			continue
		}
		var m moneyfmt.Money
		if err := json.Unmarshal(value, &m); err != nil {
			app.failedValidationResponse(c, map[string]string{name: "must be an amount"})
			return nil, false
		}
		sent[name] = m
		delete(fields, name)
	}
	if len(sent) == 0 {
		return nil, true
	}
	rest, err := json.Marshal(fields)
	if err != nil {
		app.serverErrorResponse(c, err)
		return nil, false
	}
	restore(rest)
	return sent, true
}

// guardUnitMoneyFields is the guard for the unit routes (PATCH /units/:id,
// POST /properties/:id/units). A unit has no money of its own: rent and deposits
// live on its lease. So the unit's "current" amount for each field is its active
// lease's (zero for a new or vacant unit), and:
//
//	absent, or equal to what is there  -> fine (the fields are dropped)
//	changed, caller below accountant   -> 403, nothing written
//	changed, accountant or above       -> 422: set it on the lease, not the unit
//
// It reports false when a response has been written.
func (app *application) guardUnitMoneyFields(c *gin.Context, current func() (map[string]moneyfmt.Money, error)) bool {
	sent, ok := app.takeMoneyFields(c)
	if !ok {
		return false
	}
	if len(sent) == 0 {
		return true
	}
	now := map[string]moneyfmt.Money{}
	if current != nil {
		var err error
		if now, err = current(); err != nil {
			app.billingErrorResponse(c, err)
			return false
		}
	}
	var changes []moneyChange
	for name, value := range sent {
		changes = append(changes, moneyChange{Field: name, Sent: value, Current: now[name]})
	}
	if app.refuseMoneyChange(c, changes) {
		return false
	}
	errs := map[string]string{}
	for _, ch := range changes {
		if ch.changed() {
			errs[ch.Field] = "is set on the lease (PATCH /v1/leases/:id) or the rent schedule, not on the unit"
		}
	}
	if len(errs) > 0 {
		app.failedValidationResponse(c, errs)
		return false
	}
	return true
}

// activeLeaseMoney is the money fields of a unit's active lease, keyed by request
// name; empty for a vacant unit.
func (app *application) activeLeaseMoney(ctx context.Context, tenantID, unitID uuid.UUID) (map[string]moneyfmt.Money, error) {
	leases, err := app.models.Leases.ListForUnit(ctx, tenantID, unitID)
	if err != nil {
		return nil, err
	}
	out := map[string]moneyfmt.Money{}
	for _, l := range leases {
		if l.Status != data.LeaseStatusActive {
			continue
		}
		out["rent_amount"] = l.RentAmount
		out["rent_deposit_amount"] = l.RentDepositAmount
		out["water_deposit_amount"] = l.WaterDepositAmount
		out["electricity_deposit_amount"] = l.ElectricityDepositAmount
		break
	}
	return out, nil
}
