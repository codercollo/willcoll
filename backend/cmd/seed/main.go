// Command seed fills the development database with a realistic firm so the
// frontend has real-life data to work against: THE RUNDA'S ARCADE and KIWI
// PLACE (the two documents the product digitises), three months of billing
// history, and every kind of payment the reconciliation engine handles.
//
// It writes ONLY through the service layer (internal/data), the same code the
// API runs, so ledgers, balances, receipts and reports are consistent by
// construction, not hand-inserted. Run it with `make db/seed`. It refuses to
// run twice; `make db/seed ARGS=-reset` removes the demo firm first.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/codercollo/willcoll/backend/internal/data"
	"github.com/codercollo/willcoll/backend/internal/payhero"
	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	demoEmail    = "demo@willcoll.test"
	demoPassword = "Demo-Password-1"
	demoUsername = "kimani-associates"
)

func main() {
	reset := flag.Bool("reset", false, "remove the demo firm and everything under it first (needs MIGRATE_DSN)")
	flag.Parse()

	appDSN := os.Getenv("DB_DSN")
	if appDSN == "" {
		log.Fatal("DB_DSN is not set (run through `make db/seed`, which loads backend/.env)")
	}
	adminDSN := os.Getenv("DB_ADMIN_DSN")
	if adminDSN == "" {
		adminDSN = deriveAdminDSN(appDSN, os.Getenv("DB_ADMIN_PASSWORD"))
	}
	if adminDSN == "" {
		log.Fatal("set DB_ADMIN_DSN, or DB_ADMIN_PASSWORD to derive it from DB_DSN")
	}

	app, admin := mustOpen(appDSN), mustOpen(adminDSN)
	defer app.Close()
	defer admin.Close()
	models := data.NewModels(app, admin, 30*time.Second)
	ctx := context.Background()

	if *reset {
		resetDemo(ctx)
	}
	if exists, err := demoExists(ctx, app); err != nil {
		log.Fatal(err)
	} else if exists {
		log.Fatalf("the demo firm %s already exists: run `make db/seed ARGS=-reset` to replace it", demoEmail)
	}

	s := newSeeder(ctx, models)
	s.run()
	backdateLedgerTimestamps(ctx, s.tenant)
	s.summary()
}

// backdateLedgerTimestamps fixes a gap the service layer can't: every
// charge, ledger entry, transaction header and payment allocation is
// created with the real wall-clock time (append-only ledger, ADR 0002 —
// correct for production, where a posting's created_at IS the moment it
// happened), but this seeder writes months of backdated demo history in a
// single run today. Left alone, every "AsOf" balance snapshot (the NOTE:2
// arrears/advance derivation, e.g.) sees zero entries for any past period
// boundary, since every entry's created_at is today, not its nominal
// month. Run once after seeding, as the owner role (MIGRATE_DSN, same as
// resetDemo): the ledger tables' append-only REVOKE blocks UPDATE for both
// willcoll_app and willcoll_admin, and only the owner can write here.
// Nothing in cmd/api or internal/data reads or writes through this path,
// so production behavior is unaffected.
func backdateLedgerTimestamps(ctx context.Context, tenantID uuid.UUID) {
	dsn := os.Getenv("MIGRATE_DSN")
	if dsn == "" {
		log.Fatal("backdating demo timestamps needs MIGRATE_DSN (the owner role)")
	}
	owner := mustOpen(dsn)
	defer owner.Close()

	stmts := []string{
		// Charge-sourced entries: the charge's own period, offset a day in
		// so it reads as "posted during the month," not on the 1st.
		`UPDATE ledger_entries le SET created_at = c.period + interval '1 day'
		 FROM charges c
		 WHERE le.reference_type = 'charge' AND le.reference_id = c.id AND le.tenant_id = $1`,
		`UPDATE charges SET created_at = period + interval '1 day' WHERE tenant_id = $1`,
		// Payment-sourced entries: the payment's own received_at, already
		// correctly historical (see the seeder's at() helper).
		`UPDATE ledger_entries le SET created_at = p.received_at
		 FROM payment_allocations pa JOIN payments p ON p.id = pa.payment_id
		 WHERE le.reference_type = 'payment_allocation' AND le.reference_id = pa.id AND le.tenant_id = $1`,
		`UPDATE payment_allocations pa SET created_at = p.received_at
		 FROM payments p WHERE pa.payment_id = p.id AND pa.tenant_id = $1`,
		// Transaction headers: the earliest of the entries they now cover,
		// so a header never claims to predate its own entries.
		`UPDATE transaction_headers th SET created_at = sub.at
		 FROM (SELECT transaction_header_id AS id, min(created_at) AS at FROM ledger_entries WHERE tenant_id = $1 GROUP BY transaction_header_id) sub
		 WHERE th.id = sub.id`,
	}
	for _, stmt := range stmts {
		if _, err := owner.ExecContext(ctx, stmt, tenantID); err != nil {
			log.Fatalf("backdating ledger timestamps: %v", err)
		}
	}
}

