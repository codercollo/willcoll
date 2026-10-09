package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	platformdata "github.com/codercollo/willcoll/backend/internal/platform/data"
	platformsqlc "github.com/codercollo/willcoll/backend/internal/platform/db/sqlc"
	"github.com/google/uuid"
)

// Hand-entered payments by someone who cannot confirm them (c4-matrix-fixes.md
// item 3), against real Postgres and the real router: an officer's "cash
// received" is pending until an accountant (or the owner) confirms it, posts
// nothing before that, and is never confirmed by the person who typed it.

type pendingRig struct {
	*scopeHTTP
	officerID, accountantID uuid.UUID
	officer, accountant     string // Cookie headers
	viewer, scopedOfficer   string
	firmLogin               string
}

func newPendingRig(t *testing.T) *pendingRig {
	t.Helper()
	s := newScopeHTTP(t)
	r := &pendingRig{scopeHTTP: s}
	r.officerID, r.officer = s.stub.newStaff(t, s.firm, platformdata.RoleOfficer, nil)
	r.accountantID, r.accountant = s.stub.newStaff(t, s.firm, platformdata.RoleAccountant, nil)
	_, r.viewer = s.stub.newStaff(t, s.firm, platformdata.RoleViewer, nil)
	_, r.scopedOfficer = s.stub.newStaff(t, s.firm, platformdata.RoleOfficer, map[string][]uuid.UUID{"rentals": {s.propA.ID}})
	r.firmLogin = s.stub.loginManager(t, s.firm)
	return r
}

func (r *pendingRig) send(method, target, payload, cookie string) (int, string) {
	code, body, _ := r.matrixCall(method, target, payload, matrixCreds{cookie: cookie})
	return code, body
}

