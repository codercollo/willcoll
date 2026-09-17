from contextlib import contextmanager

import psycopg2

from app.core.config import settings


@contextmanager
def get_connection():
    """One connection, one implicit transaction — read-only (ml_sidecar_readonly
    role has no INSERT/UPDATE grants, spec §13.3.1). Never committed; closing
    without commit rolls back, which is a no-op for a read-only session."""
    conn = psycopg2.connect(settings.database_dsn)
    try:
        yield conn
    finally:
        conn.close()
