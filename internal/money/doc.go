// Core money-movement package — simplebank "transfer" pattern applied to rent
// instead of bank transfers. Owns EVERY write to ledger_accounts.balance and
// invoices.amount_paid; nothing else touches those tables (spec §4.3).
package money
