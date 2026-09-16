import { useBranding, type Branding } from '~/composables/useBranding'

const ACCENT_HEX: Record<string, string> = {
  orange: '#E8702A',
  teal: '#1B8A7A',
  indigo: '#4A55B8',
  crimson: '#C13B4B',
  forest: '#2F7D4B',
}

function resolveSlug(): string {
  const parts = window.location.pathname.split('/').filter(Boolean)
  return parts[0] || 'willcoll'
}

function applyBranding(branding: Branding) {
  const root = document.documentElement

  root.style.setProperty('--brand-name', `"${branding.brand_name}"`)
  root.style.setProperty('--brand-logo-url', branding.logo_url ? `url("${branding.logo_url}")` : 'none')
  root.style.setProperty('--color-action-primary', ACCENT_HEX[branding.accent_key] ?? ACCENT_HEX.orange)

  document.title = branding.brand_name

  const link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (link) {
    link.href = branding.logo_url ?? '/favicon.ico'
  }
}

export default defineNuxtPlugin(async () => {
  const branding = useBranding()
  const slug = resolveSlug()

  try {
    const response = await $fetch<{ data: Branding }>(`/v1/public/branding?slug=${encodeURIComponent(slug)}`)
    branding.value = response.data
  } catch {
    // Branding stays at the mocked default during local visual-shell work.
  }

  applyBranding(branding.value)
})
