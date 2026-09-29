import { defineStore } from 'pinia'

// A lease-bound unit QR code (backend features section 12). The sticker
// encodes only a URL; the token identifies the unit and never authenticates
// the tenant, who still verifies by SMS on the pay page.
export interface UnitQr {
  id: string
  token: string
  url: string
  short_code: string // K7QM-2XH9-PTRB
  lease_id: string
  unit_id: string
  property_id: string
  scan_count: number
  last_scanned_at?: string | null
  created_at: string
}

export const useQr = defineStore('qr', () => {
  const api = useApi()

  /** The lease's live code; the API creates it on first use. */
  async function get(leaseId: string) {
    return (await api.get<{ qr: UnitQr }>(`/leases/${leaseId}/qr`)).qr
  }

  /** Retire the current code and issue a new one. The old sticker stops working at once. */
  async function rotate(leaseId: string) {
    return (await api.post<{ qr: UnitQr }>(`/leases/${leaseId}/qr/rotate`)).qr
  }

  /** The PNG, fetched with the session cookie (an <img src> to another origin would not send it). */
  async function image(leaseId: string) {
    return api.blob(`/leases/${leaseId}/qr.png`)
  }

  /**
   * Get-or-creates one code per lease and returns the A4 sticker sheet PDF
   * plus a created/reused/skipped summary (the response header this rides
   * in — the PDF body has no room for it).
   */
  async function bulk(propertyId: string, leaseIds: string[]) {
    const { blob, headers } = await api.blobWithHeaders(`/properties/${propertyId}/qr/bulk`, { method: 'POST', body: { lease_ids: leaseIds } })
    const summaryHeader = headers.get('X-Bulk-Qr-Summary')
    const summary: BulkQrSummary = summaryHeader ? JSON.parse(summaryHeader) : { created: 0, reused: 0, skipped: [] }
    return { blob, summary }
  }

  return { get, rotate, image, bulk }
})

export interface BulkQrSummary {
  created: number
  reused: number
  skipped: { lease_id: string; reason: string }[]
}
