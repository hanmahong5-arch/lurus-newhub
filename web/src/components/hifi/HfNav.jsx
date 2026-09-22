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

// Sidebar sections and items for HFShell.
//
// A root operator saw 26 links in one flat column. Sections marked
// `collapsible` (the two admin sections) now fold behind their heading:
// closed by default, always open while the current page is inside them, and
// the operator's own open/closed choice is remembered per browser.

import React, { useCallback, useState } from 'react';
import { Link } from 'react-router-dom';
import { LuChevronDown, LuChevronRight } from 'react-icons/lu';

const STORAGE_KEY = 'hf-nav-open';

const readOpen = () => {
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}') || {};
  } catch (_) {
    return {};
  }
};

/** [isOpen(section, activeId), toggle(section)] */
export const useNavSections = () => {
  const [open, setOpen] = useState(readOpen);
  const toggle = useCallback((h) => {
    setOpen((prev) => {
      const next = { ...prev, [h]: !prev[h] };
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(next));
      } catch (_) {
        // private mode — the choice just is not remembered
      }
      return next;
    });
  }, []);
  const isOpen = (s, activeId) =>
    !s.collapsible || !!open[s.h] || s.items.some((it) => it.id === activeId);
  return [isOpen, toggle];
};

export const HfNavItem = ({ it, active, t, onNavigate }) => {
  const className = 'nav-i' + (active ? ' active' : '');
  const Glyph = it.glyph;
  const inner = (
    <>
      <span className='nav-glyph' aria-hidden='true'>
        <Glyph size={14} />
      </span>
      <span className='nav-label'>
        {t(it.key, it.label)}
        {it.legacyBridge && (
          <span
            className='nav-legacy-tag'
            data-testid={`nav-legacy-tag-${it.id}`}
            title={t(
              'console.nav.legacy_hint',
              'opens the legacy console page, not this v2 shell',
            )}
          >
            {' '}
            ↗
          </span>
        )}
      </span>
      {it.badge && <span className='nav-badge'>{it.badge}</span>}
    </>
  );
  // Deferred surfaces render as a non-interactive, greyed entry carrying an
  // honest reason — never a dead link.
  if (it.disabled) {
    return (
      <div
        className={className}
        data-testid={`nav-disabled-${it.id}`}
        aria-disabled='true'
        title={t(it.titleKey, it.title || 'not available in v2 yet')}
        style={{ opacity: 0.4, cursor: 'not-allowed' }}
      >
        {inner}
      </div>
    );
  }
  return it.href ? (
    <Link to={it.href} className={className} onClick={onNavigate}>
      {inner}
    </Link>
  ) : (
    <div className={className}>{inner}</div>
  );
};

export const HfNavSection = ({ s, open, onToggle, children, t }) => (
  <div className='nav-section'>
    {s.collapsible ? (
      <button
        type='button'
        className='nav-h nav-h-toggle'
        onClick={() => onToggle(s.h)}
        aria-expanded={open}
        data-testid={`nav-section-toggle-${s.h}`}
      >
        {open ? (
          <LuChevronDown size={11} aria-hidden='true' />
        ) : (
          <LuChevronRight size={11} aria-hidden='true' />
        )}
        {t(s.hKey, s.h)}
      </button>
    ) : (
      <div className='nav-h'>{t(s.hKey, s.h)}</div>
    )}
    {/* Collapsed items stay mounted but hidden (and out of the a11y tree):
        which links a role may see is decided by visibleNavItems, not by
        whether a heading happens to be folded. */}
    <div hidden={!open} data-testid={`nav-section-items-${s.h}`}>
      {children}
    </div>
  </div>
);
