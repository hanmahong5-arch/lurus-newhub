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
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import useThemeToggle from './useThemeToggle';

/**
 * HfUserMenu — the topbar's right-hand identity cluster, extracted out of
 * HFShell. Language and theme stay as ghost icon buttons; what used to be a
 * bare username label + a separate logout button is now a single
 * "avatar + name" trigger that opens a role=menu with Settings / Log out.
 *
 * When there is no bridged user, this renders the original plain login
 * button — nothing else in the cluster (language/theme toggles included)
 * makes sense before a session exists.
 */

const initials = (user) => {
  const src = (user?.display_name || user?.username || '').trim();
  if (!src) return '?';
  const parts = src.split(/\s+/).filter(Boolean);
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
};

const isMacPlatform = () => {
  if (typeof navigator === 'undefined') return false;
  const p = navigator.platform || navigator.userAgent || '';
  return /Mac|iPhone|iPad|iPod/i.test(p);
};

const HfUserMenu = ({ user, onLogout }) => {
  const { t, i18n } = useTranslation();
  const [theme, toggleTheme] = useThemeToggle();
  const isZh = (i18n.resolvedLanguage || i18n.language || 'zh').startsWith(
    'zh',
  );
  // Reuse i18next + the LanguageDetector localStorage cache (i18nextLng) — the
  // same mechanism the v1 LanguageSelector drives, so the choice persists and
  // is shared across the whole app.
  const toggleLang = () => i18n.changeLanguage(isZh ? 'en' : 'zh');

  const [open, setOpen] = useState(false);
  const rootRef = useRef(null);
  const triggerRef = useRef(null);
  const itemRefs = useRef([]);

  useEffect(() => {
    if (!open) return undefined;
    const onDocClick = (e) => {
      if (rootRef.current && !rootRef.current.contains(e.target)) {
        setOpen(false);
      }
    };
    document.addEventListener('mousedown', onDocClick);
    return () => document.removeEventListener('mousedown', onDocClick);
  }, [open]);

  useEffect(() => {
    if (open) itemRefs.current[0]?.focus();
  }, [open]);

  const closeAndFocusTrigger = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  const onTriggerKeyDown = (e) => {
    if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      setOpen(true);
    }
  };

  const onMenuKeyDown = (e) => {
    const items = itemRefs.current.filter(Boolean);
    if (items.length === 0) return;
    const idx = items.indexOf(document.activeElement);
    if (e.key === 'Escape') {
      e.preventDefault();
      closeAndFocusTrigger();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      items[(idx + 1) % items.length]?.focus();
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      items[(idx - 1 + items.length) % items.length]?.focus();
    }
  };

  if (!user) {
    return (
      <button
        type='button'
        className='btn ghost'
        onClick={() => {
          window.location.href = '/login';
        }}
        title={t('console.shell.login', 'log in')}
        aria-label={t('console.shell.login', 'log in')}
        style={{ fontSize: 12, padding: '0 10px' }}
      >
        {t('console.shell.login', 'login')}
      </button>
    );
  }

  return (
    <>
      <button
        type='button'
        className='btn ghost'
        onClick={toggleLang}
        title={t('console.shell.toggle_language', 'switch language')}
        aria-label={t('console.shell.toggle_language', 'switch language')}
        style={{
          fontSize: 12,
          padding: '0 8px',
          fontFamily: 'var(--hf-mono)',
        }}
      >
        {isZh ? 'EN' : '中'}
      </button>
      <button
        type='button'
        className='btn ghost'
        onClick={toggleTheme}
        title={
          theme === 'dark'
            ? t('console.shell.theme_to_light', 'switch to light')
            : t('console.shell.theme_to_dark', 'switch to dark')
        }
        aria-label={t('console.shell.toggle_theme', 'toggle theme')}
        style={{ fontSize: 14, padding: '0 8px' }}
      >
        {theme === 'dark' ? '☀' : '◐'}
      </button>
      <div ref={rootRef} style={{ position: 'relative' }}>
        <button
          type='button'
          ref={triggerRef}
          className='btn ghost'
          onClick={() => setOpen((v) => !v)}
          onKeyDown={onTriggerKeyDown}
          aria-haspopup='menu'
          aria-expanded={open}
          data-testid='shell-user-menu-trigger'
          title={user.email || user.username}
          aria-label={t('console.shell.account_menu', 'account')}
          style={{ fontSize: 12, padding: '2px 8px 2px 2px', gap: 6 }}
        >
          <span className='hf-user-avatar' aria-hidden='true'>
            {initials(user)}
          </span>
          <span className='hf-user-name'>
            {user.display_name || user.username}
          </span>
        </button>
        {open && (
          <div
            role='menu'
            className='hf-menu'
            data-testid='shell-user-menu'
            onKeyDown={onMenuKeyDown}
          >
            <Link
              to='/console/v2/settings'
              role='menuitem'
              ref={(el) => {
                itemRefs.current[0] = el;
              }}
              className='hf-menu-item'
              onClick={closeAndFocusTrigger}
            >
              {t('console.nav.settings', 'Settings')}
            </Link>
            <button
              type='button'
              role='menuitem'
              ref={(el) => {
                itemRefs.current[1] = el;
              }}
              className='hf-menu-item'
              onClick={() => {
                setOpen(false);
                onLogout();
              }}
            >
              {t('console.shell.logout', 'logout')}
            </button>
          </div>
        )}
      </div>
    </>
  );
};

// Exported so HFShell's ⌘/Ctrl keycap label uses the same Mac detection
// instead of a second, possibly-drifting copy.
export { isMacPlatform };
export default HfUserMenu;
