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

// cycle-13 L7 (console honesty). The two pieces pages/v2/Dashboard/index.jsx
// repeats once its fetches can fail out loud:
//
//   - LoadErrorPanel, the banner above the page body once either KPI-strip
//     fetch settled into a non-ok outcome;
//   - KpiCaption + captionText, the caption slot under a KPI number, which
//     has to say something different when the panel is empty because the
//     fetch failed than when it is empty because the account is idle.
//
// They live here rather than inline because the page is at its size ceiling
// (src/file_size_ratchet.test.js) and because the caption rule was six
// copies of the same ternary, which is six chances to fix the copy in five
// places.

import React from 'react';
import { useTranslation } from 'react-i18next';
import { isLoadFailed } from '../../../helpers/loadState';

const CAPTION_STYLE = {
  marginTop: 8,
  fontSize: 10,
  color: 'var(--hf-ink-3)',
  fontFamily: 'var(--hf-mono)',
};

/**
 * The mono caption line under a KPI number. Styling only — the text is the
 * caller's, so the page keeps its copy decisions visible at the call site.
 */
export function KpiCaption({ children }) {
  return <div style={CAPTION_STYLE}>{children}</div>;
}

/**
 * Picks what an empty panel's caption says. `okText` ("no traffic in last 5
 * min", "No recent requests found.") asserts that a check happened and
 * found nothing, which is true when the fetch behind the panel answered and
 * false when it did not — so any non-ok classifyLoad() status swaps in
 * `failedText` instead.
 *
 * Before the first attempt settles (status null/undefined) neither claim is
 * true yet: the check has not happened, so it has neither found nothing nor
 * failed. The first cut returned `okText` there on the reasoning that the
 * page was showing loading placeholders anyway — but the caption IS on
 * screen under the "…" KPI number, and it read "no traffic in last 5 min"
 * for a check that had not run (cycle-13 acceptance minor). It now says
 * `pendingText`, or nothing at all when the caller has no pending copy —
 * an empty caption asserts nothing, which is the honest floor.
 *
 * @param {'ok'|'forbidden'|'unauthenticated'|'error'|null|undefined} status
 * @param {string} okText
 * @param {string} failedText
 * @param {string} [pendingText]
 * @returns {string}
 */
export function captionText(status, okText, failedText, pendingText) {
  if (status === null || status === undefined) return pendingText ?? '';
  return isLoadFailed(status) ? failedText : okText;
}

/**
 * Banner shown above the dashboard body when a KPI-strip fetch failed. One
 * banner covers the failure shapes classifyLoad() distinguishes (forbidden
 * / unauthenticated / error) because they all leave the reader with the
 * same next step: retry.
 */
export default function LoadErrorPanel({ onRetry }) {
  const { t } = useTranslation();
  return (
    <div
      data-testid='dashboard-load-error'
      className='panel'
      style={{
        margin: '14px 24px 0',
        padding: '14px 18px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 12,
        flexWrap: 'wrap',
        borderColor: 'var(--hf-err)',
      }}
    >
      <span style={{ fontSize: 12 }}>
        {t('console.dashboard.load_failed', 'Unable to load — retry')}
      </span>
      <button
        type='button'
        className='btn ghost sm'
        data-testid='dashboard-retry-btn'
        onClick={onRetry}
      >
        {t('console.common.retry', 'retry')}
      </button>
    </div>
  );
}
