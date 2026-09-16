package money

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/codercollo/willcoll-sys/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	if err := db.RunMigrations(dsn); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	pool, err := db.NewPool(context.Background(), dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open pool: %v\n", err)
		os.Exit(1)
	}
	testPool = pool

	code := m.Run()

	pool.Close()
	os.Exit(code)
}

func TestLockAccountsConcurrent(t *testing.T) {
	ctx := context.Background()
	s := NewService(testPool)

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Lock Test Org", "Lock Test Org", "lock-test-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	accountIDs := make([]uuid.UUID, 3)
	for i := range accountIDs {
		id := uuid.New()
		if _, err := testPool.Exec(ctx, `
			INSERT INTO ledger_accounts (id, organization_id, owner_type, owner_id)
			VALUES ($1, $2, 'tenant', $3)`,
			id, orgID, uuid.New(),
		); err != nil {
			t.Fatalf("insert account %d: %v", i, err)
		}
		accountIDs[i] = id
	}

	const workers = 20

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			tx, err := testPool.Begin(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer tx.Rollback(ctx)

			// Alternate the requested order. lockAccounts must sort these
			// internally; if it didn't, this test can deadlock.
			ids := []uuid.UUID{accountIDs[0], accountIDs[1]}
			if i%2 == 0 {
				ids = []uuid.UUID{accountIDs[1], accountIDs[0]}
			}

			if err := s.lockAccounts(ctx, tx, ids); err != nil {
				errs <- err
				return
			}
			if err := tx.Commit(ctx); err != nil {
				errs <- err
				return
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("lockAccounts: %v", err)
	}
}

func seedOrgAndUser(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	orgID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO organizations (id, name, brand_name, slug)
		VALUES ($1, $2, $3, $4)`,
		orgID, "Money Org "+uuid.NewString(), "Money Org", "money-"+uuid.NewString(),
	); err != nil {
		t.Fatalf("insert organization: %v", err)
	}

	userID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
		VALUES ($1, $2, $3, $4, $5, 'manager', true, true, 'active')`,
		userID, orgID, "Manager", uuid.NewString(), "money-"+uuid.NewString()+"@example.com",
	); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	return orgID, userID
}

func balanceFor(t *testing.T, orgID uuid.UUID, ownerType string, ownerID uuid.UUID) int64 {
	t.Helper()

	var cents int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT (balance * 100)::bigint
		FROM ledger_accounts
		WHERE organization_id = $1
		  AND owner_type = $2::account_owner_kind
		  AND owner_id = $3`,
		orgID, ownerType, ownerID,
	).Scan(&cents); err != nil {
		t.Fatalf("read %s balance: %v", ownerType, err)
	}
	return cents
}

func seedRentInvoice(t *testing.T, orgID, userID uuid.UUID, amountDue int64) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	propertyID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO properties (id, organization_id, manager_id, name, location)
		VALUES ($1, $2, $3, $4, $5)`,
		propertyID, orgID, userID, "Property", "Test Location",
	); err != nil {
		t.Fatalf("insert property: %v", err)
	}

	unitID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO units (id, property_id, unit_label, base_rent, deposit_amount)
		VALUES ($1, $2, $3, $4, $5)`,
		unitID, propertyID, "A1", 10000, 10000,
	); err != nil {
		t.Fatalf("insert unit: %v", err)
	}

	tenantID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO tenants (id, organization_id, full_name, phone)
		VALUES ($1, $2, $3, $4)`,
		tenantID, orgID, "Tenant", "254700000000",
	); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	leaseID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO leases (id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date, rent_due_day, late_fee_pct_per_day, status, created_by)
		VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, 5, 1.0, 'active', $6)`,
		leaseID, unitID, tenantID, 10000, 10000, userID,
	); err != nil {
		t.Fatalf("insert lease: %v", err)
	}

	invoiceID := uuid.New()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
		VALUES ($1, $2, $3, date_trunc('month', CURRENT_DATE)::date, 'rent', $4)`,
		invoiceID, orgID, leaseID, amountDue,
	); err != nil {
		t.Fatalf("insert invoice: %v", err)
	}

	return tenantID, propertyID, invoiceID
}

func TestExecuteDepositTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID := uuid.New()
	unitID := uuid.New()
	const workers = 20
	amount := int64(10000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteDepositTx(context.Background(), DepositTxInput{
				OrganizationID: orgID,
				TenantID:       tenantID,
				UnitID:         unitID,
				Amount:         amount,
				Method:         "cash",
				IdempotencyKey: "dep-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteDepositTx: %v", err)
	}

	if got := balanceFor(t, orgID, "tenant", tenantID); got != -workers*amount {
		t.Fatalf("tenant balance = %d, want %d", got, -workers*amount)
	}
	if got := balanceFor(t, orgID, "deposit_holding", unitID); got != workers*amount {
		t.Fatalf("deposit balance = %d, want %d", got, workers*amount)
	}
}

func TestExecuteRentPaymentTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID, propertyID, invoiceID := seedRentInvoice(t, orgID, userID, 20*10000)
	const workers = 20
	amount := int64(10000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteRentPaymentTx(context.Background(), PaymentTxInput{
				OrganizationID: orgID,
				TenantID:       tenantID,
				PropertyID:     propertyID,
				InvoiceID:      invoiceID,
				Amount:         amount,
				Method:         "cash",
				IdempotencyKey: "rent-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteRentPaymentTx: %v", err)
	}

	if got := balanceFor(t, orgID, "tenant", tenantID); got != -workers*amount {
		t.Fatalf("tenant balance = %d, want %d", got, -workers*amount)
	}
	if got := balanceFor(t, orgID, "property_till", propertyID); got != workers*amount {
		t.Fatalf("till balance = %d, want %d", got, workers*amount)
	}

	var paid int64
	if err := testPool.QueryRow(context.Background(), `
		SELECT (amount_paid * 100)::bigint FROM invoices WHERE id = $1`, invoiceID,
	).Scan(&paid); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if paid != workers*amount {
		t.Fatalf("invoice paid = %d, want %d", paid, workers*amount)
	}
}

func TestExecuteWaterGarbageTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID, propertyID, invoiceID := seedRentInvoice(t, orgID, userID, 20*10000)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE invoices SET invoice_type = 'water_garbage' WHERE id = $1`, invoiceID,
	); err != nil {
		t.Fatalf("set water invoice: %v", err)
	}

	const workers = 20
	amount := int64(10000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteWaterGarbageTx(context.Background(), PaymentTxInput{
				OrganizationID: orgID,
				TenantID:       tenantID,
				PropertyID:     propertyID,
				InvoiceID:      invoiceID,
				Amount:         amount,
				Method:         "cash",
				IdempotencyKey: "water-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteWaterGarbageTx: %v", err)
	}

	if got := balanceFor(t, orgID, "property_till", propertyID); got != workers*amount {
		t.Fatalf("till balance = %d, want %d", got, workers*amount)
	}
}

func TestExecuteLateFeeChargeTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID := uuid.New()
	propertyID := uuid.New()
	const workers = 20
	amount := int64(5000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteLateFeeChargeTx(context.Background(), LateFeeTxInput{
				OrganizationID: orgID,
				TenantID:       tenantID,
				PropertyID:     propertyID,
				Amount:         amount,
				IdempotencyKey: "late-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteLateFeeChargeTx: %v", err)
	}

	if got := balanceFor(t, orgID, "tenant", tenantID); got != workers*amount {
		t.Fatalf("tenant balance = %d, want %d", got, workers*amount)
	}
	if got := balanceFor(t, orgID, "property_till", propertyID); got != -workers*amount {
		t.Fatalf("till balance = %d, want %d", got, -workers*amount)
	}
}

func TestExecuteRemittanceTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	propertyID := uuid.New()
	landlordID := uuid.New()
	const workers = 20
	amount := int64(10000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteRemittanceTx(context.Background(), RemittanceTxInput{
				OrganizationID: orgID,
				PropertyID:     propertyID,
				LandlordID:     landlordID,
				Amount:         amount,
				Method:         "bank",
				IdempotencyKey: "remit-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteRemittanceTx: %v", err)
	}

	if got := balanceFor(t, orgID, "property_till", propertyID); got != -workers*amount {
		t.Fatalf("till balance = %d, want %d", got, -workers*amount)
	}
	if got := balanceFor(t, orgID, "landlord_payable", landlordID); got != workers*amount {
		t.Fatalf("landlord balance = %d, want %d", got, workers*amount)
	}
}

func TestExecuteDepositRefundTxConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID := uuid.New()
	unitID := uuid.New()
	const workers = 20
	amount := int64(10000)

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.ExecuteDepositRefundTx(context.Background(), DepositRefundTxInput{
				OrganizationID: orgID,
				TenantID:       tenantID,
				UnitID:         unitID,
				Amount:         amount,
				Method:         "bank",
				IdempotencyKey: "refund-" + uuid.NewString(),
				RecordedBy:     userID,
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ExecuteDepositRefundTx: %v", err)
	}

	if got := balanceFor(t, orgID, "deposit_holding", unitID); got != -workers*amount {
		t.Fatalf("deposit balance = %d, want %d", got, -workers*amount)
	}
	if got := balanceFor(t, orgID, "tenant", tenantID); got != workers*amount {
		t.Fatalf("tenant balance = %d, want %d", got, workers*amount)
	}
}

func TestReverseTransferTxIdempotentConcurrent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID := uuid.New()
	unitID := uuid.New()
	amount := int64(10000)

	deposit, err := s.ExecuteDepositTx(context.Background(), DepositTxInput{
		OrganizationID: orgID,
		TenantID:       tenantID,
		UnitID:         unitID,
		Amount:         amount,
		Method:         "cash",
		IdempotencyKey: "deposit-for-reverse-" + uuid.NewString(),
		RecordedBy:     userID,
	})
	if err != nil {
		t.Fatalf("seed deposit: %v", err)
	}

	const workers = 20
	key := "reverse-" + uuid.NewString()

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ReverseTransferTx(context.Background(), ReverseTxInput{
				OrganizationID: orgID,
				TransferID:     deposit.ID,
				IdempotencyKey: key,
				RecordedBy:     userID,
			}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ReverseTransferTx: %v", err)
	}

	if got := balanceFor(t, orgID, "tenant", tenantID); got != 0 {
		t.Fatalf("tenant balance after reversal = %d, want 0", got)
	}
	if got := balanceFor(t, orgID, "deposit_holding", unitID); got != 0 {
		t.Fatalf("deposit balance after reversal = %d, want 0", got)
	}
}

func TestExecuteDepositTxIdempotent(t *testing.T) {
	s := NewService(testPool)
	orgID, userID := seedOrgAndUser(t)
	tenantID := uuid.New()
	unitID := uuid.New()
	amount := int64(10000)
	key := "deposit-once-" + uuid.NewString()

	input := DepositTxInput{
		OrganizationID: orgID,
		TenantID:       tenantID,
		UnitID:         unitID,
		Amount:         amount,
		Method:         "cash",
		IdempotencyKey: key,
		RecordedBy:     userID,
	}

	first, err := s.ExecuteDepositTx(context.Background(), input)
	if err != nil {
		t.Fatalf("first ExecuteDepositTx: %v", err)
	}
	second, err := s.ExecuteDepositTx(context.Background(), input)
	if err != nil {
		t.Fatalf("second ExecuteDepositTx: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("duplicate call returned different transfer ids: %s vs %s", first.ID, second.ID)
	}

	if got := balanceFor(t, orgID, "tenant", tenantID); got != -amount {
		t.Fatalf("tenant balance = %d, want %d", got, -amount)
	}
	if got := balanceFor(t, orgID, "deposit_holding", unitID); got != amount {
		t.Fatalf("deposit balance = %d, want %d", got, amount)
	}
}
