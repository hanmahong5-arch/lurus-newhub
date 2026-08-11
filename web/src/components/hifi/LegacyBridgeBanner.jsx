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

// LegacyBridgeBanner — sits at the top of the remaining `/console/*` v1
// Semi UI pages (User/Setting/Personal/TopUp…) to tell users they're on the
// old design system and link them to the v2 hi-fi equivalent. Mirrors
// WIPBanner.jsx's pattern (same `--hf-*` custom properties, which are
// declared on `:root` in hifi-tokens.css so they resolve here even though
// this component isn't nested under a `.hf` ancestor).
//
// See doc/coord/ui-industrial-audit-2026-07-07.md fixPlan #11/#12 — the two
// design systems are recorded debt (not migrated this round); this is the
// minimum-cost transition affordance called for there.
import React from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';

/**
 * @param {string} to - v2 route this legacy page's functionality moved to.
 */
const LegacyBridgeBanner = ({ to }) => {
  const { t } = useTranslation();
  return (
    <div
      role='note'
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 12,
        flexWrap: 'wrap',
        margin: '0 0 12px',
        padding: '8px 14px',
        border: '1px dashed var(--hf-rule-strong)',
        borderRadius: 2,
        background: 'var(--hf-sunken)',
        color: 'var(--hf-ink-2)',
        fontFamily: 'var(--hf-mono)',
        fontSize: 11,
        lineHeight: 1.5,
      }}
    >
      <span>
        {t(
          'console.legacy_bridge.notice',
          'This is the legacy console. The new console has a redesigned version of this page.',
        )}
      </span>
      <Link
        to={to}
        style={{
          color: 'var(--hf-accent-text)',
          textDecoration: 'underline',
          whiteSpace: 'nowrap',
        }}
      >
        {t('console.legacy_bridge.cta', 'Open in new console →')}
      </Link>
    </div>
  );
};

export default LegacyBridgeBanner;
