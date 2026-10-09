package data

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/testenv"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// C3.4 against the real database: a job that writes for firm A and then firm B
// on the same pool leaves each firm's row under that firm only (row-level
// security hides the other's), carrying actor_type=system and the job name,
// and the table refuses an anonymous system row or a named non-system one.
func TestSystemActorAuditRowsAreScopedPerFirmIntegration(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_PLATFORM_DB_DSN", "WILLCOLL_TEST_PLATFORM_ADMIN_DB_DSN")
	open := func(dsn string) *sql.DB {
		c, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if err := c.Ping(); err != nil {
			t.Fatal(err)
		}
		return c
	}
	appConn, adminConn := open(dsns[0]), open(dsns[1])
	models := NewModels(appConn, adminConn, 5*time.Second)
	ctx := context.Background()

	newFirm := func(name string) *Manager {
		m := &Manager{FirmName: name, Username: "u" + uuid.NewString()[:10], Email: uuid.NewString()[:8] + "@audit.example.com", Phone: "+254700000000", Status: ManagerStatusPending}
		if err := m.Password.Set("correct-horse-battery"); err != nil {
			t.Fatal(err)
		}
		if err := models.Managers.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	a, b := newFirm("Audit Firm A"), newFirm("Audit Firm B")
	t.Cleanup(func() {
		_, _ = appConn.Exec(`DELETE FROM managers WHERE id IN ($1, $2)`, a.ID, b.ID)
	})

	const job = "usage-recount"
	for _, firm := range []*Manager{a, b} {
		err := models.Audit.Log(ctx, firm.ID, AuditEntry{ActorType: ActorSystem, ActorName: job, Product: ProductRentals,
			Action: "usage.recount", EntityType: "module_usage", Summary: "recounted rentals units: 5"})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Each firm's own transaction sees only its own row.
	for _, firm := range []*Manager{a, b} {
		var rows int
		var otherFirm int
		tx, err := appConn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1, true)`, firm.ID.String()); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE actor_type = 'system' AND actor_name = $1`, job).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE manager_id <> $1`, firm.ID).Scan(&otherFirm); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		if rows != 1 || otherFirm != 0 {
			t.Errorf("firm %s sees %d of its own system rows and %d of another firm's, want 1 and 0", firm.ID, rows, otherFirm)
		}
	}

	// The constraint: a system row names its job; nobody else carries a job name.
	if _, err := adminConn.ExecContext(ctx, `INSERT INTO audit_log (manager_id, actor_type, action, entity_type, summary) VALUES ($1, 'system', 'x', 'y', 'z')`, a.ID); err == nil {
		t.Error("a system row with no job name was accepted")
	}
	if _, err := adminConn.ExecContext(ctx, `INSERT INTO audit_log (manager_id, actor_type, actor_id, actor_name, action, entity_type, summary) VALUES ($1, 'manager', $1, 'job', 'x', 'y', 'z')`, a.ID); err == nil {
		t.Error("a manager row carrying a job name was accepted")
	}
}
