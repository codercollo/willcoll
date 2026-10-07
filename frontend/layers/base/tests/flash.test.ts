import { describe, expect, it } from 'vitest'
import { flashOf } from '~/utils/flash'

describe('flashOf', () => {
  it('reads the top-level flash string', () => {
    expect(flashOf({ message: 'x', flash: ' Your password has been reset. ' })).toBe('Your password has been reset.')
  })
  it('ignores a missing or non-string flash', () => {
    expect(flashOf({})).toBe('')
    expect(flashOf(null)).toBe('')
    expect(flashOf({ flash: 3 })).toBe('')
  })
  it('is present on error bodies too', () => {
    expect(flashOf({ error: 'nope', flash: 'Signed out.' })).toBe('Signed out.')
  })
})
