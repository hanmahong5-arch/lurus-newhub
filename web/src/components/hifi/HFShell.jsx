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
import React, { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  LuActivity,
  LuBookOpen,
  LuBoxes,
  LuFlaskConical,
  LuImage,
  LuKeyRound,
  LuLayoutDashboard,
  LuLifeBuoy,
  LuLink2,
  LuMenu,
  LuMessageSquare,
  LuRadioTower,
  LuScrollText,
  LuSearch,
  LuSettings,
  LuSlidersHorizontal,
  LuTag,
  LuTicket,
  LuTimer,
  LuTrendingDown,
  LuUserCog,
  LuUsers,
  LuWallet,
  LuZap,
} from 'react-icons/lu';
import TenantSwitcher from './TenantSwitcher';
import { API } from '../../helpers';
import { clearAllDrafts } from '../../hooks/common/useFormDraft';
import { HfToastHost } from './HfToast';

// Single source of truth: pathname suffix → nav item id.
// HFShell uses this to auto-highlight the active item when caller doesn't
// pass an `active` prop, so each page doesn't have to hardcode it.
const PATH_TO_ID = {
  dashboard: 'dashboard',
  playground: 'playground',
  chat: 'chat',
  token: 'tokens',
  log: 'logs',
  billing: 'billing',
  channel: 'channels',
  models: 'models',
  tenants: 'users',
  pricing: 'pricing',
  redemption: 'redemption',
  settings: 'settings',
  flows: 'channels',
  states: 'logs',
  variants: 'dashboard',
  cmdk: 'tokens',
  'design-system': 'settings',
};

// Help escape hatches. The v2 console had none — the only docs links lived in
// the legacy Footer (components/layout/Footer.jsx) which the hi-fi shell
// replaces, and the only support address was buried in the AccountDisabled
// page. Both targets are the ones already in use elsewhere in this repo.
const DOCS_URL = 'https://docs.lurus.cn';
const SUPPORT_MAILTO = 'mailto:support@lurus.cn';

