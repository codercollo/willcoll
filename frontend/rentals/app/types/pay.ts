// Public pay flow (features-functionalities.txt section 6).
export interface PayUnit { property_name: string; unit_code: string; can_pay: boolean }

export interface PayBalance {
  type: string // RENT | WATER | GARBAGE | RENT_DEPOSIT | WATER_DEPOSIT | ELECTRICITY_DEPOSIT
  balance: string // what is owed; negative = credit
  // Whether this line can be paid right now: always true for rent/water/
  // garbage, false for a deposit that's already settled (still shown, just
  // not payable — a deposit can't be paid ahead).
  payable: boolean
}

export interface PayIntent { id: string; status: 'pending'; amount: string; expires_at: string }

// Rides alongside a created intent: normally "check your phone and enter
// your M-Pesa PIN", but a softer "...if no prompt arrives in a minute, try
// again" when the STK push call to PayHero timed out and we genuinely don't
// know whether the phone was prompted.
export interface CreateIntentResponse { intent: PayIntent; message: string }

export interface PayReceipt {
  mpesa_receipt: string
  amount: string
  paid_at: string
  property_name: string
  unit_code: string
  lines: { type: string; amount: string }[]
}

export interface IntentStatus { status: 'pending' | 'completed' | 'failed' | 'expired'; failure_reason?: string; receipt?: PayReceipt }
