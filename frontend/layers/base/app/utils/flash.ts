// The backend puts a one-time SCS flash message on the next JSON response of
// the session, success or error, as a top-level "flash" string (writeJSON).
export function flashOf(body: unknown): string {
  const f = (body as { flash?: unknown } | null)?.flash
  return typeof f === 'string' ? f.trim() : ''
}