const useV2ActiveId = () => {
  const { pathname } = useLocation();
  const m = pathname.match(/\/console\/v2\/([^/?#]+)/);
  return m ? PATH_TO_ID[m[1]] || '' : '';
};

const useThemeToggle = () => {
  const [theme, setTheme] = useState(() => {
    if (typeof document === 'undefined') return 'light';
    return document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light';
  });
  useEffect(() => {
    if (typeof document === 'undefined') return;
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem('lurus-hf-theme', theme);
    } catch (e) {
      // ignore — private browsing / disabled storage
    }
  }, [theme]);
  // On first mount, hydrate from storage if no theme yet set.
  useEffect(() => {
    try {
      const saved = localStorage.getItem('lurus-hf-theme');
      if (saved === 'light' || saved === 'dark') setTheme(saved);
    } catch (e) {
      // ignore
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return [theme, () => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))];
};

/*
 * HFShell — sidebar + topbar wrapper for hi-fi v2 screens.
 * Ported from design canvas hifi/_shell.jsx (2026-05-07).
 *
 * The hi-fi screens render their own chrome; PageLayout bypasses HeaderBar/SiderBar
 * for /console/v2/* paths (see PageLayout.jsx).
 */

// Nav labels carry an i18n `key` plus the original English `label` as the
// t() defaultValue — so a missing translation degrades to readable English,
// never a bare `console.nav.x` key.
const NAV_SECTIONS = [
  {
    h: 'workspace',
    hKey: 'console.nav.section_workspace',
    items: [
      {
        id: 'dashboard',
        href: '/console/v2/dashboard',
        glyph: LuLayoutDashboard,
        label: 'Dashboard',
        key: 'console.nav.dashboard',
        badge: '',
      },
      {
        id: 'playground',
        href: '/console/v2/playground',
        glyph: LuFlaskConical,
        label: 'Playground',
        key: 'console.nav.playground',
        badge: '',
      },
      {
        id: 'chat',
        href: '/console/v2/chat',
        glyph: LuMessageSquare,
        label: 'Chat',
        key: 'console.nav.chat',
        badge: '',
      },
    ],
  },
  {
    h: 'my account',
    hKey: 'console.nav.section_my_account',
    items: [
      {
        id: 'tokens',
        href: '/console/v2/token',
        glyph: LuKeyRound,
        label: 'Tokens',
        key: 'console.nav.tokens',
        badge: '5',
      },
      {
        id: 'logs',
        href: '/console/v2/log',
        glyph: LuScrollText,
        label: 'Usage & logs',
        key: 'console.nav.logs',
        badge: '',
      },
      // Honest placeholder: Midjourney / async-task logs are not ported to v2
      // yet. Shown greyed so the surface is visibly deferred, not silently gone.
      {
        id: 'mj-logs',
        href: null,
        glyph: LuImage,
        label: 'MJ / Task logs',
        key: 'console.nav.mj_logs',
        badge: '',
        disabled: true,
        titleKey: 'console.nav.mj_logs_unavailable',
        title: 'Midjourney / async task logs not available in v2 yet',
      },
      {
        id: 'billing',
        href: '/console/v2/billing',
        glyph: LuWallet,
        label: 'Billing',
        key: 'console.nav.billing',
        badge: '$241',
      },
    ],
  },
  {
    h: 'platform · admin',
    hKey: 'console.nav.section_platform_admin',
    items: [
      {
        id: 'channels',
        href: '/console/v2/channel',
        glyph: LuRadioTower,
        label: 'Channels',
        key: 'console.nav.channels',
        badge: '8',
      },
      {
        id: 'models',
        href: '/console/v2/models',
        glyph: LuBoxes,
        label: 'Models',
        key: 'console.nav.models',
        badge: '54',
      },
      {
        id: 'users',
        href: '/console/v2/tenants',
        glyph: LuUsers,
        label: 'Tenants',
        key: 'console.nav.tenants',
        badge: '',
      },
      {
        id: 'pricing',
        href: '/console/v2/pricing',
        glyph: LuTag,
        label: 'Pricing',
        key: 'console.nav.pricing',
        badge: '',
      },
      {
        id: 'redemption',
        href: '/console/v2/redemption',
        glyph: LuTicket,
        label: 'Redemption',
        key: 'console.nav.redemption',
        badge: '',
      },
      {
        id: 'settings',
        href: '/console/v2/settings',
        glyph: LuSettings,
        label: 'Settings',
        key: 'console.nav.settings',
        badge: '',
      },
      // Cost-aware-routing savings analyzer (Phase 1).
      {
        id: 'admin-cost',
        href: '/console/v2/admin/cost-intelligence',
        glyph: LuTrendingDown,
        label: 'Cost intelligence',
        key: 'console.nav.cost_intelligence',
        badge: '',
      },
      // Per-model performance analytics (requests/errors/latency).
      {
        id: 'admin-analytics',
        href: '/console/v2/admin/model-performance',
        glyph: LuActivity,
        label: 'Model performance',
        key: 'console.nav.model_performance',
        badge: '',
      },
      // Per-(tenant, model) RPM/TPM rate-limit config (migration 026).
      {
        id: 'admin-model-limits',
        href: '/console/v2/admin/model-limits',
        glyph: LuTimer,
        label: 'Model limits',
        key: 'console.nav.model_limits',
        badge: '',
      },
      // Admin surfaces (deferred backlog round 2) — now wired.
      {
        id: 'admin-users',
        href: '/console/v2/admin/users',
        glyph: LuUserCog,
        label: 'Users (admin)',
        key: 'console.nav.admin_users',
        badge: '',
      },
      // Live circuit-breaker state per channel (this replica).
      {
        id: 'admin-gateway',
        href: '/console/v2/admin/gateway',
        glyph: LuZap,
        label: 'Gateway health',
        key: 'console.nav.admin_gateway',
        badge: '',
      },
      // Audit trail + tamper-evidence chain verifier (migration 024 backend).
      {
        id: 'admin-audit',
        href: '/console/v2/admin/audit',
        glyph: LuLink2,
        label: 'Audit trail',
        key: 'console.nav.admin_audit',
        badge: '',
      },
      {
        id: 'admin-settings',
        href: '/console/v2/admin/settings',
        glyph: LuSlidersHorizontal,
        label: 'Admin settings',
        key: 'console.nav.admin_settings',
        badge: '',
      },
    ],
  },
];

// Pull the bridged user identity directly from localStorage — set by
// OidcRedirect/zita-bootstrap. The v2 hi-fi shell intentionally
// avoids the StatusContext/UserContext used by the legacy chrome (those
// pull a chunk of v1 state we don't need here); a thin localStorage
// read is enough to surface "who am I" + a logout escape hatch.
const useBridgedUser = () => {
  const [user, setUser] = useState(null);
  useEffect(() => {
    try {
      const raw = localStorage.getItem('user');
      if (raw) setUser(JSON.parse(raw));
    } catch (e) {
      // malformed payload — leave user null, the logout button still works
    }
  }, []);
  return user;
};

const readTenantSlug = () => {
  try {
    return localStorage.getItem('tenant_slug') || 'default';
  } catch (_) {
    return 'default';
  }
};

const inferModeFromRole = (role) => {
  // Map v1 role ints to TenantSwitcher mode buckets.
  // role 100 = root, 10 = admin → manage tenant pool; else Personal/EndUser.
  if (role === 100 || role === 10) return 'Reseller';
  return 'Personal';
};

// Real tenants + active tenant for TenantSwitcher.
// Admin (root) attempts /api/v2/admin/tenants; everyone falls back to a
// single-item list derived from the bridged user. Empty/error → still
// renders a non-DEMO list so users never see "acme · prod" placeholder.
const useRealTenants = (user) => {
  const [tenants, setTenants] = useState(null);

  useEffect(() => {
    if (!user) return;
    let cancelled = false;

    const slug = readTenantSlug();
    const fallbackName =
      user.tenant_name || user.display_name || user.username || slug;
    const fallback = [
      {
        id: slug,
        name: fallbackName,
        mode: inferModeFromRole(user.role),
      },
    ];

    // Only root (100) can list all tenants. Skip the call otherwise.
    if (user.role !== 100) {
      if (!cancelled) setTenants(fallback);
      return () => {
        cancelled = true;
      };
    }

    API.get('/api/v2/admin/tenants?page_size=50', { skipErrorHandler: true })
      .then((res) => {
        if (cancelled) return;
        const rows = res?.data?.data?.tenants;
        if (!Array.isArray(rows) || rows.length === 0) {
          setTenants(fallback);
          return;
        }
        setTenants(
          rows.map((t) => ({
            id: t.slug || t.id,
            name: t.name || t.slug || String(t.id),
            mode: 'Reseller',
          })),
        );
      })
      .catch(() => {
        if (!cancelled) setTenants(fallback);
      });

    return () => {
      cancelled = true;
    };
  }, [user]);

  return tenants;
};

const switchTenantSlug = (slug) => {
  try {
    localStorage.setItem('tenant_slug', slug);
  } catch (_) {
    // ignore — private mode
  }
  // Trigger a re-fetch of tenant-scoped data by reloading.
  window.location.reload();
};

const handleLogout = async () => {
  try {
    await API.post('/api/v2/auth/zita-logout');
  } catch (e) {
    // session may already be expired; the cookie clear still happened
  }
  try {
    localStorage.removeItem('user');
    localStorage.removeItem('tenant_slug');
    // Tier 1.4: wipe in-progress form drafts so a different account on
    // this browser starts clean.
    clearAllDrafts();
  } catch (e) {
    // ignore — private mode / disabled storage
  }
  window.location.href = '/login';
};

const HFShell = ({ active, crumbs = [], actions, children }) => {
  const autoActive = useV2ActiveId();
  const activeId = active != null ? active : autoActive;
  const [theme, toggleTheme] = useThemeToggle();
  const { t, i18n } = useTranslation();
  const isZh = (i18n.resolvedLanguage || i18n.language || 'zh').startsWith(
    'zh',
  );
  // Reuse i18next + the LanguageDetector localStorage cache (i18nextLng) — the
  // same mechanism the v1 LanguageSelector drives, so the choice persists and
  // is shared across the whole app.
  const toggleLang = () => i18n.changeLanguage(isZh ? 'en' : 'zh');
  const user = useBridgedUser();
  const tenants = useRealTenants(user);
  const currentSlug = readTenantSlug();
  const currentTenant =
    (tenants && tenants.find((t) => t.id === currentSlug)) ||
    (tenants && tenants[0]) ||
    null;
  const [navOpen, setNavOpen] = useState(false);
  const closeNav = () => setNavOpen(false);
  return (
    <div className={'hf hf-shell' + (navOpen ? ' nav-open' : '')}>
      <HfToastHost />
      <div className='hf-nav-backdrop' onClick={closeNav} aria-hidden='true' />
      <aside className='hf-side'>
        <div className='brand'>
          <div className='brand-mark' />
          <div className='brand-text'>
            <div className='brand-name'>Lurus Hub</div>
            <div className='brand-tag'>
              {t('console.shell.tagline', 'data processing layer')}
            </div>
          </div>
        </div>

        <button
          type='button'
          className='btn'
          aria-label={t('console.shell.search', 'search anything')}
          style={{
            width: '100%',
            justifyContent: 'space-between',
            marginBottom: 14,
          }}
        >
          <span className='search-label' style={{ color: 'var(--hf-ink-3)' }}>
            <LuSearch size={12} aria-hidden='true' />{' '}
            {t('console.shell.search', 'search anything')}
          </span>
          <span className='kbd-group'>
            <span className='kbd'>⌘</span>
            <span className='kbd'>K</span>
          </span>
        </button>

        {NAV_SECTIONS.map((s) => (
          <div className='nav-section' key={s.h}>
            <div className='nav-h'>{t(s.hKey, s.h)}</div>
            {s.items.map((it) => {
              const className = 'nav-i' + (activeId === it.id ? ' active' : '');
              const Glyph = it.glyph;
              const inner = (
                <>
                  <span className='nav-glyph' aria-hidden='true'>
                    <Glyph size={14} />
                  </span>
                  <span className='nav-label'>{t(it.key, it.label)}</span>
                  {it.badge && <span className='nav-badge'>{it.badge}</span>}
                </>
              );
              // Deferred surfaces render as a non-interactive, greyed entry
              // carrying an honest reason — never a dead link.
              if (it.disabled) {
                return (
                  <div
                    key={it.id}
                    className={className}
                    data-testid={`nav-disabled-${it.id}`}
                    aria-disabled='true'
                    title={t(
                      it.titleKey,
                      it.title || 'not available in v2 yet',
                    )}
                    style={{ opacity: 0.4, cursor: 'not-allowed' }}
                  >
                    {inner}
                  </div>
                );
              }
              return it.href ? (
                <Link
                  key={it.id}
                  to={it.href}
                  className={className}
                  onClick={closeNav}
                >
                  {inner}
                </Link>
              ) : (
                <div key={it.id} className={className}>
                  {inner}
                </div>
              );
            })}
          </div>
        ))}

        <div className='footer'>
          <div className='footer-links'>
            <a
              href={DOCS_URL}
              target='_blank'
              rel='noreferrer noopener'
              data-testid='shell-docs-link'
            >
              <LuBookOpen size={12} aria-hidden='true' />
              <span>{t('console.shell.docs', 'Docs')}</span>
            </a>
            <a href={SUPPORT_MAILTO} data-testid='shell-support-link'>
              <LuLifeBuoy size={12} aria-hidden='true' />
              <span>{t('console.shell.support', 'Support')}</span>
            </a>
          </div>
          <TenantSwitcher
            tenants={tenants ?? []}
            tenantName={currentTenant?.name ?? ''}
            mode={currentTenant?.mode ?? 'Personal'}
            onSelect={(tenantId) => {
              if (tenantId && tenantId !== currentSlug) {
                switchTenantSlug(tenantId);
              }
            }}
          />
        </div>
      </aside>

      <div className='hf-main'>
        <div className='hf-top'>
          <button
            type='button'
            className='btn ghost hf-nav-toggle'
            onClick={() => setNavOpen((o) => !o)}
            aria-label={t('console.shell.toggle_nav', 'toggle navigation')}
            aria-expanded={navOpen}
            style={{ fontSize: 14, padding: '0 8px', marginRight: 4 }}
          >
            <LuMenu size={16} aria-hidden='true' />
          </button>
          <div className='crumb'>
            {crumbs.map((c, i) => (
              <React.Fragment key={i}>
                {i > 0 && <span className='faint'>/</span>}
                {i === crumbs.length - 1 ? <b>{c}</b> : <span>{c}</span>}
              </React.Fragment>
            ))}
          </div>
          <div style={{ flex: 1 }} />
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {actions}
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
            {user && (
              <span
                style={{
                  fontSize: 12,
                  color: 'var(--hf-ink-2)',
                  padding: '0 6px',
                  maxWidth: 160,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
                title={user.email || user.username}
              >
                {user.display_name || user.username}
              </span>
            )}
            <button
              type='button'
              className='btn ghost'
              onClick={
                user
                  ? handleLogout
                  : () => {
                      window.location.href = '/login';
                    }
              }
              title={
                user
                  ? t('console.shell.logout', 'log out')
                  : t('console.shell.login', 'log in')
              }
              aria-label={
                user
                  ? t('console.shell.logout', 'log out')
                  : t('console.shell.login', 'log in')
              }
              style={{ fontSize: 12, padding: '0 10px' }}
            >
              {user
                ? t('console.shell.logout', 'logout')
                : t('console.shell.login', 'login')}
            </button>
          </div>
        </div>
        <div className='hf-body'>{children}</div>
      </div>
    </div>
  );
};

export default HFShell;
