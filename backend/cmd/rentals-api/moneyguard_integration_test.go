package main

import (
	"net/http"
	"testing"
)

// The money-field guard (D1, c4-matrix-fixes.md item 4), against real Postgres and
// the real router: an officer cannot change what a tenant is charged through a
// route they can reach, a refusal writes nothing at all, and echoing the current
// value (a full-object form) is fine.

func (r *pendingRig) unitCode(t *testing.T) string {
	return r.scalar(t, `SELECT unit_code FROM units WHERE id = $1`, r.unitB.ID)
}

func (r *pendingRig) leaseRent(t *testing.T) string {
	return r.scalar(t, `SELECT rent_amount::text FROM leases WHERE id = $1`, r.leaseB.ID)
}

func (r *pendingRig) unitCount(t *testing.T) string {
	return r.scalar(t, `SELECT count(*)::text FROM units WHERE property_id = $1`, r.propB.ID)
}

func TestMoneyGuard_UnitPatch(t *testing.T) {
	r := newPendingRig(t)
	target := "/v1/units/" + r.unitB.ID.String()
	rentBefore := r.leaseRent(t)

	// A changed rent from an officer: 403, and NOTHING of the request is applied,
	// not even the unit code that was fine on its own.
	if code, body := r.send(http.MethodPatch, target, `{"unit_code":"ZZ9","rent_amount":"1"}`, r.officer); code != http.StatusForbidden {
		t.Fatalf("officer changing the rent = %d %s, want 403", code, body)
	}
	if got := r.unitCode(t); got != "B1" {
		t.Errorf("unit code = %q after a refused request, want B1 (no partial apply)", got)
	}
	if got := r.leaseRent(t); got != rentBefore {
		t.Errorf("lease rent = %s, want %s", got, rentBefore)
	}

	// Echoing the current rent is fine (a full-object form), and the label change goes through.
	if code, body := r.send(http.MethodPatch, target, `{"unit_code":"B1-EAST","rent_amount":"10000"}`, r.officer); code != http.StatusOK {
		t.Fatalf("officer echoing the rent = %d %s, want 200", code, body)
	}
	if got := r.unitCode(t); got != "B1-EAST" {
		t.Errorf("unit code = %q, want B1-EAST", got)
	}
	if got := r.leaseRent(t); got != rentBefore {
		t.Errorf("echoing the rent changed it to %s", got)
	}

	// An accountant may hold the authority, but a unit has no rent of its own: the
	// answer says where it is set, and nothing changes.
	code, body := r.send(http.MethodPatch, target, `{"unit_code":"B1-WEST","rent_amount":"1"}`, r.accountant)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("accountant sending a rent to the unit route = %d %s, want 422", code, body)
	}
	if got := r.unitCode(t); got != "B1-EAST" {
		t.Errorf("unit code = %q after a rejected request, want B1-EAST", got)
	}
	if got := r.leaseRent(t); got != rentBefore {
		t.Errorf("lease rent = %s, want %s", got, rentBefore)
	}

	// A label alone is the officer's to edit; a viewer edits nothing.
	if code, body := r.send(http.MethodPatch, target, `{"meter_number":"M-5"}`, r.officer); code != http.StatusOK {
		t.Errorf("officer editing the meter number = %d %s, want 200", code, body)
	}
	if code, _ := r.send(http.MethodPatch, target, `{"meter_number":"M-6"}`, r.viewer); code != http.StatusForbidden {
		t.Errorf("viewer editing the meter number = %d, want 403", code)
	}
	// An unknown field is still an unknown field.
	if code, _ := r.send(http.MethodPatch, target, `{"colour":"red"}`, r.officer); code != http.StatusBadRequest {
		t.Errorf("an unknown field = %d, want 400", code)
	}
}

func TestMoneyGuard_UnitCreate(t *testing.T) {
	r := newPendingRig(t)
	create := "/v1/properties/" + r.propB.ID.String() + "/units"
	count := r.unitCount(t)

	if code, body := r.send(http.MethodPost, create, `{"unit_code":"N1","rent_amount":"8000"}`, r.officer); code != http.StatusForbidden {
		t.Fatalf("officer creating a unit with a rent = %d %s, want 403", code, body)
	}
	if got := r.unitCount(t); got != count {
		t.Errorf("a refused creation left a unit behind (%s -> %s)", count, got)
	}
	if code, body := r.send(http.MethodPost, create, `{"unit_code":"N1"}`, r.officer); code != http.StatusCreated {
		t.Errorf("officer creating a unit with no money fields = %d %s, want 201", code, body)
	}
	if code, body := r.send(http.MethodPost, create, `{"unit_code":"N2","rent_amount":"0"}`, r.officer); code != http.StatusCreated {
		t.Errorf("officer creating a unit with a zero rent = %d %s, want 201 (zero is no money)", code, body)
	}
	if code, body := r.send(http.MethodPost, create, `{"unit_code":"N3","rent_amount":"8000"}`, r.accountant); code != http.StatusUnprocessableEntity {
		t.Errorf("accountant sending a rent to the unit route = %d %s, want 422 (rent is set on the lease)", code, body)
	}
}

func TestMoneyGuard_WaterRate(t *testing.T) {
	r := newPendingRig(t)
	water := "/v1/properties/" + r.propB.ID.String() + "/water?period=2026-09"
	reading := func(rate string) string {
		return `{"readings":[{"unit_id":"` + r.unitB.ID.String() + `","current_reading":"20"` + rate + `}]}`
	}
	savedReadings := func() string {
		return r.scalar(t, `SELECT count(*)::text FROM water_readings WHERE unit_id = $1`, r.unitB.ID)
	}

	if code, body := r.send(http.MethodPut, water, reading(`,"rate":"1"`), r.officer); code != http.StatusForbidden {
		t.Fatalf("officer setting a water rate of 1 = %d %s, want 403", code, body)
	}
	if got := savedReadings(); got != "0" {
		t.Errorf("a refused water save wrote %s readings, want 0", got)
	}
	// The property's rate (150), echoed as the grid form does, and no rate at all, are fine.
	if code, body := r.send(http.MethodPut, water, reading(`,"rate":"150"`), r.officer); code != http.StatusOK {
		t.Errorf("officer echoing the water rate = %d %s, want 200", code, body)
	}
	if code, body := r.send(http.MethodPut, water, reading(""), r.officer); code != http.StatusOK {
		t.Errorf("officer saving a reading with no rate = %d %s, want 200", code, body)
	}
	// An accountant may set a rate for a bill.
	if code, body := r.send(http.MethodPut, water, reading(`,"rate":"175"`), r.accountant); code != http.StatusOK {
		t.Errorf("accountant setting a water rate = %d %s, want 200", code, body)
	}
	// ...which is then the rate in force, so an officer echoing it is fine and
	// sending the old one is a change.
	if code, body := r.send(http.MethodPut, water, reading(`,"rate":"175"`), r.officer); code != http.StatusOK {
		t.Errorf("officer echoing the new rate = %d %s, want 200", code, body)
	}
	if code, _ := r.send(http.MethodPut, water, reading(`,"rate":"150"`), r.officer); code != http.StatusForbidden {
		t.Errorf("officer sending the old rate after it changed = %d, want 403", code)
	}
}