// record posts a cash payment of amount to unit B1's rent and returns its id and the
// status the API reports.
func (r *pendingRig) record(t *testing.T, cookie, amount string) (id uuid.UUID, status string, code int) {
	t.Helper()
	code, body := r.send(http.MethodPost, "/v1/units/"+r.unitB.ID.String()+"/rent/payments",
		`{"amount":"`+amount+`","source":"manual","reference":"R-`+uuid.NewString()[:8]+`","ledger_type":"rent"}`, cookie)
	if code != http.StatusCreated {
		return uuid.Nil, "", code
	}
	var out struct {
		Payment struct {
			PaymentID uuid.UUID `json:"payment_id"`
			Status    string    `json:"status"`
		} `json:"payment"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("payment response: %v: %s", err, body)
	}
	return out.Payment.PaymentID, out.Payment.Status, code
}

func (r *pendingRig) scalar(t *testing.T, query string, args ...any) string {
	t.Helper()
	var out sql.NullString
	tenantSQL(t, r.conn, r.firm, func(tx *sql.Tx) {
		if err := tx.QueryRow(query, args...).Scan(&out); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	})
	return out.String
}

func (r *pendingRig) rentBalance(t *testing.T) string {
	return r.scalar(t, `SELECT balance::text FROM unit_ledger_balances WHERE unit_id = $1 AND type = 'RENT'`, r.unitB.ID)
}

func (r *pendingRig) receipts(t *testing.T) string {
	return r.scalar(t, `SELECT count(*)::text FROM receipts WHERE unit_id = $1`, r.unitB.ID)
}

func (r *pendingRig) allocations(t *testing.T, payment uuid.UUID) string {
	return r.scalar(t, `SELECT count(*)::text FROM payment_allocations WHERE payment_id = $1`, payment)
}

func (r *pendingRig) paymentColumn(t *testing.T, payment uuid.UUID, column string) string {
	return r.scalar(t, `SELECT `+column+`::text FROM payments WHERE id = $1`, payment)
}

// reviewIDs lists the payment ids the review queue shows the cookie's holder.
func (r *pendingRig) reviewIDs(t *testing.T, cookie string) (ids map[uuid.UUID]string, summary struct{ Pending, Stale int }) {
	t.Helper()
	code, body := r.send(http.MethodGet, "/v1/payments/review?page_size=100", "", cookie)
	if code != http.StatusOK {
		t.Fatalf("review queue = %d %s", code, body)
	}
	var out struct {
		Payments []struct {
			ID             uuid.UUID `json:"id"`
			Status         string    `json:"status"`
			RecordedByRole *string   `json:"recorded_by_role"`
		} `json:"payments"`
		Summary struct {
			Pending int `json:"pending"`
			Stale   int `json:"pending_over_48h"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	ids = map[uuid.UUID]string{}
	for _, p := range out.Payments {
		role := ""
		if p.RecordedByRole != nil {
			role = *p.RecordedByRole
		}
		ids[p.ID] = p.Status + "/" + role
	}
	return ids, struct{ Pending, Stale int }{out.Summary.Pending, out.Summary.Stale}
}

// 1 + 2 + 3: an officer's payment waits, an accountant's confirmation posts it
// exactly once.
func TestPendingPayment_OfficerRecordsAccountantConfirmsOnce(t *testing.T) {
	r := newPendingRig(t)
	balanceBefore, receiptsBefore := r.rentBalance(t), r.receipts(t)

	id, status, code := r.record(t, r.officer, "700")
	if code != http.StatusCreated || status != "pending" {
		t.Fatalf("officer's payment = %d %q, want 201 pending", code, status)
	}
	// Nothing posted: no allocation, no ledger entry, no balance, no receipt.
	if got := r.allocations(t, id); got != "0" {
		t.Errorf("a pending payment has %s allocations, want 0", got)
	}
	if got := r.rentBalance(t); got != balanceBefore {
		t.Errorf("rent balance moved from %s to %s while the payment was pending", balanceBefore, got)
	}
	if got := r.receipts(t); got != receiptsBefore {
		t.Errorf("receipts went from %s to %s while the payment was pending", receiptsBefore, got)
	}
	if got := r.paymentColumn(t, id, "status"); got != "pending" {
		t.Errorf("stored status = %q, want pending", got)
	}
	if got := r.paymentColumn(t, id, "recorded_by"); got != r.officerID.String() {
		t.Errorf("recorded_by = %s, want the officer %s", got, r.officerID)
	}
	// It is in the review list, as a pending payment recorded by an officer.
	ids, summary := r.reviewIDs(t, r.accountant)
	if ids[id] != "pending/officer" || summary.Pending != 1 {
		t.Errorf("review list has %q (pending count %d) for the officer's payment, want pending/officer and 1", ids[id], summary.Pending)
	}

	// The officer cannot confirm it (no payments.allocate), and the accountant can.
	if code, _ := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.officer); code != http.StatusForbidden {
		t.Errorf("officer confirming = %d, want 403", code)
	}
	if code, body := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.accountant); code != http.StatusOK {
		t.Fatalf("accountant confirming = %d %s, want 200", code, body)
	}
	if got := r.allocations(t, id); got != "1" {
		t.Errorf("after confirmation: %s allocations, want 1", got)
	}
	if got := r.rentBalance(t); got == balanceBefore {
		t.Errorf("rent balance did not move on confirmation (still %s)", got)
	}
	if got := r.receipts(t); got == receiptsBefore {
		t.Errorf("no receipt was issued on confirmation (still %s)", got)
	}
	if got := r.paymentColumn(t, id, "confirmed_by"); got != r.accountantID.String() {
		t.Errorf("confirmed_by = %s, want the accountant %s", got, r.accountantID)
	}
	if got := r.paymentColumn(t, id, "confirmed_at"); got == "" {
		t.Error("confirmed_at was not recorded")
	}
	if got := r.paymentColumn(t, id, "status"); got != "allocated" {
		t.Errorf("stored status after confirmation = %q, want allocated", got)
	}
	balanceAfter := r.rentBalance(t)

	// Confirming again is a 409 and posts nothing more: one ledger entry only.
	if code, _ := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.accountant); code != http.StatusConflict {
		t.Errorf("second confirmation = %d, want 409", code)
	}
	if code, _ := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.firmLogin); code != http.StatusConflict {
		t.Errorf("second confirmation by the firm login = %d, want 409", code)
	}
	if got := r.allocations(t, id); got != "1" {
		t.Errorf("after confirming twice: %s allocations, want 1", got)
	}
	if got := r.rentBalance(t); got != balanceAfter {
		t.Errorf("a second confirmation moved the balance from %s to %s", balanceAfter, got)
	}
	if got := r.scalar(t, `SELECT count(*)::text FROM ledger_entries le JOIN payment_allocations pa ON pa.ledger_entry_id = le.id WHERE pa.payment_id = $1`, id); got != "1" {
		t.Errorf("%s ledger entries for the payment, want exactly 1", got)
	}
	if ids, _ := r.reviewIDs(t, r.accountant); ids[id] != "" {
		t.Errorf("a confirmed payment is still in the review list: %q", ids[id])
	}
}

