"""6.4 — append-only guarantee: property_scores is never UPDATEd.

property_scores has UNIQUE(property_id, as_of_date, model_version) (spec
§3.5) — a genuine re-run on the SAME day/model_version is therefore treated
as already-computed and returns the existing row (persist_score's
ON CONFLICT DO NOTHING), not a duplicate insert or a silent overwrite. A
re-run on a DIFFERENT as_of_date/model_version is what actually creates a
second, distinct row — that's the real "never overwrite a prior score"
guarantee from spec §13.2 point 4, and is what this test proves.
"""

import uuid
from datetime import date, timedelta

from app.repositories.score_repo import persist_score

from .conftest import requires_db


def _score_kwargs(property_id, org_id, requested_by, as_of_date, model_version):
    return dict(
        organization_id=org_id,
        property_id=property_id,
        as_of_date=as_of_date,
        model_version=model_version,
        noi=1000,
        dscr=1.5,
        score_value=72.5,
        score_band="B",
        feature_snapshot={"collection_rate": 0.9},
        requested_by=requested_by,
    )


@requires_db
def test_same_day_call_is_idempotent_not_duplicated(admin_conn):
    org_id, manager_id, property_id = str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
    with admin_conn.cursor() as cur:
        cur.execute(
            "INSERT INTO organizations (id, name, brand_name, slug) VALUES (%s,%s,%s,%s)",
            (org_id, "Idem Org", "Idem Org", f"idem-{org_id[:8]}"),
        )
        cur.execute(
            """INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
               VALUES (%s,%s,'Manager',%s,%s,'manager',true,true,'active')""",
            (manager_id, org_id, str(uuid.uuid4())[:15], f"{manager_id}@example.com"),
        )
        cur.execute(
            "INSERT INTO properties (id, organization_id, manager_id, name, location) VALUES (%s,%s,%s,'P','L')",
            (property_id, org_id, manager_id),
        )
    admin_conn.commit()
    admin_conn.autocommit = True

    as_of = date.today()
    first = persist_score(admin_conn, **_score_kwargs(property_id, org_id, manager_id, as_of, "v1"))
    second = persist_score(admin_conn, **_score_kwargs(property_id, org_id, manager_id, as_of, "v1"))

    assert first["id"] == second["id"]

    with admin_conn.cursor() as cur:
        cur.execute("SELECT count(*) FROM property_scores WHERE property_id = %s", (property_id,))
        assert cur.fetchone()[0] == 1


@requires_db
def test_different_as_of_date_creates_a_distinct_row(admin_conn):
    org_id, manager_id, property_id = str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())
    with admin_conn.cursor() as cur:
        cur.execute(
            "INSERT INTO organizations (id, name, brand_name, slug) VALUES (%s,%s,%s,%s)",
            (org_id, "Idem Org 2", "Idem Org 2", f"idem2-{org_id[:8]}"),
        )
        cur.execute(
            """INSERT INTO users (id, organization_id, full_name, phone, email, role, is_super_manager, activated, status)
               VALUES (%s,%s,'Manager',%s,%s,'manager',true,true,'active')""",
            (manager_id, org_id, str(uuid.uuid4())[:15], f"{manager_id}@example.com"),
        )
        cur.execute(
            "INSERT INTO properties (id, organization_id, manager_id, name, location) VALUES (%s,%s,%s,'P','L')",
            (property_id, org_id, manager_id),
        )
    admin_conn.commit()
    admin_conn.autocommit = True

    today = date.today()
    yesterday = today - timedelta(days=1)
    first = persist_score(admin_conn, **_score_kwargs(property_id, org_id, manager_id, yesterday, "v1"))
    second = persist_score(admin_conn, **_score_kwargs(property_id, org_id, manager_id, today, "v1"))

    assert first["id"] != second["id"]

    with admin_conn.cursor() as cur:
        cur.execute("SELECT count(*) FROM property_scores WHERE property_id = %s", (property_id,))
        assert cur.fetchone()[0] == 2
