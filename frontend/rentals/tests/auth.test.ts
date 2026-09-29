import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { computed, ref } from 'vue'
import { guardRedirect } from '~/utils/routeGuard'
import { ApiError } from '~/utils/apiError'

describe('guardRedirect', () => {
  it('sends signed-out visitors to login', () => {
    expect(guardRedirect('/properties', null)).toBe('/auth/login')
    expect(guardRedirect('/', null)).toBe('/auth/login')
    expect(guardRedirect('/admin/managers', null)).toBe('/admin/login')
  })
  it('leaves public pages alone', () => {
    for (const p of ['/auth/login', '/auth/signup', '/auth/activate', '/admin/login', '/pay/runda-arcade/G1']) {
      expect(guardRedirect(p, null)).toBeNull()
    }
  })
  it('never requires login on the pay page, even signed in', () => {
    expect(guardRedirect('/pay/kiwi-place/A1', 'manager')).toBeNull()
  })
  it('keeps managers out of /admin and admins out of manager pages', () => {
    expect(guardRedirect('/admin/managers', 'manager')).toBe('/properties')
    expect(guardRedirect('/properties', 'admin')).toBe('/admin/managers')
    expect(guardRedirect('/admin/managers', 'admin')).toBeNull()
  })
  it('skips sign-in pages when already signed in', () => {
    expect(guardRedirect('/auth/login', 'manager')).toBe('/properties')
    expect(guardRedirect('/admin/login', 'admin')).toBe('/admin/managers')
    expect(guardRedirect('/', 'manager')).toBe('/properties')
  })
})

describe('auth store', () => {
  const get = vi.fn()
  const post = vi.fn()
  const del = vi.fn()

  beforeEach(() => {
    setActivePinia(createPinia())
    get.mockReset(); post.mockReset(); del.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('computed', computed)
    vi.stubGlobal('useApi', () => ({ get, post, del }))
  })

  async function store() {
    const { useAuthStore } = await import('~/stores/auth')
    return useAuthStore()
  }

  it('treats a 401 from /sessions as signed out, not an error', async () => {
    get.mockRejectedValue(new ApiError(401, 'no'))
    const auth = await store()
    await auth.fetchSession()
    expect(auth.principal).toBeNull()
    expect(auth.loaded).toBe(true)
  })

  it('rethrows other failures', async () => {
    get.mockRejectedValue(new ApiError(500, 'boom'))
    const auth = await store()
    await expect(auth.fetchSession()).rejects.toThrow('boom')
    expect(auth.loaded).toBe(false)
  })

  it('reads a manager session once', async () => {
    get.mockResolvedValue({ principal_type: 'manager', manager: { id: '1', firm_name: 'Kimani', email: 'a@b.c' } })
    const auth = await store()
    await auth.fetchSession()
    await auth.fetchSession()
    expect(get).toHaveBeenCalledTimes(1)
    expect(auth.principal).toBe('manager')
    expect(auth.manager?.firm_name).toBe('Kimani')
  })

  it('logs in and out', async () => {
    post.mockResolvedValue({ manager: { id: '1', firm_name: 'K' } })
    del.mockResolvedValue({})
    get.mockResolvedValue({ principal_type: 'manager', manager: { id: '1', firm_name: 'K' }, access: { state: 'trial', days_left: 7 } })
    const auth = await store()
    await auth.login('a@b.c', 'pw')
    expect(post).toHaveBeenCalledWith('/sessions', { email: 'a@b.c', password: 'pw' })
    expect(auth.principal).toBe('manager')
    expect(auth.access?.state).toBe('trial') // login reads the trial standing
    await auth.logout()
    expect(auth.principal).toBeNull()
  })

  it('a failed login leaves the store signed out', async () => {
    post.mockRejectedValue(new ApiError(401, 'wrong credentials'))
    const auth = await store()
    await expect(auth.login('a@b.c', 'bad')).rejects.toThrow('wrong credentials')
    expect(auth.principal).toBeNull()
  })

  it('expire() clears the session', async () => {
    get.mockResolvedValue({ principal_type: 'admin', admin: { id: '9', email: 'x@y.z' } })
    const auth = await store()
    await auth.fetchSession()
    auth.expire()
    expect(auth.principal).toBeNull()
    expect(auth.admin).toBeNull()
  })
})

describe('paywall guard', () => {
  it('keeps an expired firm on the pages needed to pay', () => {
    expect(guardRedirect('/properties', 'manager', 'expired')).toBe('/billing')
    expect(guardRedirect('/payments', 'manager', 'expired')).toBe('/billing')
    expect(guardRedirect('/billing', 'manager', 'expired')).toBeNull()
    expect(guardRedirect('/account', 'manager', 'expired')).toBeNull()
  })
  it('never paywalls a firm on a trial or a plan', () => {
    expect(guardRedirect('/properties', 'manager', 'trial')).toBeNull()
    expect(guardRedirect('/properties', 'manager', 'subscribed')).toBeNull()
    expect(guardRedirect('/properties', 'manager', null)).toBeNull()
  })
  it('never paywalls tenants or the admin', () => {
    expect(guardRedirect('/pay/runda-arcade/G1', null, 'expired')).toBeNull()
    expect(guardRedirect('/admin/managers', 'admin', 'expired')).toBeNull()
  })
})
