"""Feature extraction over ledger_repo's raw rows (spec §13.2).

Pure functions only — no DB/network calls anywhere in this file — so every
function is testable with fixed fixture data and a known expected output.
Each row is one ledger_entries record joined against its invoice/unit
(ledger_repo.get_property_ledger's shape); an invoice can appear on several
rows when it was paid in more than one entry (partial payments), so every
function here dedupes by invoice_id before touching invoice-level fields.
"""

from collections import defaultdict


def _dedupe_invoices(rows: list[dict]) -> list[dict]:
    """Collapses the ledger-entry fan-out back to one row per invoice.
    amount_due/amount_paid/status/created_at are identical across every row
    that shares an invoice_id, so the last one seen is as good as any."""
    by_invoice = {r["invoice_id"]: r for r in rows}
    return list(by_invoice.values())


def collection_rate(rows: list[dict]) -> dict[str, float]:
    """Per unit: total amount_paid / total amount_due across its invoices.
    1.0 = fully collected, 0.0 = nothing collected. A unit with no invoices
    is simply absent from the result."""
    by_unit = defaultdict(list)
    for inv in _dedupe_invoices(rows):
        by_unit[inv["unit_id"]].append(inv)

    result = {}
    for unit_id, invoices in by_unit.items():
        due = sum(inv["amount_due"] for inv in invoices)
        paid = sum(inv["amount_paid"] for inv in invoices)
        result[unit_id] = float(paid / due) if due else 0.0
    return result


def vacancy_periods(rows: list[dict]) -> dict[str, int]:
    """Per unit: count of calendar months, between its first and last
    invoiced period (inclusive), that have no invoice at all — a proxy for
    time the unit sat unlet, using only invoice history (there is no
    dedicated unit-status-transition table to read instead)."""
    by_unit = defaultdict(set)
    for inv in _dedupe_invoices(rows):
        pm = inv["period_month"]
        by_unit[inv["unit_id"]].add((pm.year, pm.month))

    result = {}
    for unit_id, months in by_unit.items():
        if len(months) < 2:
            result[unit_id] = 0
            continue
        ordered = sorted(months)
        span = _month_span(ordered[0], ordered[-1])
        result[unit_id] = span - len(months)
    return result


def arrears_recovery_speed(rows: list[dict]) -> dict[str, float]:
    """Per unit: average days from an invoice's creation (when it went
    'open') to the ledger entry that brought it fully paid, across that
    unit's paid invoices. A unit with no paid invoices is absent from the
    result — there's nothing to average."""
    by_unit_invoice = defaultdict(lambda: defaultdict(list))
    for r in rows:
        by_unit_invoice[r["unit_id"]][r["invoice_id"]].append(r)

    result = {}
    for unit_id, invoices in by_unit_invoice.items():
        deltas = []
        for entries in invoices.values():
            if entries[0]["invoice_status"] != "paid":
                continue
            paid_at = max(e["entry_at"] for e in entries)
            created_at = entries[0]["invoice_created_at"]
            deltas.append((paid_at - created_at).total_seconds() / 86400)

        if deltas:
            result[unit_id] = sum(deltas) / len(deltas)
    return result


def extract_features(rows: list[dict]) -> dict[str, dict]:
    """Combines the three metrics above into one per-unit feature dict —
    the shape the scoring model consumes. A unit only appears if at least
    one of the three metrics has something to say about it."""
    rates = collection_rate(rows)
    vacancies = vacancy_periods(rows)
    recovery = arrears_recovery_speed(rows)

    unit_ids = set(rates) | set(vacancies) | set(recovery)
    return {
        unit_id: {
            "collection_rate": rates.get(unit_id, 0.0),
            "vacancy_periods": vacancies.get(unit_id, 0),
            "arrears_recovery_days": recovery.get(unit_id),
        }
        for unit_id in unit_ids
    }


def _month_span(start: tuple[int, int], end: tuple[int, int]) -> int:
    return (end[0] - start[0]) * 12 + (end[1] - start[1]) + 1
