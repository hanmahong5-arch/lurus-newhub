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
// Where to send a person once they have signed in.
//
// An unauthenticated visit to any console page is sent to
// /login?redirect=<path> (lib/auth.ts) and the sign-in flow brings them back.
// The value comes from the URL, so it is attacker-controlled: only a
// same-site relative path is accepted, anything else falls back to the
// default landing page.

export const DEFAULT_LANDING = '/dashboard'

/**
 * Accepts "/keys?x=1#y" and rejects everything that could leave the site:
 * absolute URLs, protocol-relative "//host", backslash tricks ("/\host" is
 * read as a double slash by browsers), control characters, and the sign-in
 * routes themselves (a bounce back to /login would loop).
 */
export function safeRedirectPath(raw: unknown): string | null {
  if (typeof raw !== 'string' || raw === '' || raw.length > 2048) return null
  if (raw[0] !== '/' || raw[1] === '/' || raw[1] === '\\') return null
  // oxlint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f\\]/.test(raw)) return null
  let url: URL
  try {
    url = new URL(raw, 'http://redirect.invalid')
  } catch {
    return null
  }
  if (url.origin !== 'http://redirect.invalid') return null
  if (/^\/(login|bridge-login|register)(\/|$)/.test(url.pathname)) return null
  return url.pathname + url.search + url.hash
}

/** The ?redirect= value on the current URL, validated; null when absent or unsafe. */
export function readRedirectParam(
  search: string = window.location.search
): string | null {
  try {
    return safeRedirectPath(new URLSearchParams(search).get('redirect'))
  } catch {
    return null
  }
}

/** Absolute URL to land on after sign-in. */
export function postLoginUrl(origin: string = window.location.origin): string {
  return origin + (readRedirectParam() ?? DEFAULT_LANDING)
}
