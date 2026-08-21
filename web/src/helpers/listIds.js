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

/**
 * Give every row in a stored console list a usable, distinct id.
 *
 * The console settings panels (announcements, FAQ, API info, uptime groups)
 * each keep their rows as a JSON blob in one option string, and every edit and
 * delete matches on `id`. Two rows sharing an id therefore means an operation
 * aimed at one of them silently hits both, and the next save writes that loss
 * back.
 *
 * The blobs predate the id field, so rows without one still exist and have to
 * be numbered on load. The obvious `item.id || index + 1` is wrong twice over:
 * it renumbers a legitimate stored id of 0 because 0 is falsy, and the
 * index-derived number it hands an id-less row is drawn from the same range as
 * the stored ids, so `[{name: 'A'}, {id: 1, name: 'B'}]` gives both rows id 1.
 *
 * So: keep every id the blob actually stored, including 0, and draw synthesised
 * ones from above the highest stored id, where they cannot collide.
 *
 * Rows that arrive already sharing an explicit id are left as they are — the
 * stored ids are what the operator's other edits refer to, and renumbering them
 * here would trade one silent mismatch for another.
 */
export const withDistinctIds = (list) => {
  let nextId = list.reduce(
    (max, item) => (Number.isFinite(item?.id) ? Math.max(max, item.id) : max),
    0,
  );
  return list.map((item) =>
    item?.id == null ? { ...item, id: ++nextId } : item,
  );
};
