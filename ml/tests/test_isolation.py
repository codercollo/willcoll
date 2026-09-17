"""6.1 — release-blocking: scoring property A must never read a row
belonging to property B, even in the same Organization."""

import uuid

from app.repositories.ledger_repo import get_property_ledger

from .conftest import requires_db


def _seed_property_with_unit(cur, org_id, manager_id, property_id, unit_id, tenant_id, invoice_id, lease_id):
    account_id, transfer_id = str(uuid.uuid4()), str(uuid.uuid4())
    cur.execute(
        "INSERT INTO properties (id, organization_id, manager_id, name, location) VALUES (%s,%s,%s,%s,%s)",
        (property_id, org_id, manager_id, f"Property {property_id}", "Test Location"),
    )
    cur.execute(
        "INSERT INTO units (id, property_id, unit_label, base_rent, deposit_amount) VALUES (%s,%s,%s,10000,10000)",
        (unit_id, property_id, "A1"),
    )
    cur.execute(
        "INSERT INTO tenants (id, organization_id, full_name, phone) VALUES (%s,%s,%s,%s)",
        (tenant_id, org_id, "Tenant", str(uuid.uuid4())[:15]),
    )
    cur.execute(
        """INSERT INTO leases (id, unit_id, tenant_id, monthly_rent, deposit_paid, start_date,
               rent_due_day, late_fee_pct_per_day, status, created_by)
           VALUES (%s,%s,%s,10000,10000,CURRENT_DATE,5,1.0,'active',%s)""",
        (lease_id, unit_id, tenant_id, manager_id),
    )
    cur.execute(
        """INSERT INTO invoices (id, organization_id, lease_id, period_month, invoice_type, amount_due)
           VALUES (%s,%s,%s,date_trunc('month', CURRENT_DATE),'rent',10000)""",
        (invoice_id, org_id, lease_id),
    )
    cur.execute(
        "INSERT INTO ledger_accounts (id, organization_id, owner_type, owner_id) VALUES (%s,%s,'tenant',%s)",
        (account_id, org_id, tenant_id),
    )
    cur.execute(
        """INSERT INTO ledger_transfers (id, organization_id, transfer_type, invoice_id, method, idempotency_key, recorded_by)
           VALUES (%s,%s,'rent_payment',%s,'cash',%s,%s)""",
        (transfer_id, org_id, invoice_id, str(uuid.uuid4()), manager_id),
    )
    cur.execute(
        "INSERT INTO ledger_entries (account_id, amount, transfer_id) VALUES (%s,-100.00,%s)",
        (account_id, transfer_id),
    )


@requires_db
def test_scoring_property_a_never_sees_property_b_rows(admin_conn):
    org_id = str(uuid.uuid4())
    manager_id = str(uuid.uuid4())
    property_a, property_b = str(uuid.uuid4()), str(uuid.uuid4())
    unit_a, unit_b = str(uuid.uuid4()), str(uuid.uuid4())

    with admin_conn.cursor() as cur:
        cur.execute(
            "INSERT INTO organizations (id, name, brand_name, slug) VALUES (%s,%s,%s,%s)",
            (org_id, "Isolation Org", "Isolation Org", f"iso-{org_id[:8]}"),
        )
        cur.execute(
            """INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
               VALUES (%s,%s,'Manager',%s,%s,'manager',true,true,'active')""",
            (manager_id, org_id, str(uuid.uuid4())[:15], f"{manager_id}@example.com"),
        )
        cur.execute("SELECT set_config('app.current_org_id', %s, false)", (org_id,))

        _seed_property_with_unit(
            cur, org_id, manager_id, property_a, unit_a, str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
        )
        _seed_property_with_unit(
            cur, org_id, manager_id, property_b, unit_b, str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
        )
    admin_conn.commit()

    rows_a = get_property_ledger(property_a)
    rows_b = get_property_ledger(property_b)

    assert rows_a and rows_b  # guard against a trivially-empty pass
    assert all(r["unit_id"] == unit_a for r in rows_a)
    assert all(r["unit_id"] == unit_b for r in rows_b)
    assert unit_b not in {r["unit_id"] for r in rows_a}
    assert unit_a not in {r["unit_id"] for r in rows_b}
