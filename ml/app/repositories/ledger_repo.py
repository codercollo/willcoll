"""Data access for Verified Property Score feature extraction (spec §13.3).

One function, one property_id — no signature anywhere in this codebase takes
a list of properties or an organization_id alone (spec §13.5).
"""

from app.core.db import get_connection

_QUERY = """
    SELECT u.id AS unit_id, u.unit_label, u.status,
           i.id AS invoice_id, i.period_month, i.invoice_type, i.amount_due, i.amount_paid,
           i.status AS invoice_status, i.created_at AS invoice_created_at,
           le.amount, le.created_at AS entry_at
    FROM units u
    JOIN leases l ON l.unit_id = u.id
    JOIN invoices i ON i.lease_id = l.id
    JOIN ledger_accounts a ON a.owner_type = 'tenant' AND a.owner_id = l.tenant_id
    JOIN ledger_entries le ON le.account_id = a.id
    WHERE u.property_id = %(property_id)s
    ORDER BY u.unit_label, i.period_month, le.created_at
"""


def get_property_ledger(property_id: str) -> list[dict]:
    """Returns one property's full unit-by-unit rent-collection history:
    units -> leases -> invoices -> ledger_entries, ordered unit/period/entry
    time. Row-Level Security still governs every table here (ml_sidecar_readonly
    is not BYPASSRLS); this only narrows RLS's already-scoped result set
    further, to one property_id.
    """
    with get_connection() as conn, conn.cursor() as cur:
        # RLS needs app.current_org_id set before it returns anything, but
        # this endpoint never receives organization_id — resolve_property_org
        # is the one narrow, SECURITY DEFINER bootstrap that reads only
        # properties.organization_id (a foreign-key fact, not ledger data)
        # so that value can be set for the rest of this transaction.
        cur.execute("SELECT resolve_property_org(%(property_id)s)", {"property_id": property_id})
        row = cur.fetchone()
        org_id = row[0] if row else None
        if org_id is None:
            return []

        cur.execute("SELECT set_config('app.current_org_id', %(org_id)s, true)", {"org_id": str(org_id)})

        cur.execute(_QUERY, {"property_id": property_id})
        columns = [desc[0] for desc in cur.description]
        return [dict(zip(columns, r)) for r in cur.fetchall()]
