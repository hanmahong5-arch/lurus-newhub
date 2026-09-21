/*
Copyright (C) 2025 QuantumNous

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

// What to hand showError() when a redeem fails. A body that carries an
// error_code (REDEMPTION_INVALID / _USED / _EXPIRED / _TENANT_MISMATCH /
// _FAILED, cycle-13 L3) is returned as an axios-shaped error OBJECT so
// showError's resolveErrorMessage branch turns the code into the localized
// sentence — the backend's message is Chinese by contract (the Switch
// desktop client greps substrings out of it), not console copy. Passing the
// message STRING, as this page did before, bypassed that mapping and left
// the REDEMPTION_* cases in helpers/errorMessages.js unreachable from the
// only page that redeems (cycle-13 acceptance minor). A body without a code
// falls back to its message, then to the generic line.
export function redeemFailure(body, fallback, status) {
  if (body?.error_code) return { response: { status, data: body } };
  return body?.message || fallback;
}
