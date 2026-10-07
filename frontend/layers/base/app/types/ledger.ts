// One line of a unit's (or any account's) append-only ledger, as the APIs
// return it. Amounts are decimal strings; never do arithmetic on them.
export interface LedgerEntry {
  id: string
  direction: 'DEBIT' | 'CREDIT'
  amount: string
  reference_type: string
  created_at: string
  transaction_type: string
  description: string
  running_balance: string
  reversed: boolean
}
