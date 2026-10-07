import { appUrl, billingUrl, loginUrl, safeReturnTo, type AppUrls } from '../utils/appLinks'

// The one place a component asks "where is X?" across the Willcoll apps. All
// URLs come from runtimeConfig.public.apps (build-phases.txt 3l); nothing is
// hardcoded, so production's subdomains need only config.
export function useAppLinks() {
  const config = useRuntimeConfig().public
  const apps = config.apps as AppUrls
  // 'local': this app has the sign-in pages (platform). 'platform': it does not.
  const signInHere = config.guard.loginMode === 'local'

  /** Absolute URL of a path in platform ("" if not configured). */
  const platformUrl = (path = '/') => appUrl(apps, 'platform', path)

  /** Send the browser to the sign-in page, wherever it lives. */
  async function goToLogin(opts: { admin?: boolean } = {}) {
    const page = opts.admin ? config.guard.adminLoginPath : config.guard.loginPath
    if (signInHere) return navigateTo(page)
    if (opts.admin) return navigateTo(platformUrl(page), { external: true })
    return navigateTo(loginUrl(apps, window.location.href), { external: true })
  }

  /** Send the browser to this app's billing page (platform's, per product). */
  async function goToBilling() {
    if (signInHere) return navigateTo(config.product ? `/billing/${config.product}` : '/billing')
    return navigateTo(billingUrl(apps, config.product), { external: true })
  }

  return {
    apps,
    signInHere,
    platformUrl,
    accountUrl: () => platformUrl('/account'),
    billingUrl: (product = config.product) => billingUrl(apps, product),
    goToLogin,
    goToBilling,
    /** return_to from the current URL, only if it is an app of ours. */
    returnTo: (raw: unknown) => safeReturnTo(raw, apps),
  }
}
