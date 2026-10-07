import type { UnclaimedChannel } from '~/types/admin'

/** One line for an unclaimed channel: the channel id and, when known, its property. */
export function unclaimedChannelLabel(c: UnclaimedChannel): string {
  return c.property_name ? `${c.channel_id} (${c.property_name})` : c.channel_id
}

/**
 * The "Unclaimed channels" entry on the System page. An old rentals-api that
 * does not send the field reads as none, not as a crash.
 */
export function unclaimedChannelsSummary(list: UnclaimedChannel[] | undefined | null): { count: number; text: string } {
  const items = list ?? []
  return {
    count: items.length,
    text: items.length ? items.map(unclaimedChannelLabel).join(', ') : 'every channel that took a payment is claimed',
  }
}
