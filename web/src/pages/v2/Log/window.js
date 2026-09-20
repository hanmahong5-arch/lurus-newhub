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

// window.js — the time window every Log-page query is bounded by. Extracted
// from pages/v2/Log/index.jsx by the cycle-13 wiring pass as a pure move: the
// two constants and computeStartTimeSec are byte-identical to the ones that
// stood there. web/src/file_size_ratchet.test.js holds index.jsx at its
// measured line count, so this cycle's additions to that page are paid for by
// an extraction rather than by raising the ceiling.
//
// Having them in a module of their own also makes the bound directly
// testable: the page mounts a live-polling table and a date picker, so
// asserting "what start_time does an empty filter produce" through the
// component meant driving the whole screen.

// DEFAULT_LOOKBACK_SEC bounds every logs/stat query made with no start date —
// which is how this page opens. Without it the list and the stat header both
// asked the database to consider every row the tenant ever wrote
// (serveLogStatV2 now applies a 30-day bound of its own; this is the narrower
// window the page actually renders).
export const DEFAULT_LOOKBACK_SEC = 7 * 24 * 3600; // 7 days

// ID_FILTER_LOOKBACK_SEC is the window an id search runs over:
// request_id/upstream_request_id filter the `other` JSON column via an
// unindexed extract (internal/adapter/repo/log.go, jsonOtherTextExpr), so it
// must never ride out unbounded. Same length as the default today, named apart
// because the two answer to different constraints and can move apart.
export const ID_FILTER_LOOKBACK_SEC = DEFAULT_LOOKBACK_SEC;

// Effective lower time bound: an explicit start wins; otherwise the lookback
// above, anchored on the end bound when one is set (so the computed start_time
// can never land after an explicit end_time) and on now() otherwise. The CSV
// export on the Log page builds its own query and is deliberately NOT bounded this way.
export const computeStartTimeSec = (start, end, hasIdFilter) => {
  if (start) return Math.floor(new Date(start).getTime() / 1000);
  const anchorSec = end
    ? Math.floor(new Date(end).getTime() / 1000)
    : Math.floor(Date.now() / 1000);
  return (
    anchorSec - (hasIdFilter ? ID_FILTER_LOOKBACK_SEC : DEFAULT_LOOKBACK_SEC)
  );
};
