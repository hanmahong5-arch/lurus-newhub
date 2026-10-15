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
/** Healthy cadence of the live view. */
export const POLL_MS = 15_000
/**
 * While the endpoint is failing, keep checking, but slowly: stopping outright
 * freezes the page after one transient 502 during a rolling update, polling
 * at the healthy rate hammers a dead endpoint.
 */
export const ERROR_POLL_MS = 30_000

/** "86400" -> "24h", "300" -> "5m", "45" -> "45s", 0/invalid -> em dash. */
export function formatInterval(secs: number | null | undefined): string {
  const n = Number(secs ?? 0)
  if (!Number.isFinite(n) || n <= 0) return '—'
  if (n % 3600 === 0) return `${n / 3600}h`
  if (n % 60 === 0) return `${n / 60}m`
  return `${n}s`
}

/** HTTP status carried by a thrown ApiError, if any. */
export function errorStatus(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) return undefined
  const status = (error as { status?: unknown }).status
  return typeof status === 'number' ? status : undefined
}

/** A refusal (403) or expired session (401): polling again cannot help. */
export function isTerminalRefusal(error: unknown): boolean {
  const status = errorStatus(error)
  return status === 401 || status === 403
}
