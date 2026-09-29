// The short code printed under a unit's QR sticker: 12 characters from an
// unambiguous alphabet (no 0/O/1/I/L), shown as K7QM-2XH9-PTRB. It is typed by
// hand when the QR is damaged, so entry is case-insensitive and ignores dashes
// and spaces. The API applies the same rules; this only saves a round trip.
const ALPHABET = '23456789ABCDEFGHJKMNPQRSTUVWXYZ'
export const UNIT_CODE_LENGTH = 12

/** "k7qm 2xh9-ptrb" -> "K7QM2XH9PTRB" */
export function normalizeUnitCode(input: string): string {
  return input.replace(/[\s_-]/g, '').toUpperCase()
}

export function isUnitCode(input: string): boolean {
  const c = normalizeUnitCode(input)
  return c.length === UNIT_CODE_LENGTH && [...c].every(ch => ALPHABET.includes(ch))
}

/** Live formatting while typing: upper case, dashes every four, at most 12 characters. */
export function formatUnitCodeInput(input: string): string {
  const c = normalizeUnitCode(input).replace(/[^A-Z0-9]/g, '').slice(0, UNIT_CODE_LENGTH)
  return c.match(/.{1,4}/g)?.join('-') ?? ''
}
