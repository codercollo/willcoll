import { describe, expect, it } from 'vitest'
import { unclaimedChannelLabel, unclaimedChannelsSummary } from '~/utils/systemHealth'

const runda = { channel_id: 'PH-CH-RUNDA-DEMO', property_id: 'p1', property_name: "THE RUNDA'S ARCADE", count: 3, last_seen_at: '2026-10-07T10:00:00Z' }
const bare = { channel_id: 'CH-X', count: 1, last_seen_at: '2026-10-07T10:00:00Z' }

describe('unclaimedChannelsSummary', () => {
  it('counts and lists channels with their property', () => {
    const s = unclaimedChannelsSummary([runda, bare])
    expect(s.count).toBe(2)
    expect(s.text).toBe("PH-CH-RUNDA-DEMO (THE RUNDA'S ARCADE), CH-X")
  })
  it('reads none, a missing field and null as zero', () => {
    expect(unclaimedChannelsSummary([]).count).toBe(0)
    expect(unclaimedChannelsSummary(undefined).count).toBe(0)
    expect(unclaimedChannelsSummary(null).count).toBe(0)
  })
  it('labels a channel without a known property by its id alone', () => {
    expect(unclaimedChannelLabel(bare)).toBe('CH-X')
  })
})
