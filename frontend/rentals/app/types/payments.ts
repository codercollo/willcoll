// Payment review queue (features-functionalities.txt section 5).
export interface ReviewPayment {
  id: string
  source: string
  mpesa_receipt: string
  amount: string
  msisdn: string
  payer_name?: string | null
  account_reference?: string | null
  matched_unit_id?: string | null
  unit_code?: string | null
  status: 'unmatched' | 'matched' | 'allocated' | 'pending'
  auto_applied_unconfirmed: boolean
  review_note?: string | null
  received_at: string
  /** A pending cash payment: the ledger it will credit, who recorded it and when. Posts nothing until confirmed. */
  pending_ledger_type?: string | null
  recorded_by?: string | null
  recorded_by_role?: string | null
  recorded_at?: string
}

/** Counts shown with the review queue; pending_over_48h drives the dashboard notice. */
export interface PendingSummary { pending: number; pending_over_48h: number }

export interface AllocationLine { ledger_type: string; amount: string }
