import { useBranding, useBrandingStatus, type Branding } from '~/composables/useBranding'

const ACCENT_HEX: Record<string, string> = {
  orange: '#E8702A',
  teal: '#1B8A7A',
  indigo: '#4A55B8',
  crimson: '#C13B4B',
  forest: '#2F7D4B',
}

function applyBranding(branding: Branding) {
  const root = document.documentElement
  root.style.setProperty('--brand-name', `"${branding.brand_name}"`)
  root.style.setProperty('--brand-logo-url', branding.logo_url ? `url("${branding.logo_url}")` : 'none')
  root.style.setProperty('--color-action-primary', ACCENT_HEX[branding.accent_key] ?? ACCENT_HEX.orange)
  document.title = branding.brand_name

  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (link) link.href = branding.logo_url ?? '/favicon.ico'
}

// Resolves the Organization from the URL's slug prefix and paints branding
// before the app mounts (spec §1b.2). No mock fallback: a failed fetch
// leaves branding null / status 'error' so the UI shows a real error state
// rather than a fake brand.
export default defineNuxtPlugin(async () => {
  const branding = useBranding()
  const status = useBrandingStatus()
  const slug = useRoute().params.slug
  const slugValue = typeof slug === 'string' ? slug : ''

  if (!slugValue) {
    status.value = 'error'
    return
  }

  try {
    const response = await $fetch<{ data: Branding }>(`/v1/public/branding?slug=${encodeURIComponent(slugValue)}`)
    branding.value = response.data
    status.value = 'ready'
    applyBranding(response.data)
  } catch {
    status.value = 'error'
  }
})
