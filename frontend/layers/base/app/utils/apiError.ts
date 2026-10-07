// Failure envelope: { error: string | { field: message }, code?, failures? }
// (features 1.2). `code` is a machine-readable branch key some endpoints
// add alongside the human message (payment_particulars_missing,
// period_not_confirmed, period_stale, sanity_failed) — callers should
// switch on it instead of matching `message` text.
export class ApiError extends Error {
  status: number
  fields: Record<string, string>
  /** The raw error object when the API sent structured detail (e.g. CSV import rows). */
  details: Record<string, unknown> | null
  code: string | null
  /** Sanity-check failures, present when code === 'sanity_failed'. */
  failures: { code: string; message: string }[]
  constructor(
    status: number, message: string, fields: Record<string, string> = {}, details: Record<string, unknown> | null = null,
    code: string | null = null, failures: { code: string; message: string }[] = [],
  ) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.fields = fields
    this.details = details
    this.code = code
    this.failures = failures
  }
  get isAuth() { return this.status === 401 }
  get isForbidden() { return this.status === 403 }
  get isConflict() { return this.status === 409 }
  get isValidation() { return this.status === 422 }
  get isRateLimited() { return this.status === 429 }
}

export function defaultMessage(status: number): string {
  switch (status) {
    case 401: return 'Please sign in to continue.'
    case 403: return 'Your account cannot do this right now.'
    case 404: return 'We could not find that.'
    case 409: return 'This was changed by someone else. Reload and try again.'
    case 422: return 'Some details need fixing.'
    case 429: return 'Too many attempts. Please wait a moment and try again.'
    case 0: return 'Cannot reach the server. Check your connection.'
    default: return status >= 500 ? 'Something went wrong on our side. Please try again.' : 'The request could not be completed.'
  }
}

export function parseApiError(status: number, body: unknown): ApiError {
  const top = body as { error?: unknown; code?: unknown; failures?: unknown } | null
  const code = typeof top?.code === 'string' ? top.code : null
  const failures = Array.isArray(top?.failures) ? top.failures as { code: string; message: string }[] : []
  const err = top?.error
  if (typeof err === 'string') return new ApiError(status, err, {}, null, code, failures)
  if (err && typeof err === 'object') {
    const obj = err as Record<string, unknown>
    // Structured detail (import rows): keep it whole, message stays human.
    if (Array.isArray(obj.rows)) return new ApiError(status, String(obj.message ?? defaultMessage(status)), {}, obj, code, failures)
    const fields = Object.fromEntries(Object.entries(obj).filter(([, v]) => typeof v === 'string') as [string, string][])
    const first = Object.values(fields)[0] ?? defaultMessage(status)
    return new ApiError(status, first, fields, null, code, failures)
  }
  return new ApiError(status, defaultMessage(status), {}, null, code, failures)
}
