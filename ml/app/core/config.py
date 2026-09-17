import os


class Settings:
    """Sidecar config, env-driven — its own read-only DSN, never the Go
    API's read-write DATABASE_DSN (spec §13.3.1)."""

    database_dsn: str = os.environ["SIDECAR_DATABASE_DSN"]
    shared_secret: str = os.environ["SIDECAR_SHARED_SECRET"]


settings = Settings()
