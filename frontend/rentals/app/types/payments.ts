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
  status: 'unmatched' | 'matched' | 'allocated'
  auto_applied_unconfirmed: boolean
  review_note?: string | null
  received_at: string
}

export interface AllocationLine { ledger_type: string; amount: string }
