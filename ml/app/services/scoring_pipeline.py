"""Orchestrates the full scoring pipeline (spec §13.2): fetch ledger data ->
extract features -> compute NOI/DSCR -> run model -> band -> persist.

NOI/DSCR need operating_expenses and annual_debt_service — Willcoll's ledger
has no expense/loan tables, so these are never guessed; they're required
caller inputs (the Manager/underwriter supplies real figures). Gross
Potential Rent and vacancy/credit losses ARE derived from real ledger data
(amount_due vs amount_paid on rent invoices).
"""

from datetime import date
from decimal import Decimal

from app.core.db import get_connection
from app.core.model_loader import get_model
from app.repositories.ledger_repo import get_property_ledger
from app.repositories.score_repo import persist_score
from app.services.feature_extraction_service import extract_features
from app.services.financial_metrics import (
    debt_service_coverage_ratio,
    gross_operating_income,
    net_operating_income,
)

_BAND_THRESHOLDS = (("A", 80), ("B", 60), ("C", 40))


class UnknownPropertyError(ValueError):
    pass


def score_band(score_value: float) -> str:
    for band, threshold in _BAND_THRESHOLDS:
        if score_value >= threshold:
            return band
    return "D"


def _portfolio_features(rows: list[dict]) -> dict:
    """Averages per-unit Phase 2 features into one property-level vector."""
    per_unit = extract_features(rows)
    if not per_unit:
        return {"collection_rate": 0.0, "vacancy_periods": 0.0, "arrears_recovery_days": 0.0}

    n = len(per_unit)
    recovery = [f["arrears_recovery_days"] for f in per_unit.values() if f["arrears_recovery_days"] is not None]
    return {
        "collection_rate": sum(f["collection_rate"] for f in per_unit.values()) / n,
        "vacancy_periods": sum(f["vacancy_periods"] for f in per_unit.values()) / n,
        "arrears_recovery_days": sum(recovery) / len(recovery) if recovery else 0.0,
    }


def _rent_invoices(rows: list[dict]) -> list[dict]:
    seen = {}
    for r in rows:
        if r["invoice_type"] == "rent":
            seen[r["invoice_id"]] = r
    return list(seen.values())


def score_property(
    property_id: str,
    requested_by: str,
    operating_expenses,
    annual_debt_service,
    other_income=0,
) -> dict:
    # psycopg2 returns NUMERIC columns as Decimal; caller inputs arrive as
    # plain floats from the request body. Normalize to Decimal before any
    # arithmetic mixes the two (Python refuses Decimal - float directly).
    operating_expenses = Decimal(str(operating_expenses))
    annual_debt_service = Decimal(str(annual_debt_service))
    other_income = Decimal(str(other_income))

    rows = get_property_ledger(property_id)
    if not rows:
        raise UnknownPropertyError(f"no data for property_id={property_id}")

    features = _portfolio_features(rows)

    rent_invoices = _rent_invoices(rows)
    gross_potential_rent = sum(inv["amount_due"] for inv in rent_invoices)
    vacancy_and_credit_losses = sum(inv["amount_due"] - inv["amount_paid"] for inv in rent_invoices)

    goi = gross_operating_income(gross_potential_rent, other_income, vacancy_and_credit_losses)
    noi = net_operating_income(goi, operating_expenses)
    dscr = debt_service_coverage_ratio(noi, annual_debt_service)

    model, model_version, _ = get_model()
    X = [[features["collection_rate"], features["vacancy_periods"], features["arrears_recovery_days"], float(dscr)]]
    score_value = float(model.predict(X)[0])
    band = score_band(score_value)

    with get_connection() as conn:
        with conn.cursor() as cur:
            cur.execute("SELECT resolve_property_org(%(property_id)s)", {"property_id": property_id})
            row = cur.fetchone()
            org_id = row[0] if row else None
            if org_id is None:
                raise UnknownPropertyError(f"unknown property_id: {property_id}")
            cur.execute("SELECT set_config('app.current_org_id', %(org_id)s, true)", {"org_id": str(org_id)})

        return persist_score(
            conn,
            organization_id=org_id,
            property_id=property_id,
            as_of_date=date.today(),
            model_version=model_version,
            noi=noi,
            dscr=dscr,
            score_value=score_value,
            score_band=band,
            feature_snapshot=features,
            requested_by=requested_by,
        )
