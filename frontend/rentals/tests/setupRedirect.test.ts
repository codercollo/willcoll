import { afterEach, describe, expect, it, vi } from 'vitest'
import { markSetupRedirected, readSetupRedirected, shouldSendToSetup } from '~/utils/setupRedirect'

describe('shouldSendToSetup', () => {
  it('sends a firm with no properties that has not been sent yet', () => {
    expect(shouldSendToSetup(0, 'firm-1', null)).toBe(true)
  })
  it('never sends a firm that has a property', () => {
    expect(shouldSendToSetup(1, 'firm-1', null)).toBe(false)
    expect(shouldSendToSetup(12, 'firm-1', 'someone-else')).toBe(false)
  })
  it('sends a firm only once, but a different firm on the same tab still gets it', () => {
    expect(shouldSendToSetup(0, 'firm-1', 'firm-1')).toBe(false)
    expect(shouldSendToSetup(0, 'firm-2', 'firm-1')).toBe(true)
  })
  it('does nothing before we know who is signed in', () => {
    expect(shouldSendToSetup(0, undefined, null)).toBe(false)
  })
})

describe('the sent-once flag', () => {
  afterEach(() => { sessionStorage.clear(); vi.restoreAllMocks() })

  it('remembers the firm for the tab', () => {
    expect(readSetupRedirected()).toBeNull()
    markSetupRedirected('firm-1')
    expect(readSetupRedirected()).toBe('firm-1')
  })
  it('survives storage that throws (private window): reads nothing, writes nothing, no crash', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    expect(readSetupRedirected()).toBeNull()
    expect(() => markSetupRedirected('firm-1')).not.toThrow()
  })
})
