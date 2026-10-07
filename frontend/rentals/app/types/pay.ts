// Public pay flow (features-functionalities.txt section 6).
import type { PayBalance } from '#base/types/payLine'

export type { PayBalance }
export interface PayUnit { property_name: string; unit_code: string; can_pay: boolean }


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