// 4: a rejection posts nothing and alerts the owner, atomically with the decision.
func TestPendingPayment_RejectPostsNothingAndAlertsTheOwner(t *testing.T) {
	r := newPendingRig(t)
	balanceBefore, receiptsBefore := r.rentBalance(t), r.receipts(t)
	id, status, _ := r.record(t, r.officer, "900")
	if status != "pending" {
		t.Fatalf("status = %q, want pending", status)
	}

	reject := "/v1/payments/" + id.String() + "/reject"
	if code, _ := r.send(http.MethodPost, reject, `{}`, r.accountant); code != http.StatusUnprocessableEntity {
		t.Errorf("rejecting without a reason = %d, want 422", code)
	}
	if code, _ := r.send(http.MethodPost, reject, `{"reason":"no cash was handed over"}`, r.officer); code != http.StatusForbidden {
		t.Errorf("an officer rejecting = %d, want 403", code)
	}
	if code, body := r.send(http.MethodPost, reject, `{"reason":"no cash was handed over"}`, r.accountant); code != http.StatusOK {
		t.Fatalf("rejecting = %d %s, want 200", code, body)
	}
	if got := r.paymentColumn(t, id, "status"); got != "rejected" {
		t.Errorf("stored status = %q, want rejected", got)
	}
	if got := r.paymentColumn(t, id, "reject_reason"); got != "no cash was handed over" {
		t.Errorf("reject_reason = %q", got)
	}
	if got := r.paymentColumn(t, id, "rejected_by"); got != r.accountantID.String() {
		t.Errorf("rejected_by = %s, want the accountant", got)
	}
	if got := r.allocations(t, id); got != "0" {
		t.Errorf("a rejected payment has %s allocations, want 0", got)
	}
	if got := r.rentBalance(t); got != balanceBefore {
		t.Errorf("rent balance moved from %s to %s on a rejection", balanceBefore, got)
	}
	if got := r.receipts(t); got != receiptsBefore {
		t.Errorf("a rejection issued a receipt (%s -> %s)", receiptsBefore, got)
	}
	// The owner alert is queued, once, in the same transaction as the decision.
	if got := r.scalar(t, `SELECT count(*)::text FROM alert_outbox WHERE tenant_id = $1 AND event_key = $2`, r.firm, "payment.rejected:"+id.String()); got != "1" {
		t.Errorf("%s owner alerts for the rejection, want 1", got)
	}
	// It is gone from the queue, cannot be confirmed or rejected again.
	if ids, _ := r.reviewIDs(t, r.accountant); ids[id] != "" {
		t.Errorf("a rejected payment is still in the review list: %q", ids[id])
	}
	if code, _ := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.accountant); code != http.StatusConflict {
		t.Errorf("confirming a rejected payment = %d, want 409", code)
	}
	if code, _ := r.send(http.MethodPost, reject, `{"reason":"again"}`, r.accountant); code != http.StatusConflict {
		t.Errorf("rejecting twice = %d, want 409", code)
	}
	if got := r.scalar(t, `SELECT count(*)::text FROM alert_outbox WHERE tenant_id = $1 AND event_key = $2`, r.firm, "payment.rejected:"+id.String()); got != "1" {
		t.Errorf("a second rejection attempt queued another alert (%s)", got)
	}
}

