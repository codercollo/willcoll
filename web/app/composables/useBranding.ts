export interface Branding {
  brand_name: string
  slug: string
  logo_url: string | null
  accent_key: string
  sms_sender_id: string | null
}

// Real state only — null means "not loaded yet", never a fake placeholder
// brand. branding.client.ts is the one place that populates this from the
// real GET /v1/public/branding response.
export function useBranding() {
  return useState<Branding | null>('branding', () => null)
}

export function useBrandingStatus() {
  return useState<'loading' | 'ready' | 'error'>('branding-status', () => 'loading')
}
