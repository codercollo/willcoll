import { describe, expect, it, vi } from 'vitest'
import { createActivityPinger, PING_INTERVAL_MS } from '~/utils/activityPing'

function setup(over: { signedIn?: boolean; visible?: boolean; ping?: () => Promise<unknown> } = {}) {
  let t = 1_000_000
  const state = { signedIn: over.signedIn ?? true, visible: over.visible ?? true }
  const ping = vi.fn(over.ping ?? (() => Promise.resolve({})))
  const pinger = createActivityPinger({
    ping,
    isSignedIn: () => state.signedIn,
    isVisible: () => state.visible,
    now: () => t,
  })
  return { pinger, ping, state, advance: (ms: number) => { t += ms } }
}

const settle = () => new Promise((r) => setTimeout(r, 0))

describe('createActivityPinger', () => {
  it('pings on the first activity', () => {
    const { pinger, ping } = setup()
    expect(pinger.activity()).toBe(true)
    return settle().then(() => expect(ping).toHaveBeenCalledTimes(1))
  })

  it('debounces a burst of events into one ping', async () => {
    const { pinger, ping } = setup()
    for (let i = 0; i < 50; i++) pinger.activity()
    await settle()
    expect(ping).toHaveBeenCalledTimes(1)
  })

  it('pings again once the interval has passed, not before', async () => {
    const { pinger, ping, advance } = setup()
    pinger.activity()
    await settle()
    advance(PING_INTERVAL_MS - 1)
    expect(pinger.activity()).toBe(false)
    advance(1)
    expect(pinger.activity()).toBe(true)
    await settle()
    expect(ping).toHaveBeenCalledTimes(2)
  })

  it('does not ping while the tab is hidden, and does once visible', async () => {
    const { pinger, ping, state } = setup({ visible: false })
    expect(pinger.activity()).toBe(false)
    state.visible = true
    expect(pinger.activity()).toBe(true)
    await settle()
    expect(ping).toHaveBeenCalledTimes(1)
  })

  it('never pings a signed-out user', async () => {
    const { pinger, ping, state } = setup({ signedIn: false })
    expect(pinger.activity()).toBe(false)
    await settle()
    expect(ping).not.toHaveBeenCalled()
    state.signedIn = true
    expect(pinger.activity()).toBe(true)
  })

  it('does not overlap pings, and a failed ping waits for the next window', async () => {
    let reject!: (e: Error) => void
    const { pinger, ping, advance } = setup({ ping: () => new Promise((_, rj) => { reject = rj }) })
    pinger.activity()
    await settle()
    advance(PING_INTERVAL_MS)
    expect(pinger.activity()).toBe(false) // first still in flight
    reject(new Error('network'))
    await settle()
    expect(pinger.activity()).toBe(true) // settled, and the interval has passed
    await settle()
    expect(ping).toHaveBeenCalledTimes(2)
  })
})
