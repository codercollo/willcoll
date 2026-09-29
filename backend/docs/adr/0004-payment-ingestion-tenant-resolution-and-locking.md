# 0004. Payment ingestion: resolving the tenant, and serialising postings

Status: accepted

## Context

Row-level security keys every table on `app.tenant_id` (ADR 0003), set as the
first statement of a tenant transaction. Two kinds of request arrive with **no
tenant context**:

- PayHero webhooks: only an STK reference, a PayHero channel or an invoice id.
- The public pay page: only a property slug and a unit code.

Separately, payments for one manager can arrive in bursts (month-end), and
under SERIALIZABLE (system-design.txt 3.8) simultaneous postings against the
same ledgers conflict (SQLSTATE 40001).

## Decision

1. **Cross-tenant lookups use the existing `willcoll_admin` pool** (BYPASSRLS,
   SELECT-only) through exactly four id-only queries in
   `internal/db/query/payment_ingest.sql` and `billing.sql`
   (`ResolveIntentByReference`, `ResolveChannelTenants`, `ResolvePayTarget`,
   `ResolveSubscriptionInvoice`), wrapped by `data.ResolverModel`. Everything
   after the lookup runs on the RLS-subject `willcoll_app` pool inside that
   tenant's transaction. `SECURITY DEFINER` functions were rejected: they only
   bypass RLS if their owner has BYPASSRLS, and a silent ownership failure
   would make every payment "unmatched".
2. **Payment postings take a per-manager advisory lock** before their
   transaction begins (`Store.ExecTenantTxExclusive`): a session lock on a
   dedicated connection, taken before the snapshot, released after. SERIALIZABLE
   with jittered retry remains the backstop. Retries alone lost payments in
   `test/ledger_concurrency_test.go` when ten arrived at once.
3. **Webhooks are authenticated, not rate limited**: a shared secret (`?token=`
   or `X-Webhook-Token`) and/or a source-IP allowlist, both enforced when both
   are set, refusing everything when neither is. PayHero cannot sign requests,
   so the secret rides in the callback URL we choose.
4. **Webhooks acknowledge first, then process** in `app.background`, retrying
   transient failures. A payment that still fails is logged in full for
   manual recovery.

## Consequences

- The admin pool now serves four read-only lookups beyond `/v1/admin/*`.
- Payments post one at a time per manager. Throughput is ample (each posting
  is a few milliseconds) and no payment is lost to retry exhaustion.
- A payment the engine places on a guess (default waterfall, or a unit found
  by fuzzy name) is flagged `auto_applied_unconfirmed` and appears in the
  manager review queue until confirmed.
