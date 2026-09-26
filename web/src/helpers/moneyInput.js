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
import { showError } from './index';
import { parseUSDInput } from './formatting';

/**
 * Read a dollar field on submit. Returns the amount, or null for an empty
 * field. When the text cannot be read as an amount it tells the person and
 * returns undefined, so the caller stops instead of saving a wrong number.
 */
export function readUSDField(raw, tr) {
  const usd = parseUSDInput(raw);
  if (!Number.isNaN(usd)) return usd;
  showError(
    tr(
      'console.common.invalid_amount',
      'Enter an amount in dollars, like 5 or 12.50',
    ),
  );
  return undefined;
}
