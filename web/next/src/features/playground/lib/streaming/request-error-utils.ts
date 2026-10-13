/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { ERROR_MESSAGES } from '../../constants'

type ErrorBody = {
  /** Relay failure: `{ error: { message, code } }`. */
  error?: {
    code?: string
    message?: string
  }
  /** Console envelope failure (tenant gate, session): `{ message, error_code }`. */
  message?: string
  error_code?: string
}

type RequestErrorLike = {
  message?: string
  code?: string
  /** ApiError wraps the original axios error here. */
  cause?: unknown
  response?: { data?: unknown }
}

export type RequestErrorDetails = {
  errorCode?: string
  errorMessage: string
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object'
    ? (value as Record<string, unknown>)
    : undefined
}

function readBody(error: RequestErrorLike): ErrorBody | undefined {
  const direct = asRecord(error.response?.data)
  if (direct) return direct as ErrorBody
  const cause = asRecord(error.cause) as RequestErrorLike | undefined
  return asRecord(cause?.response?.data) as ErrorBody | undefined
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined
}

export function parseRequestErrorDetails(error: unknown): RequestErrorDetails {
  const requestError = (asRecord(error) ?? {}) as RequestErrorLike
  const body = readBody(requestError)

  return {
    errorCode:
      text(body?.error?.code) ??
      text(body?.error_code) ??
      text(requestError.code),
    errorMessage:
      text(body?.error?.message) ??
      text(body?.message) ??
      text(requestError.message) ??
      ERROR_MESSAGES.API_REQUEST_ERROR,
  }
}
