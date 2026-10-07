import { describe, expect, it } from 'vitest'
import { passwordPath } from '~/utils/account'
import { isPlatformPath } from '#base/utils/apiTarget'

describe('which route changes the password', () => {
  it('the firm login uses /account/password, a staff member /staff/me/password', () => {
    expect(passwordPath(false)).toBe('/account/password')
    expect(passwordPath(true)).toBe('/staff/me/password')
  })
  it('both are served by platform-api', () => {
    expect(isPlatformPath(passwordPath(false))).toBe(true)
    expect(isPlatformPath(passwordPath(true))).toBe(true)
  })
})
