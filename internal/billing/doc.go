// Late-fee computation — deliberately separate from internal/money's transfer
// execution so the *policy* (1%/day past due day, spec §4.3) can change per
// property without touching ledger mechanics
package billing
