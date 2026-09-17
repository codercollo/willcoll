from decimal import Decimal

import pytest

from app.services.financial_metrics import (
    debt_service_coverage_ratio,
    gross_operating_income,
    net_operating_income,
)


def test_goi_hand_calculated():
    # GPR 120,000 + other income 5,000 - vacancy/credit losses 6,000 = 119,000
    goi = gross_operating_income(Decimal("120000"), Decimal("5000"), Decimal("6000"))
    assert goi == Decimal("119000")


def test_noi_hand_calculated():
    # GOI 119,000 - OpEx 45,000 = 74,000
    noi = net_operating_income(Decimal("119000"), Decimal("45000"))
    assert noi == Decimal("74000")


def test_dscr_hand_calculated_example_1():
    # NOI 74,000 / annual debt service 50,000 = 1.48 exactly
    dscr = debt_service_coverage_ratio(Decimal("74000"), Decimal("50000"))
    assert dscr == Decimal("1.48")


def test_dscr_hand_calculated_example_2():
    # GPR 200,000 - vacancy 10,000 = GOI 190,000; NOI = 190,000 - 80,000 = 110,000
    # DSCR = 110,000 / 100,000 = 1.1 exactly
    goi = gross_operating_income(Decimal("200000"), Decimal("0"), Decimal("10000"))
    noi = net_operating_income(goi, Decimal("80000"))
    dscr = debt_service_coverage_ratio(noi, Decimal("100000"))
    assert noi == Decimal("110000")
    assert dscr == Decimal("1.1")


def test_dscr_below_one_means_insufficient_income():
    # NOI 90,000 / ADS 100,000 = 0.9 — property doesn't cover its debt
    dscr = debt_service_coverage_ratio(Decimal("90000"), Decimal("100000"))
    assert dscr == Decimal("0.9")


def test_dscr_rejects_zero_debt_service():
    with pytest.raises(ValueError):
        debt_service_coverage_ratio(Decimal("50000"), Decimal("0"))
