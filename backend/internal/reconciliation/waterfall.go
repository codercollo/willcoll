package reconciliation

import "github.com/codercollo/willcoll/backend/pkg/moneyfmt"

// ApplyWaterfall applies amount to the unit's open balances in the standing
// default order (WATER, GARBAGE, RENT, RENT_DEPOSIT, WATER_DEPOSIT), each
// cleared in full before the next is touched. A ledger account balances the
// whole ledger, not individual charges, so paying a type down naturally
// clears its oldest period first.
//
// Anything left after every open balance is cleared is an overpayment. It is
// credited to RENT as an advance payment, the way the paper ledger carries a
// tenant forward, rather than being dropped or bounced. The allocations
// always sum to exactly amount.
func ApplyWaterfall(amount moneyfmt.Money, open []OpenBalance) []Allocation {
	if !amount.IsPositive() {
		return nil
	}

	remaining := amount
	var out []Allocation
	for _, o := range openInOrder(open) {
		if !remaining.IsPositive() {
			break
		}
		pay := o.Balance
		if remaining.Cmp(pay) < 0 {
			pay = remaining
		}
		out = append(out, Allocation{LedgerType: o.LedgerType, Amount: pay})
		remaining = remaining.Sub(pay)
	}

	if remaining.IsPositive() {
		for i := range out {
			if out[i].LedgerType == Rent {
				out[i].Amount = out[i].Amount.Add(remaining)
				return out
			}
		}
		out = append(out, Allocation{LedgerType: Rent, Amount: remaining})
	}
	return out
}
