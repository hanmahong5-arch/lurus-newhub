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
// Invite links. An operator issues `${origin}/login?invite=<code>`; the sign-in
// flow forwards the code to the bootstrap call and carries it through the
// identity round trip so the visitor comes back to the same link.

const INVITE_CODE_RE = /^[A-Za-z0-9_-]{1,64}$/

/**
 * The delivery URL an operator sends an invitee. Both the issuing side and
 * the return_to this flow rebuilds go through here, so the shape cannot drift
 * between "issue" and "carry it back".
 */
export function inviteLink(origin: string, code: string): string {
  return `${origin}/login?invite=${encodeURIComponent(code)}`
}

/**
 * ?invite=<code> from the current URL. URL-safe and bounded: a malformed or
 * absent value counts as "no invite" (it is simply not forwarded) rather than
 * blocking the sign-in.
 */
export function readInviteCode(
  search: string = window.location.search
): string | null {
  let raw: string | null = null
  try {
    raw = new URLSearchParams(search).get('invite')
  } catch {
    return null
  }
  return raw && INVITE_CODE_RE.test(raw) ? raw : null
}
