"""Standard institutional underwriting metrics (spec §13.2) — the exact
formulas a bank's own risk team already uses, not a Willcoll invention.
Pure functions: no DB/network calls, no rounding shortcuts.
"""


def gross_operating_income(gross_potential_rent, other_income, vacancy_and_credit_losses):
    """GOI = Gross Potential Rent + Other Income - Vacancy and Credit Losses."""
    return gross_potential_rent + other_income - vacancy_and_credit_losses


def net_operating_income(goi, operating_expenses):
    """NOI = Gross Operating Income (GOI) - Operating Expenses."""
    return goi - operating_expenses


def debt_service_coverage_ratio(noi, annual_debt_service):
    """DSCR = NOI / Annual Debt Service (total principal + interest paid on
    the loan over one year)."""
    if annual_debt_service == 0:
        raise ValueError("annual_debt_service must be greater than zero")
    return noi / annual_debt_service
