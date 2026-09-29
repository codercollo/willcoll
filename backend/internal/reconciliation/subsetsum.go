// Package reconciliation works out what a shilling paid for (system-design.txt
// section 5). It is pure: no database, no clock. Callers pass in a unit's open
// balances and the payment, and get back proposed allocations.
package reconciliation

import "github.com/codercollo/willcoll/backend/pkg/moneyfmt"

// Ledger types, spelled as in ledger_accounts.type.
const (
	Rent         = "RENT"
	Water        = "WATER"
	Garbage      = "GARBAGE"
	RentDeposit  = "RENT_DEPOSIT"
	WaterDeposit = "WATER_DEPOSIT"
)

// Allocation is part of a payment applied to one ledger type.
type Allocation struct {
	LedgerType string
	Amount     moneyfmt.Money
}

// OpenBalance is what a unit currently owes on one ledger type. Only
// positive balances are open; zero and credit balances are ignored.
type OpenBalance struct {
	LedgerType string
	Balance    moneyfmt.Money
}

// order is the standing default waterfall (system-design.txt section 5).
var order = []string{Water, Garbage, Rent, RentDeposit, WaterDeposit}

// openInOrder returns the positive balances in waterfall order, merging any
// duplicate ledger types.
func openInOrder(open []OpenBalance) []OpenBalance {
	sums := make(map[string]moneyfmt.Money)
	for _, o := range open {
		sums[o.LedgerType] = sums[o.LedgerType].Add(o.Balance)
	}
	var out []OpenBalance
	for _, t := range order {
		if b, ok := sums[t]; ok && b.IsPositive() {
			out = append(out, OpenBalance{LedgerType: t, Balance: b})
			delete(sums, t)
		}
	}
	// Types outside the standard five (not expected) keep a stable place last.
	for t, b := range sums {
		if b.IsPositive() {
			out = append(out, OpenBalance{LedgerType: t, Balance: b})
		}
	}
	return out
}

// FindMatchingCombinations returns every combination of the unit's open balances that adds
// up to exactly amount, each combination clearing those balances in full.
// With at most five ledger types that is at most 32 subsets, so it simply
// enumerates them. The result is deterministic: combinations come in
// bitmask order over the waterfall ordering.
//
// Callers read the result as: exactly one combination means auto-apply;
// none means fall back to the waterfall; more than one means ask a manager.
func FindMatchingCombinations(amount moneyfmt.Money, open []OpenBalance) [][]Allocation {
	if !amount.IsPositive() {
		return nil
	}
	items := openInOrder(open)
	n := len(items)

	var matches [][]Allocation
	for mask := 1; mask < 1<<n; mask++ {
		var sum moneyfmt.Money
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sum = sum.Add(items[i].Balance)
			}
		}
		if sum != amount {
			continue
		}
		var combo []Allocation
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				combo = append(combo, Allocation{LedgerType: items[i].LedgerType, Amount: items[i].Balance})
			}
		}
		matches = append(matches, combo)
	}
	return matches
}
