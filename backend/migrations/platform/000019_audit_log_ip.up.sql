-- The client address the action came from, as the API resolved it: the TCP
-- peer, or the X-Forwarded-For chain only when the peer is a trusted proxy
-- (TRUSTED_PROXIES), so a spoofed header is never recorded as the actor's IP.
-- NULL for system actions (scheduled jobs) and rows written before this.
ALTER TABLE audit_log ADD COLUMN ip text NULL;
