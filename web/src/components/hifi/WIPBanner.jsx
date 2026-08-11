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
import React from 'react';
import { useTranslation } from 'react-i18next';

/**
 * WIPBanner — explicit "not yet wired" marker for v2 hi-fi pages whose
 * content is still design-mock content. Surfaces honesty per §4.1 ⑥:
 * "markers present" vs "behavior verified" — this is the marker.
 *
 * Usage:
 *   <WIPBanner
 *     reason="No backend endpoint for X yet"
 *     todo="path/to/issue or planning artifact"
 *   />
 */
const WIPBanner = ({ reason, todo }) => {
  const { t } = useTranslation();
  return (
    <div
      role='status'
      aria-label={t('console.wip.aria', 'work in progress')}
      style={{
        margin: '14px 24px 0',
        padding: '10px 14px',
        border: '1px dashed var(--hf-warn)',
        borderRadius: 2,
        background: 'var(--hf-warn-bg)',
        color: 'var(--hf-ink-2)',
        fontFamily: 'var(--hf-mono)',
        fontSize: 11,
        lineHeight: 1.55,
      }}
    >
      <div
        style={{ fontWeight: 600, marginBottom: 2, letterSpacing: '0.04em' }}
      >
        {t(
          'console.wip.title',
          '⚠ WORK IN PROGRESS — content is design-mock only',
        )}
      </div>
      {reason && (
        <div style={{ opacity: 0.85 }}>
          {t('console.wip.reason_label', 'Reason:')} {reason}
        </div>
      )}
      {todo && (
        <div style={{ opacity: 0.7 }}>
          {t('console.wip.todo_label', 'TODO:')} {todo}
        </div>
      )}
    </div>
  );
};

export default WIPBanner;
