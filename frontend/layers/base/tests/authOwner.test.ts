import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { computed, ref } from 'vue'
import { ApiError } from '~/utils/apiError'

// build-phases.txt 3l: who counts as an owner, and the firm's modules the
// product switcher and platform's home page read.
describe('auth store: owner and modules', () => {
  const get = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia())
    get.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('computed', computed)
    vi.stubGlobal('useApi', () => ({ get, post: vi.fn(), del: vi.fn() }))
    vi.stubGlobal('useRuntimeConfig', () => ({ public: { product: '' } }))
  })
  async function store() {
    const { useAuthStore } = await import('~/composables/useAuthStore')
    return useAuthStore()
  }

  it("the firm's own login is an owner", async () => {
    get.mockResolvedValue({ principal_type: 'manager', manager: { id: '1' } })
    const auth = await store()
    await auth.fetchSession()
    expect(auth.isOwner).toBe(true)
  })
  it('a staff member is an owner only with the owner role', async () => {
    get.mockResolvedValue({ principal_type: 'staff', staff: { id: 's', role: 'accountant', name: 'A', email: 'a@b.c' }, manager: { id: '1' } })
    const auth = await store()
    await auth.fetchSession()
    expect(auth.isOwner).toBe(false)
    get.mockResolvedValue({ principal_type: 'staff', staff: { id: 's', role: 'owner', name: 'A', email: 'a@b.c' }, manager: { id: '1' } })
    await auth.fetchSession(true)
    expect(auth.isOwner).toBe(true)
  })
  it('nobody signed in, and a Super Admin, are not owners', async () => {
    const auth = await store()
    expect(auth.isOwner).toBe(false)
    get.mockResolvedValue({ principal_type: 'admin', admin: { id: '9', email: 'x@y.z' } })
    await auth.fetchSession()
    expect(auth.isOwner).toBe(false)
  })

  it('loads the modules once and drops them at sign-out', async () => {
    get.mockResolvedValueOnce({ principal_type: 'manager', manager: { id: '1' } })
    const auth = await store()
    await auth.fetchSession()
    get.mockResolvedValueOnce({ modules: [{ product: 'rentals', status: 'active' }] })
    await auth.fetchModules()
    await auth.fetchModules()
    expect(get).toHaveBeenCalledWith('/modules')
    expect(get).toHaveBeenCalledTimes(2) // the session and the modules, not the modules twice
    expect(auth.modules).toHaveLength(1)
    auth.expire()
    expect(auth.modules).toBeNull()
  })
  it('an admin has no modules to load', async () => {
    get.mockResolvedValue({ principal_type: 'admin', admin: { id: '9', email: 'x@y.z' } })
    const auth = await store()
    await auth.fetchSession()
    await auth.fetchModules()
    expect(auth.modules).toEqual([])
    expect(get).toHaveBeenCalledTimes(1)
  })
  it('a failed modules call leaves an empty list rather than an error', async () => {
    get.mockResolvedValueOnce({ principal_type: 'manager', manager: { id: '1' } })
    const auth = await store()
    await auth.fetchSession()
    get.mockRejectedValueOnce(new ApiError(500, 'boom'))
    await auth.fetchModules()
    expect(auth.modules).toEqual([])
  })
})
