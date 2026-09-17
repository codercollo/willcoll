from datetime import datetime, timezone
from decimal import Decimal

from app.services.feature_extraction_service import (
    arrears_recovery_speed,
    collection_rate,
    extract_features,
    vacancy_periods,
)

UNIT_A = "unit-a"


def _row(**over):
    base = dict(
        unit_id=UNIT_A,
        unit_label="A1",
        status="occupied",
        invoice_id="inv-jan",
        period_month=datetime(2026, 1, 1),
        invoice_type="rent",
        amount_due=Decimal("1000"),
        amount_paid=Decimal("1000"),
        invoice_status="paid",
        invoice_created_at=datetime(2026, 1, 1, tzinfo=timezone.utc),
        amount=Decimal("-1000"),
        entry_at=datetime(2026, 1, 10, tzinfo=timezone.utc),
    )
    base.update(over)
    return base


def _fixture_rows():
    # Jan invoice: two partial-payment entries (tests dedupe), paid in full
    # 9 days after creation.
    jan_1 = _row(amount=Decimal("-400"), entry_at=datetime(2026, 1, 5, tzinfo=timezone.utc))
    jan_2 = _row(amount=Decimal("-600"), entry_at=datetime(2026, 1, 10, tzinfo=timezone.utc))
    # Feb has no invoice at all — the vacancy gap.
    # Mar invoice: paid 2 days after creation.
    mar = _row(
        invoice_id="inv-mar",
        period_month=datetime(2026, 3, 1),
        invoice_created_at=datetime(2026, 3, 1, tzinfo=timezone.utc),
        entry_at=datetime(2026, 3, 3, tzinfo=timezone.utc),
    )
    return [jan_1, jan_2, mar]


def test_collection_rate():
    assert collection_rate(_fixture_rows()) == {UNIT_A: 1.0}


def test_collection_rate_partial():
    rows = [_row(amount_due=Decimal("1000"), amount_paid=Decimal("400"), invoice_status="partially_paid")]
    assert collection_rate(rows) == {UNIT_A: 0.4}


def test_collection_rate_no_invoices():
    assert collection_rate([]) == {}


def test_vacancy_periods_counts_missing_month():
    # Jan and Mar invoiced, Feb missing → 1 vacant month.
    assert vacancy_periods(_fixture_rows()) == {UNIT_A: 1}


def test_vacancy_periods_single_invoice_is_zero():
    assert vacancy_periods([_row()]) == {UNIT_A: 0}


def test_arrears_recovery_speed_averages_paid_invoices():
    result = arrears_recovery_speed(_fixture_rows())
    assert result == {UNIT_A: (9 + 2) / 2}


def test_arrears_recovery_speed_ignores_unpaid_invoices():
    rows = [_row(invoice_status="open")]
    assert arrears_recovery_speed(rows) == {}


def test_extract_features_combines_all_three():
    out = extract_features(_fixture_rows())
    assert out == {
        UNIT_A: {
            "collection_rate": 1.0,
            "vacancy_periods": 1,
            "arrears_recovery_days": (9 + 2) / 2,
        }
    }
