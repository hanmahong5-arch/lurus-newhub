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
/** Wallet amounts are plain CNY floats from the platform. */
export function formatCny(v: number | null | undefined): string {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '--'
  return `${v < 0 ? '-' : ''}¥${Math.abs(v).toFixed(2)}`
}

/** Like formatCny but a positive amount carries an explicit "+". */
export function formatSignedCny(v: number | null | undefined): string {
  if (typeof v !== 'number' || !Number.isFinite(v)) return '--'
  return `${v > 0 ? '+' : ''}${formatCny(v)}`
}

/** RFC 3339 -> local "YYYY-MM-DD HH:mm"; unparsable input is shown as-is. */
export function formatIso(iso: string | null | undefined): string {
  if (!iso) return '-'
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ${pad(at.getHours())}:${pad(at.getMinutes())}`
}
