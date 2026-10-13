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
import type { RankingRow } from './api';

/** Format a token count as `1.2B`, `42M`, `980K`, or `512`. */
export function formatTokens(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0';
  if (value >= 1_000_000_000_000) {
    return `${(value / 1_000_000_000_000).toFixed(2)}T`;
  }
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(value >= 10_000_000_000 ? 1 : 2)}B`;
  }
  if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M`;
  }
  if (value >= 1_000) {
    return `${(value / 1_000).toFixed(value >= 10_000 ? 0 : 1)}K`;
  }
  return value.toLocaleString();
}

/** A 0..100 share as a percentage; tiny shares stay visible as "<0.1%". */
export function formatSharePct(pct: number): string {
  if (!Number.isFinite(pct) || pct <= 0) return '0%';
  if (pct < 0.1) return '<0.1%';
  return `${pct.toFixed(pct < 1 ? 2 : 1)}%`;
}

/** Biggest rank gainers / losers; a brand-new entry has no rank change. */
export function movers(rows: RankingRow[], limit = 5) {
  const up = rows
    .filter((r) => r.rank_delta > 0)
    .sort((a, b) => b.rank_delta - a.rank_delta || a.rank - b.rank)
    .slice(0, limit);
  const down = rows
    .filter((r) => r.rank_delta < 0)
    .sort((a, b) => a.rank_delta - b.rank_delta || a.rank - b.rank)
    .slice(0, limit);
  const fresh = rows.filter((r) => r.is_new).slice(0, limit);
  return { up, down, fresh };
}
