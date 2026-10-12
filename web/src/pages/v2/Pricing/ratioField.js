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

// The ratio input of one pricing row. A row the server flags
// price_configured=false has no ratio (its model_ratio is 0, not a price), so
// the field starts empty with a "not priced" hint instead of showing 0.
export function ratioFieldProps(row, edits, tr) {
  const unpriced = row.price_configured === false;
  return {
    value:
      edits[row.model_name]?.model_ratio ??
      (unpriced ? '' : (row.model_ratio ?? '')),
    placeholder: unpriced
      ? tr('console.pricing.not_priced', 'Not priced')
      : undefined,
  };
}
