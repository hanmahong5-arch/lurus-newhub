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

// Shared loading placeholder for v2 tables/panels — replaces the plain
// "Loading…"/"加载中…" text swap that used to be the only loading state
// (fixPlan #8), so a slow network doesn't cause a content jump when real
// rows mount. Pure CSS shimmer (`.hf-skeleton` in hifi-tokens.css), already
// wired to `prefers-reduced-motion`.
//
// Each placeholder row is a single shimmer bar (varying width, like a
// line of text/a table row) rather than trying to mirror a specific
// table's exact column layout — callers show/hide this in the same
// conditional slot the old "Loading…" text sat in, so it doesn't need to
// know column count.
import React from 'react';

const DEFAULT_ROW_WIDTHS = ['86%', '62%', '74%', '48%', '68%'];

/**
 * @param {number} rows - how many placeholder rows to render
 */
const HfSkeletonRows = ({ rows = 4 }) => (
  <div role='presentation' aria-hidden='true'>
    {Array.from({ length: rows }).map((_, r) => (
      <div className='hf-skeleton-row' key={r}>
        <span
          className='hf-skeleton'
          style={{ width: DEFAULT_ROW_WIDTHS[r % DEFAULT_ROW_WIDTHS.length] }}
        />
      </div>
    ))}
  </div>
);

export default HfSkeletonRows;
