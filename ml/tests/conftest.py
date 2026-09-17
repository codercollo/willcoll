import os

import psycopg2
import pytest

# Defaults so importing app.core.config (eager os.environ[...] reads) never
# crashes test collection when no real sidecar env is configured; individual
# DB tests still skip via requires_db if nothing is actually listening.
os.environ.setdefault("SIDECAR_DATABASE_DSN", "postgres://ml_sidecar_readonly:changeme_in_production@localhost:5432/willcoll?sslmode=disable")
os.environ.setdefault("SIDECAR_SHARED_SECRET", "test-secret")

ADMIN_DSN = os.environ.get("TEST_ADMIN_DSN", "postgres://postgres:postgres@localhost:5432/willcoll?sslmode=disable")
READONLY_DSN = os.environ["SIDECAR_DATABASE_DSN"]


def _db_available() -> bool:
    try:
        psycopg2.connect(ADMIN_DSN).close()
        return True
    except Exception:
        return False


requires_db = pytest.mark.skipif(not _db_available(), reason="no live Postgres at TEST_ADMIN_DSN")


@pytest.fixture
def admin_conn():
    conn = psycopg2.connect(ADMIN_DSN)
    yield conn
    conn.rollback()
    conn.close()
