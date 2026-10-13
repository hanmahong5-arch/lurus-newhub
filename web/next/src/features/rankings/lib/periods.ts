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
 * The rankings endpoint snaps `hours` to 1, 6, 24, 168 or 720, so the longest
 * window is 30 days: there is no yearly view.
 */
export type RankingPeriod = 'today' | 'week' | 'month';

export type RankingHours = 24 | 168 | 720;

export const PERIODS: ReadonlyArray<{
  id: RankingPeriod;
  hours: RankingHours;
  /** English source of the tab label. */
  label: string;
}> = [
  { id: 'today', hours: 24, label: 'Today' },
  { id: 'week', hours: 168, label: 'Week' },
  { id: 'month', hours: 720, label: 'Month' },
];

export const DEFAULT_PERIOD: RankingPeriod = 'week';

export function hoursOf(period: RankingPeriod): RankingHours {
  return PERIODS.find((p) => p.id === period)?.hours ?? 168;
}

/**
 * Dimensions the market-share section can group by. `vendor` is left out on
 * purpose (platform staff only; it reveals the supply chain) and `model` has
 * its own section above.
 */
export type RankingDimension =
  | 'model'
  | 'group'
  | 'product'
  | 'user'
  | 'key'
  | 'relay_mode'
  | 'usage_unit';

export const SHARE_DIMENSIONS: ReadonlyArray<{
  id: Exclude<RankingDimension, 'model'>;
  /** English source of the tab label. */
  label: string;
}> = [
  { id: 'group', label: 'Group' },
  { id: 'product', label: 'Product' },
  { id: 'user', label: 'Member' },
  { id: 'key', label: 'API key' },
  { id: 'relay_mode', label: 'Endpoint kind' },
  { id: 'usage_unit', label: 'Billing unit' },
];

/** English source of a section's period caption. */
export function periodCaption(period: RankingPeriod): string {
  if (period === 'today') return 'the last 24 hours';
  if (period === 'week') return 'the last 7 days';
  return 'the last 30 days';
}