func mustOpen(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("cannot reach the database: %v", err)
	}
	return db
}

func deriveAdminDSN(appDSN, adminPassword string) string {
	if adminPassword == "" {
		return ""
	}
	u, err := url.Parse(appDSN)
	if err != nil || u.Scheme == "" {
		return ""
	}
	u.User = url.UserPassword("willcoll_admin", adminPassword)
	return u.String()
}

// demoExists checks for the demo firm by email. The managers table is not under
// row-level security, so the API role can read it directly.
func demoExists(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM managers WHERE email = $1`, demoEmail).Scan(&n)
	return n > 0, err
}

// resetDemo deletes the demo firm as the owner role: the ledger tables are
// append-only for the API role, and removing a firm cascades through them.
func resetDemo(ctx context.Context) {
	dsn := os.Getenv("MIGRATE_DSN")
	if dsn == "" {
		log.Fatal("-reset needs MIGRATE_DSN (the owner role)")
	}
	db := mustOpen(dsn)
	defer db.Close()
	res, err := db.ExecContext(ctx, `DELETE FROM managers WHERE email = $1`, demoEmail)
	if err != nil {
		log.Fatalf("reset failed: %v", err)
	}
	n, _ := res.RowsAffected()
	fmt.Printf("reset: removed %d demo firm(s)\n", n)
}

// ---------------------------------------------------------------------------

var eat = time.FixedZone("EAT", 3*3600)

type seeder struct {
	ctx    context.Context
	m      data.Models
	rng    *lcg
	tenant uuid.UUID

	receipts int
	book     int
	props    []*property
	report   []string
}

// lcg is a tiny deterministic generator, so every seed produces the same firm.
type lcg struct{ s uint64 }

func (r *lcg) next() uint64 { r.s = r.s*6364136223846793005 + 1442695040888963407; return r.s >> 33 }
func (r *lcg) n(k int) int  { return int(r.next() % uint64(k)) }

func newSeeder(ctx context.Context, m data.Models) *seeder {
	return &seeder{ctx: ctx, m: m, rng: &lcg{s: 20260926}}
}

func (s *seeder) must(err error, what string) {
	if err != nil {
		log.Fatalf("seeding failed at %s: %v", what, err)
	}
}

func money(shillings int64) moneyfmt.Money { return moneyfmt.FromCents(shillings * 100) }
func moneyPtr(shillings int64) *moneyfmt.Money { m := money(shillings); return &m }

// at is a moment in the given month, in Kenya time, never in the future.
func at(p moneyfmt.Period, day, hour int) time.Time {
	t := time.Date(p.Year, p.Month, day, hour, 0, 0, 0, eat)
	if now := time.Now(); t.After(now) {
		t = now.Add(-time.Hour)
	}
	return t.UTC()
}

// rcpt is the next number in the office receipt book.
func (s *seeder) rcpt() string {
	s.book++
	return fmt.Sprintf("RCPT-%d", 2790+s.book)
}

func (s *seeder) mpesa() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ0123456789"
	b := make([]byte, 9)
	for i := range b {
		b[i] = alphabet[s.rng.n(len(alphabet))]
	}
	s.receipts++
	return "S" + string(b)
}

// ---------------------------------------------------------------------------

type tenantSpec struct {
	unit     string
	name     string
	phone    string
	rent     int64
	rentDep  int64
	waterDep int64
	startAgo int // months before the current one the lease started
	payers   []data.LeasePayer
	pattern  string
}

type unitState struct {
	unit    *data.Unit
	lease   *data.Lease
	spec    tenantSpec
	reading int64 // running meter reading
}

type property struct {
	p       *data.Property
	slug    string
	channel string
	units   []*unitState
	vacant  []string
	garbage bool
	target  func(u *unitState) data.PayTarget
}

func ptr(s string) *string { return &s }

func (s *seeder) run() {
	cur := moneyfmt.NewPeriod(time.Now())
	periods := []moneyfmt.Period{cur.Prev().Prev(), cur.Prev(), cur}

	s.setupPlatform()
	s.setupFirm()
	l1, l2 := s.landlords()
	runda := s.buildRunda(l1)
	kiwi := s.buildKiwi(l2)
	s.props = []*property{runda, kiwi}

	for i, p := range periods {
		for _, pr := range s.props {
			s.billingMonth(pr, p, i, len(periods))
		}
	}
	s.confirmHistory()
	s.reviewQueue(runda, cur)
}

// ---------------------------------------------------------------------------
// Platform and firm

func (s *seeder) setupPlatform() {
	existing, err := s.m.Platform.ListPlans(s.ctx)
	s.must(err, "list plans")
	have := map[string]bool{}
	for _, p := range existing {
		have[p.Name] = true
	}
	for _, in := range []data.PlanInput{
		{Name: "Starter", Price: money(1500), BillingInterval: "monthly", UnitCap: i32(20), PricingType: data.PricingFlat, SortOrder: 10},
		{Name: "Growth", Price: money(3500), BillingInterval: "monthly", UnitCap: i32(60), PricingType: data.PricingFlat, SortOrder: 20},
		{Name: "Business", Price: money(7500), BillingInterval: "monthly", UnitCap: i32(150), PricingType: data.PricingFlat, SortOrder: 30},
		{Name: "Enterprise", BillingInterval: "monthly", PricingType: data.PricingPerUnit, PerUnitPrice: moneyPtr(45), MinPrice: moneyPtr(7500), SortOrder: 40},
	} {
		if !have[in.Name] {
			_, err := s.m.Platform.CreatePlan(s.ctx, in, false)
			s.must(err, "create plan "+in.Name)
		}
	}
}

func i32(n int32) *int32 { return &n }

func (s *seeder) setupFirm() {
	mgr := &data.Manager{
		FirmName: "Kimani & Associates Property Managers", Username: demoUsername,
		Email: demoEmail, Phone: "+254722000100", Status: data.ManagerStatusActive,
	}
	s.must(mgr.Password.Set(demoPassword), "hash password")
	s.must(s.m.Managers.Insert(s.ctx, mgr), "create manager")
	now := time.Now()
	mgr.ActivatedAt = &now
	s.must(s.m.Managers.Update(s.ctx, mgr), "activate manager")
	s.tenant = mgr.ID

	// An active Professional subscription, paid the way a real one is: an
	// invoice, then the PayHero callback settling it.
	plans, err := s.m.Platform.ListPlans(s.ctx)
	s.must(err, "list plans")
	var pro data.Plan
	for _, p := range plans {
		if p.Name == "Professional" {
			pro = p.Plan
		}
	}
	ren, err := s.m.Billing.StartRenewal(s.ctx, s.tenant, &pro.ID)
	s.must(err, "start subscription")
	_, err = s.m.Billing.ProcessSubscriptionPayment(s.ctx, &payhero.SubscriptionsWebhookPayload{
		Success: true, Reference: ren.Invoice.ID.String(), MpesaReceipt: s.mpesa(), Amount: pro.Price.String(),
	})
	s.must(err, "settle subscription")
}

func (s *seeder) landlords() (*data.Landlord, *data.Landlord) {
	l1 := &data.Landlord{Name: "Wanjiku Estates Ltd", Phone: "+254733100200", Email: ptr("accounts@wanjikuestates.co.ke"),
		BankName: ptr("KCB Bank"), BankAccountName: ptr("WANJIKU ESTATES LTD"), BankAccountNumber: ptr("1234567890")}
	l2 := &data.Landlord{Name: "Mwangi Family Trust", Phone: "+254711300400", Email: ptr("trustees@mwangitrust.co.ke"),
		BankName: ptr("Equity Bank"), BankAccountName: ptr("MWANGI FAMILY TRUST"), BankAccountNumber: ptr("0987654321")}
	s.must(s.m.Landlords.Insert(s.ctx, s.tenant, l1), "landlord 1")
	s.must(s.m.Landlords.Insert(s.ctx, s.tenant, l2), "landlord 2")
	return l1, l2
}

// ---------------------------------------------------------------------------
// The two properties

func (s *seeder) buildRunda(l *data.Landlord) *property {
	p := &data.Property{LandlordID: l.ID, Name: "THE RUNDA'S ARCADE", Location: "Runda Road, Nairobi", Slug: "runda-arcade",
		GarbageEnabled: true, GarbageFee: money(300), WaterRatePerUnit: money(150), ManagementFeePercent: 5,
		PayheroChannelID: ptr("PH-CH-RUNDA-DEMO")}
	s.must(s.m.Properties.Insert(s.ctx, s.tenant, p), "property runda")
	s.must(s.m.Properties.SetPrintTheme(s.ctx, p, &data.PrintTheme{
		HeaderText: "THE RUNDA'S ARCADE", AddressLines: []string{"P.O. Box 12345-00621, Nairobi"},
		Phone: "+254733100200", AccentColor: "#1F4E79"}), "runda theme")

	specs := []tenantSpec{
		{"G1", "JOSEPH KAMAU MWANGI", "+254722111001", 18000, 36000, 3000, 14, []data.LeasePayer{{Name: "MARY WANJIRU KAMAU", Phone: ptr("+254722111002")}}, "prompt"},
		{"G2", "GRACE NJERI WAMBUI", "+254722111003", 15000, 30000, 3000, 20, nil, "late"},
		{"G3", "PETER OMONDI OTIENO", "+254722111004", 15000, 15000, 2000, 2, nil, "arrears"},
		{"SHOP NO.1", "MAMA MBOGA ENTERPRISES", "+254722111005", 25000, 50000, 5000, 30, []data.LeasePayer{{Name: "SAMUEL MUTUA", Phone: ptr("+254722111006")}}, "organic"},
		{"SHOP NO.2", "KIWI SALON & SPA LTD", "+254722111007", 30000, 60000, 5000, 9, []data.LeasePayer{
			{Name: "ROSE ATIENO", Phone: ptr("+254722111014")}, {Name: "DAVID OCHIENG", Phone: ptr("+254722111015")}}, "prompt"},
		{"1A", "FAITH CHEBET KIPKEMBOI", "+254722111008", 12000, 12000, 2000, 11, nil, "prompt"},
		{"1B", "DAVID KIPROP KORIR", "+254722111009", 12000, 12000, 2000, 18, nil, "cheque"},
		{"1C", "AGNES MUTHONI NJOROGE", "+254722111010", 13000, 13000, 2000, 7, []data.LeasePayer{{Name: "JOHN NJOROGE", Phone: ptr("+254722111011")}}, "instalments"},
		{"2A", "HASSAN ALI ABDI", "+254722111012", 14000, 14000, 2000, 5, nil, "advance"},
		{"2B", "LUCY AKINYI ODHIAMBO", "+254722111013", 14000, 14000, 2000, 13, nil, "cash"},
	}
	return s.buildProperty(p, "RA", specs, []string{"SHOP NO.3", "2C"}, true)
}

func (s *seeder) buildKiwi(l *data.Landlord) *property {
	p := &data.Property{LandlordID: l.ID, Name: "KIWI PLACE", Location: "Kiwi Road, Kasarani, Nairobi", Slug: "kiwi-place",
		GarbageEnabled: false, WaterRatePerUnit: money(120), ManagementFeePercent: 5, PayheroChannelID: ptr("PH-CH-KIWI-DEMO")}
	s.must(s.m.Properties.Insert(s.ctx, s.tenant, p), "property kiwi")
	s.must(s.m.Properties.SetPrintTheme(s.ctx, p, &data.PrintTheme{
		HeaderText: "KIWI PLACE", AddressLines: []string{"P.O. Box 4471-00100, Nairobi"}, Phone: "+254711300400", AccentColor: "#8B1E3F"}), "kiwi theme")

	specs := []tenantSpec{
		{"A1", "EUNICE WAIRIMU GITHINJI", "+254733222001", 9000, 9000, 1500, 16, nil, "prompt"},
		{"A2", "BRIAN OTIENO ONYANGO", "+254733222002", 9500, 9500, 1500, 10, []data.LeasePayer{{Name: "CAROL ACHIENG ONYANGO", Phone: ptr("+254733222009")}}, "organic"},
		{"A3", "STELLA JEPKOECH RONO", "+254733222003", 8500, 8500, 1500, 8, nil, "late"},
		{"A4", "MOSES KARIUKI GATHERU", "+254733222004", 8500, 8500, 1500, 3, nil, "prompt"},
		{"B1", "NANCY WANGARI MACHARIA", "+254733222005", 11000, 11000, 2000, 22, nil, "prompt"},
		{"B2", "ISAAC MWENDWA MUSYOKA", "+254733222006", 11000, 11000, 2000, 6, nil, "cash"},
		{"B3", "TABITHA NYAMBURA KIMANI", "+254733222007", 10500, 10500, 2000, 12, nil, "arrears"},
	}
	return s.buildProperty(p, "KP", specs, []string{"B4"}, false)
}

func (s *seeder) buildProperty(p *data.Property, meterPrefix string, specs []tenantSpec, vacant []string, garbage bool) *property {
	pr := &property{p: p, slug: p.Slug, channel: *p.PayheroChannelID, vacant: vacant, garbage: garbage}
	cur := moneyfmt.NewPeriod(time.Now())

	for i, sp := range specs {
		u := &data.Unit{PropertyID: p.ID, UnitCode: sp.unit, MeterNumber: ptr(fmt.Sprintf("%s-W-%03d", meterPrefix, i+1)), Status: data.UnitStatusVacant}
		s.must(s.m.Units.Insert(s.ctx, s.tenant, u), "unit "+sp.unit)

		start := cur
		for k := 0; k < sp.startAgo; k++ {
			start = start.Prev()
		}
		lease := &data.Lease{UnitID: u.ID, TenantName: sp.name, PrimaryPhone: sp.phone, RentAmount: money(sp.rent),
			RentDepositAmount: money(sp.rentDep), WaterDepositAmount: money(sp.waterDep),
			StartDate: start.FirstDay().AddDate(0, 0, 0), Status: data.LeaseStatusActive, Payers: sp.payers,
			GarbageBilled: garbage}
		s.must(s.m.Leases.Insert(s.ctx, s.tenant, s.tenant, lease), "lease "+sp.unit)

		st := &unitState{unit: u, lease: lease, spec: sp, reading: int64(40 + s.rng.n(400))}
		pr.units = append(pr.units, st)
		s.payDeposits(pr, st)
	}
	for _, code := range vacant {
		u := &data.Unit{PropertyID: p.ID, UnitCode: code, Status: data.UnitStatusVacant}
		s.must(s.m.Units.Insert(s.ctx, s.tenant, u), "vacant unit "+code)
	}
	pr.target = func(u *unitState) data.PayTarget {
		return data.PayTarget{TenantID: s.tenant, PropertyID: p.ID, UnitID: u.unit.ID}
	}
	return pr
}

// payDeposits settles a lease's deposits at its start date, the way they are
// collected in real life, except for the tenants who are still catching up.
func (s *seeder) payDeposits(pr *property, u *unitState) {
	when := u.lease.StartDate.Add(10 * time.Hour)
	full := func(ledger string, amount int64, ref string) {
		if amount == 0 {
			return
		}
		s.manual(u, when, ledger, amount, "bank", ref, "deposit at lease start")
	}
	switch u.spec.pattern {
	case "arrears": // still paying the deposit off
		full(data.LedgerTypeRentDeposit, u.spec.rentDep/2, "DEP-"+u.spec.unit)
	default:
		full(data.LedgerTypeRentDeposit, u.spec.rentDep, "DEP-"+u.spec.unit)
		full(data.LedgerTypeWaterDeposit, u.spec.waterDep, "WDEP-"+u.spec.unit)
	}
}

// ---------------------------------------------------------------------------
// One billing month for one property

func (s *seeder) billingMonth(pr *property, p moneyfmt.Period, index, total int) {
	isCurrent := index == total-1
	first := time.Date(p.Year, p.Month, 1, 8, 0, 0, 0, eat)
	s.m.Rent.Now = func() time.Time { return first }

	// A rent change is picked up by the next run, never by a past one.
	if isCurrent && pr.slug == "runda-arcade" {
		for _, u := range pr.units {
			if u.spec.unit == "G2" {
				s.must(s.m.Rent.BulkSetExpectedRent(s.ctx, s.tenant, pr.p.ID, []data.UnitRentInput{{UnitID: u.unit.ID, RentAmount: money(16000)}}), "rent change")
				u.spec.rent = 16000
			}
		}
	}
	_, err := s.m.Rent.GenerateForPeriod(s.ctx, s.tenant, s.tenant, pr.p.ID, p)
	s.must(err, "rent run "+p.String())

	// Water: readings at month end. The current month is left as a DRAFT, the
	// way the Water tab looks mid-month, with a couple of units still to read.
	inputs := make([]data.WaterReadingInput, 0, len(pr.units))
	for i, u := range pr.units {
		if isCurrent && (i%5 == 4) {
			continue // not read yet
		}
		use := int64(5 + s.rng.n(19))
		in := data.WaterReadingInput{UnitID: u.unit.ID, CurrentReading: money(u.reading + use)}
		if index == 0 {
			prev := money(u.reading)
			in.PreviousReading = &prev
		}
		u.reading += use
		inputs = append(inputs, in)
	}
	s.must(s.m.Water.SaveReadings(s.ctx, s.tenant, s.tenant, pr.p.ID, p, inputs), "water readings "+p.String())
	if !isCurrent {
		_, err := s.m.Water.Generate(s.ctx, s.tenant, s.tenant, pr.p.ID, p)
		s.must(err, "water run "+p.String())
		if pr.garbage {
			_, err := s.m.Garbage.Generate(s.ctx, s.tenant, s.tenant, pr.p.ID, p)
			s.must(err, "garbage run "+p.String())
		}
	}

	for _, u := range pr.units {
		s.payMonth(pr, u, p, index, isCurrent)
	}
}

func (s *seeder) payMonth(pr *property, u *unitState, p moneyfmt.Period, index int, isCurrent bool) {
	spec := u.spec
	rent := money(spec.rent)
	types := []string{data.LedgerTypeRent, data.LedgerTypeWater, data.LedgerTypeGarbage}
	if !pr.garbage {
		types = types[:2]
	}

	switch spec.pattern {
	case "prompt": // pays everything through the app, early in the month
		s.byIntent(pr, u, at(p, 3+s.rng.n(4), 9+s.rng.n(8)), types...)

	case "late": // pays rent late; the current month is still unpaid
		if !isCurrent {
			s.byIntent(pr, u, at(p, 18+s.rng.n(8), 15), types...)
		}

	case "arrears": // pays a little, and falls behind
		if !isCurrent {
			s.manual(u, at(p, 12+s.rng.n(6), 11), data.LedgerTypeRent, spec.rent/3, "manual", s.rcpt(), "part payment")
		}

	case "organic": // still pays the bank paybill, exact rent, sometimes from the co-payer's phone
		phone, name := spec.phone, spec.name
		if index%2 == 1 && len(spec.payers) > 0 {
			phone, name = *spec.payers[0].Phone, spec.payers[0].Name
		}
		if !isCurrent || s.rng.n(2) == 0 {
			s.organic(pr, u, at(p, 4+s.rng.n(5), 10), rent, phone, name)
		}

	case "cheque": // a cheque bounces in the middle month, then he pays properly
		if index == 1 {
			s.manual(u, at(p, 6, 10), data.LedgerTypeRent, spec.rent, "bank", "CHQ-004512", "cheque deposited")
			s.reverseLatest(u, data.LedgerTypeRent, "Cheque CHQ-004512 bounced (insufficient funds)")
			s.organic(pr, u, at(p, 15, 14), rent, spec.phone, spec.name)
		} else if !isCurrent {
			s.organic(pr, u, at(p, 5, 9), rent, spec.phone, spec.name)
		}

	case "instalments": // two payments in one month: they stack in one cell of the schedule
		if !isCurrent {
			half := money(spec.rent / 2)
			s.organic(pr, u, at(p, 5, 9), half, spec.phone, spec.name)
			s.organic(pr, u, at(p, 20, 16), money(spec.rent).Sub(half), spec.phone, spec.name)
		} else {
			s.organic(pr, u, at(p, 8, 9), money(spec.rent/2), spec.phone, spec.name)
		}

	case "advance": // pays three months up front in August: by September he is in credit
		switch {
		case index == 0:
			s.organic(pr, u, at(p, 4, 9), rent, spec.phone, spec.name)
			s.byIntent(pr, u, at(p, 5, 12), data.LedgerTypeWater, data.LedgerTypeGarbage)
		case index == 1:
			s.organic(pr, u, at(p, 4, 9), money(spec.rent*3), spec.phone, spec.name)
			s.byIntent(pr, u, at(p, 5, 12), data.LedgerTypeWater, data.LedgerTypeGarbage)
		}

	case "cash": // pays cash at the office, entered by hand
		if !isCurrent {
			s.manual(u, at(p, 9, 10), data.LedgerTypeRent, spec.rent, "manual", s.rcpt(), "cash at the office")
		}
	}
}

// ---------------------------------------------------------------------------
// The three ways money arrives

// byIntent is Track A: the tenant used the pay page, so the split is known.
func (s *seeder) byIntent(pr *property, u *unitState, when time.Time, ledgers ...string) {
	target := pr.target(u)
	balances, err := s.m.PayAccess.Balances(s.ctx, target)
	s.must(err, "balances "+u.spec.unit)
	var lines []data.IntentLine
	var total moneyfmt.Money
	for _, want := range ledgers {
		for _, b := range balances {
			if b.Type == want && b.Balance.IsPositive() {
				lines = append(lines, data.IntentLine{Type: b.Type, Amount: b.Balance})
				total = total.Add(b.Balance)
			}
		}
	}
	if len(lines) == 0 {
		return
	}
	intent, err := s.m.PayAccess.CreateIntent(s.ctx, target, u.spec.phone, lines, time.Hour)
	s.must(err, "intent "+u.spec.unit)
	s.process(pr, when, total, u.spec.phone, u.spec.name, intent.Reference)
}

// organic is Track B: a payment to the bank paybill that carries no reference.
func (s *seeder) organic(pr *property, u *unitState, when time.Time, amount moneyfmt.Money, phone, name string) {
	s.process(pr, when, amount, phone, name, "BANK-ACC")
}

func (s *seeder) process(pr *property, when time.Time, amount moneyfmt.Money, phone, name, reference string) {
	receipt := s.mpesa()
	res, err := s.m.Collections.Process(s.ctx, data.CollectionPayment{
		MpesaReceipt: receipt, Amount: amount, Msisdn: strings.TrimPrefix(phone, "+"), PayerName: name,
		AccountReference: reference, ChannelID: pr.channel, ReceivedAt: when,
		Raw: []byte(fmt.Sprintf(`{"seed":true,"receipt":%q}`, receipt)),
	})
	s.must(err, "payment "+receipt)
	if res.Duplicate {
		log.Fatalf("seed generated a duplicate receipt %s", receipt)
	}
}

// manual is a cash or bank payment the manager entered at the office.
func (s *seeder) manual(u *unitState, when time.Time, ledger string, amount int64, source, reference, note string) {
	s.m.Rent.Now = func() time.Time { return when }
	_, err := s.m.Rent.PostPayment(s.ctx, s.tenant, s.tenant, u.unit.ID, data.PaymentInput{
		Amount: money(amount), Source: source, Reference: reference, Note: note, LedgerType: ledger})
	s.must(err, "manual payment "+reference)
}

func (s *seeder) reverseLatest(u *unitState, ledger, reason string) {
	l, err := s.m.Ledger.GetUnitLedger(s.ctx, s.tenant, u.unit.ID, ledger, data.Filters{Page: 1, PageSize: 50, Sort: "-created_at"})
	s.must(err, "ledger "+u.spec.unit)
	for _, e := range l.Entries {
		if e.Direction == data.DirectionCredit && !e.Reversed && e.ReferenceType == data.ReferenceTypePaymentAllocation {
			_, err := s.m.Rent.ReverseEntry(s.ctx, s.tenant, s.tenant, e.ID, reason)
			s.must(err, "reverse "+reason)
			return
		}
	}
	log.Fatalf("no payment to reverse on %s", u.spec.unit)
}

// ---------------------------------------------------------------------------
// The manager's review queue, and the schedule notes

// confirmHistory is the manager working through the review list each month:
// whatever the engine placed on a guess in earlier months has been checked and
// confirmed, so only the current month's decisions are left waiting.
func (s *seeder) confirmHistory() {
	queue, _, err := s.m.Payments.ListForReview(s.ctx, s.tenant, data.Filters{Page: 1, PageSize: 100, Sort: "-received_at", SortSafelist: []string{"-received_at"}})
	s.must(err, "list review queue")
	start := moneyfmt.NewPeriod(time.Now()).FirstDay()
	for _, p := range queue {
		if p.AutoAppliedUnconfirmed && p.ReceivedAt.Before(start) {
			s.must(s.m.Payments.Confirm(s.ctx, s.tenant, p.ID), "confirm "+p.MpesaReceipt)
		}
	}
}

// reviewQueue leaves the payments the engine cannot (or should not) settle on
// its own, so the review screen has something real to show.
func (s *seeder) reviewQueue(pr *property, cur moneyfmt.Period) {
	find := func(code string) *unitState {
		for _, u := range pr.units {
			if u.spec.unit == code {
				return u
			}
		}
		log.Fatalf("no unit %s", code)
		return nil
	}

	// 1. A stranger's number and name: no unit can be found.
	s.process(pr, at(cur, 14, 9), money(2500), "254799888777", "PETER MAINA", "BANK-ACC")

	// 2. An amount that matches no combination of what the unit owes: applied by
	// the default waterfall and flagged for the manager to confirm.
	g2 := find("G2")
	s.process(pr, at(cur, 15, 11), money(7777), strings.TrimPrefix(g2.spec.phone, "+"), g2.spec.name, "BANK-ACC")

	// 3. A payer known only by name (a new number): placed on a guess, flagged.
	c1 := find("1C")
	s.process(pr, at(cur, 16, 13), money(6500), "254700123456", "AGNES NJOROGE MUTHONI", "BANK-ACC")
	_ = c1
}

// ---------------------------------------------------------------------------

func (s *seeder) summary() {
	cur := moneyfmt.NewPeriod(time.Now())
	fmt.Println()
	fmt.Println("================================================================")
	fmt.Println(" Willcoll demo data is ready")
	fmt.Println("================================================================")
	fmt.Printf(" Manager login    %s   /   %s\n", demoEmail, demoPassword)
	fmt.Println(" Super admin      the ADMIN_EMAIL / ADMIN_PASSWORD from backend/.env")
	fmt.Printf(" Periods          history %s to %s (water for %s is a draft)\n", cur.Prev().Prev(), cur, cur)
	fmt.Println()
	for _, pr := range s.props {
		sum := []*data.Property{pr.p}
		s.must(s.m.Properties.AttachSummaries(s.ctx, s.tenant, cur, sum), "summary")
		c := sum[0].Summary
		fmt.Printf(" %-20s %2d occupied, %d vacant | %s expected, %s collected this month\n",
			pr.p.Name, c.UnitsOccupied, c.UnitsVacant, c.RentExpected.Display(), c.RentCollected.Display())
		fmt.Printf("   pay page: /pay/%s/<unit>   e.g. /pay/%s/%s  (phone %s)\n", pr.slug, pr.slug, pr.units[0].spec.unit, pr.units[0].spec.phone)
	}
	q, _, err := s.m.Payments.ListForReview(s.ctx, s.tenant, data.Filters{Page: 1, PageSize: 50, Sort: "-received_at"})
	s.must(err, "review")
	fmt.Printf("\n Review queue     %d payments waiting (unmatched or placed on a guess)\n", len(q))
	fmt.Println(" Pay-page codes   SMS is not configured in development: the code is written")
	fmt.Println("                  to the API log when a phone above requests one.")
	fmt.Println("================================================================")
}
