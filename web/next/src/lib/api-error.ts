/**
 * The one error type every API call rejects with.
 *
 * newhub answers failures with `{ success: false, message, error_code? }` and
 * an HTTP status; both transport failures and business failures are folded into
 * this class so callers (and the toast layer) only ever deal with one shape.
 */
export class ApiError extends Error {
  readonly status?: number
  readonly code?: string

  constructor(
    message: string,
    options: { status?: number; code?: string; cause?: unknown } = {}
  ) {
    super(message, { cause: options.cause })
    this.name = 'ApiError'
    this.status = options.status
    this.code = options.code
  }
}

export function isApiError(value: unknown): value is ApiError {
  return value instanceof ApiError
}
