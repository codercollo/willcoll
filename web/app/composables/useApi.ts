// Authenticated fetch wrapper for every logged-in screen. On a real 401
// (expired/invalid token) it calls POST /v1/auth/refresh once and retries
// the original request — not a timer guessing when the token might expire.
export function useApi() {
  const auth = useAuth()
  let refreshing: Promise<string> | null = null

  async function request<T>(url: string, opts: Record<string, any> = {}): Promise<T> {
    const withAuth = (headers: Record<string, any> = {}) =>
      auth.token.value ? { ...headers, Authorization: `Bearer ${auth.token.value}` } : headers

    try {
      return await $fetch<T>(url, { ...opts, headers: withAuth(opts.headers) })
    } catch (e: any) {
      if (e?.response?.status !== 401 || !auth.token.value) throw e

      try {
        refreshing ??= auth.refresh().finally(() => { refreshing = null })
        await refreshing
      } catch {
        auth.logout()
        const slug = useRoute().params.slug
        await navigateTo(`/${slug}/login`)
        throw e
      }

      return await $fetch<T>(url, { ...opts, headers: withAuth(opts.headers) })
    }
  }

  return { request }
}
