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
/**
 * Full-page navigation, kept behind one seam.
 *
 * Every sign-in step ends in a real browser navigation, not a router
 * transition: the session cookie has just changed, and the next page must be
 * loaded fresh so no query cache from the signed-out state survives. Tests
 * replace this module instead of faking `window.location`.
 */
export const navigation = {
  /** Go to `url`, replacing the current history entry. */
  replace: (url: string): void => {
    window.location.replace(url)
  },
  /** Go to `url`, keeping the current entry in history. */
  assign: (url: string): void => {
    window.location.assign(url)
  },
  reload: (): void => {
    window.location.reload()
  },
}
