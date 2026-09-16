export interface Branding {
  brand_name: string
  slug: string
  logo_url: string | null
  accent_key: string
  sms_sender_id: string | null
}

export const MOCK_BRANDING: Branding = {
  brand_name: 'Willcoll',
  slug: 'willcoll',
  logo_url: null,
  accent_key: 'orange',
  sms_sender_id: null,
}

export function useBranding() {
  return useState<Branding>('branding', () => ({ ...MOCK_BRANDING }))
}
