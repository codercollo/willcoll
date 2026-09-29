import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ref } from 'vue'

describe('qr store', () => {
  const get = vi.fn(); const post = vi.fn(); const blob = vi.fn()
  beforeEach(() => {
    setActivePinia(createPinia()); for (const f of [get, post, blob]) f.mockReset()
    vi.stubGlobal('ref', ref)
    vi.stubGlobal('useApi', () => ({ get, post, blob }))
  })
  const store = async () => (await import('~/stores/qr')).useQr()

  it('gets the lease code, which the API creates on first use', async () => {
    get.mockResolvedValue({ qr: { short_code: 'K7QM-2XH9-PTRB', url: 'https://willcoll.app/q/K7QM2XH9PTRB' } })
    const q = await (await store()).get('l1')
    expect(get).toHaveBeenCalledWith('/leases/l1/qr')
    expect(q.short_code).toBe('K7QM-2XH9-PTRB')
  })
  it('rotates with a POST and returns the new code', async () => {
    post.mockResolvedValue({ qr: { short_code: '8B39-QASA-YC43' } })
    expect((await (await store()).rotate('l1')).short_code).toBe('8B39-QASA-YC43')
    expect(post).toHaveBeenCalledWith('/leases/l1/qr/rotate')
  })
  it('fetches the PNG through the credentialed client', async () => {
    const png = new Blob(['x'], { type: 'image/png' })
    blob.mockResolvedValue(png)
    expect(await (await store()).image('l1')).toBe(png)
    expect(blob).toHaveBeenCalledWith('/leases/l1/qr.png')
  })
})
