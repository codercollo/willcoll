"""Shared-secret auth (spec §13.3) — the sidecar has no other auth, since
it's never reachable from the public internet, only server-to-server from
internal/scoring. Reject anything without the header."""

from fastapi import Header, HTTPException

from app.core.config import settings


def require_shared_secret(x_sidecar_shared_secret: str = Header(default=None)) -> None:
    if not x_sidecar_shared_secret or x_sidecar_shared_secret != settings.shared_secret:
        raise HTTPException(status_code=401, detail="invalid or missing shared secret")