// A reference is entered once; a rejected entry frees it so the clerk can enter
// the payment again once it is put right.
func TestPendingPayment_RejectedReferenceCanBeEnteredAgain(t *testing.T) {
	r := newPendingRig(t)
	post := func() (int, string) {
		return r.send(http.MethodPost, "/v1/units/"+r.unitB.ID.String()+"/rent/payments",
			`{"amount":"900","source":"manual","reference":"CASHBOOK-77","ledger_type":"rent"}`, r.officer)
	}
	code, body := post()
	if code != http.StatusCreated {
		t.Fatalf("first entry = %d %s", code, body)
	}
	if code, _ := post(); code != http.StatusConflict {
		t.Errorf("the same reference while the first is pending = %d, want 409", code)
	}
	var out struct {
		Payment struct {
			PaymentID uuid.UUID `json:"payment_id"`
		} `json:"payment"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if code, b := r.send(http.MethodPost, "/v1/payments/"+out.Payment.PaymentID.String()+"/reject", `{"reason":"wrong amount"}`, r.accountant); code != http.StatusOK {
		t.Fatalf("reject = %d %s", code, b)
	}
	if code, b := post(); code != http.StatusCreated {
		t.Errorf("the reference after a rejection = %d %s, want 201", code, b)
	}
}

// 5 + 6: scope and role.
func TestPendingPayment_ScopeAndViewer(t *testing.T) {
	r := newPendingRig(t)
	unitB := "/v1/units/" + r.unitB.ID.String() + "/rent/payments"
	body := `{"amount":"100","source":"manual","reference":"X-` + uuid.NewString()[:8] + `","ledger_type":"rent"}`

	if code, _ := r.send(http.MethodPost, unitB, body, r.scopedOfficer); code != http.StatusNotFound {
		t.Errorf("an officer scoped to property A recording against a property B unit = %d, want 404", code)
	}
	if code, _ := r.send(http.MethodPost, unitB, body, r.viewer); code != http.StatusForbidden {
		t.Errorf("a viewer recording a payment = %d, want 403", code)
	}

	// A pending payment on property B is invisible to the property-A officer, and
	// they cannot decide it either.
	id, _, _ := r.record(t, r.officer, "300")
	if ids, _ := r.reviewIDs(t, r.scopedOfficer); ids[id] != "" || len(ids) != 0 {
		t.Errorf("the property-A officer sees property B's payments: %v", ids)
	}
	if code, _ := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.scopedOfficer); code != http.StatusNotFound {
		t.Errorf("a property-A officer confirming a property B payment = %d, want 404", code)
	}
	if ids, summary := r.reviewIDs(t, r.officer); ids[id] != "pending/officer" || summary.Pending != 1 {
		t.Errorf("the unscoped officer sees %v (pending %d)", ids, summary.Pending)
	}
}

// Whoever records a payment never confirms it, even after a promotion.
func TestPendingPayment_RecorderCannotConfirmTheirOwnPayment(t *testing.T) {
	r := newPendingRig(t)
	id, _, _ := r.record(t, r.officer, "450")

	r.stub.editStaff(r.officerID, func(st *platformsqlc.Staff) { st.Role = platformdata.RoleAccountant }) // promoted
	if code, body := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.officer); code != http.StatusForbidden {
		t.Fatalf("a promoted officer confirming their own payment = %d %s, want 403", code, body)
	}
	if got := r.allocations(t, id); got != "0" {
		t.Errorf("the refused self-confirmation posted (%s allocations)", got)
	}
	if code, body := r.send(http.MethodPost, "/v1/payments/"+id.String()+"/confirm", "", r.accountant); code != http.StatusOK {
		t.Fatalf("another accountant confirming = %d %s, want 200", code, body)
	}
}

// An accountant's, an owner's and the firm login's payments post at once, as before.
func TestPendingPayment_AccountantAndOwnerPostAtOnce(t *testing.T) {
	r := newPendingRig(t)
	for name, cookie := range map[string]string{"accountant": r.accountant, "firm login": r.firmLogin} {
		before := r.rentBalance(t)
		id, status, code := r.record(t, cookie, "250")
		if code != http.StatusCreated || status != "allocated" {
			t.Errorf("%s: %d %q, want 201 allocated", name, code, status)
		}
		if got := r.allocations(t, id); got != "1" {
			t.Errorf("%s: %s allocations, want 1", name, got)
		}
		if got := r.rentBalance(t); got == before {
			t.Errorf("%s: the balance did not move", name)
		}
	}
}

// A pending payment older than 48 hours is counted for the dashboard.
func TestPendingPayment_StaleCountForTheDashboard(t *testing.T) {
	r := newPendingRig(t)
	old, _, _ := r.record(t, r.officer, "120")
	r.record(t, r.officer, "130")
	tenantSQL(t, r.conn, r.firm, func(tx *sql.Tx) {
		if _, err := tx.Exec(`UPDATE payments SET created_at = now() - interval '3 days' WHERE id = $1`, old); err != nil {
			t.Fatal(err)
		}
	})
	if _, summary := r.reviewIDs(t, r.accountant); summary.Pending != 2 || summary.Stale != 1 {
		t.Errorf("summary = %+v, want 2 pending and 1 over 48 hours", summary)
	}
}
