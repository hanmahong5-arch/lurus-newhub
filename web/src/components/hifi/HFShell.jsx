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
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
  LuActivity,
  LuBell,
  LuBookOpen,
  LuBoxes,
  LuFlaskConical,
  LuFolderKanban,
  LuHeartPulse,
  LuImage,
  LuKeyRound,
  LuLayoutDashboard,
  LuLifeBuoy,
  LuLink2,
  LuMenu,
  LuMessageSquare,
  LuRadioTower,
  LuRefreshCcw,
  LuScrollText,
  LuSearch,
  LuSettings,
  LuShieldCheck,
  LuSlidersHorizontal,
  LuTag,
  LuTicket,
  LuTimer,
  LuTrendingDown,
  LuTrophy,
  LuUserCog,
  LuUsers,
  LuWallet,
  LuWorkflow,
  LuZap,
} from 'react-icons/lu';
import TenantSwitcher from './TenantSwitcher';
import { API } from '../../helpers';
import { clearAllDrafts } from '../../hooks/common/useFormDraft';
import { readTenantSlug } from '../../hooks/common/useTenantSlug';
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
  tasks: 'mj-logs',
  billing: 'billing',
  channel: 'channels',
  models: 'models',
  tenants: 'users',
  pricing: 'pricing',
  redemption: 'redemption',
  projects: 'projects',
  settings: 'settings',
  // Flows/index.jsx passes active='flows' explicitly, which wins over this
  // fallback whenever that page renders (see activeId below). This entry is
  // the mapping for any other route that resolves to the Flows surface.
  flows: 'flows',
  states: 'logs',
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
//
// Sections group by JOB, not by backing table: workspace (do work) →
// my account (my keys/usage/money) → three admin groups split by concern
// (routing & models / governance / operations & insights). `minRole` hides a
// section from the rail for lower roles — UI hygiene only; every /api/v2/admin
// route keeps its own server-side authz, which remains the real gate.
// Exported so the command palette can offer the same destinations with the same
// minRole gating, instead of maintaining a second hardcoded copy that silently
// drifts (the palette previously listed five routes chosen by hand, ungated).
export const NAV_SECTIONS = [
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
        badge: '',
      },
      {
        id: 'logs',
        href: '/console/v2/log',
        glyph: LuScrollText,
        label: 'Usage & logs',
        key: 'console.nav.logs',
        badge: '',
      },
      // Self-scoped Midjourney / async-task job logs (pages/v2/Tasks) — real
      // as of cycle 10 L6/L7. Was a disabled:true placeholder with href:null
      // ("not available in v2 yet") until this page existed. Visibility is
      // gated by the same enable_drawing/enable_task localStorage flags the
      // legacy rail uses (components/layout/SiderBar.jsx's workspaceItems,
      // set from GET /api/status by helpers/data.js) — shown if either is
      // 'true', hidden if both are off, so a deployment without MJ/task
      // enabled doesn't get a rail link to a page that can only ever be
      // empty. Filtered in the render loop below rather than here, so
      // CommandPalette's own visibleNavItems() call (a different file) still
      // lists every destination regardless of these flags.
      {
        id: 'mj-logs',
        href: '/console/v2/tasks',
        glyph: LuImage,
        label: 'MJ / Task logs',
        key: 'console.nav.mj_logs',
        badge: '',
      },
      {
        id: 'billing',
        href: '/console/v2/billing',
        glyph: LuWallet,
        label: 'Billing',
        key: 'console.nav.billing',
        badge: '',
      },
      // Account settings (sessions revoke etc.) is user-scope, not admin —
      // it lived in the admin section only as a hi-fi port leftover.
      {
        id: 'settings',
        href: '/console/v2/settings',
        glyph: LuSettings,
        label: 'Settings',
        key: 'console.nav.settings',
        badge: '',
      },
      // Legacy /console/personal (components/settings/PersonalSetting.jsx)
      // carries one capability the v2 Settings page does not: the system
      // access token (components/settings/personal/cards/AccountManagement.jsx
      // — grep for 系统访问令牌 finds it there and nowhere under pages/v2).
      // Quota-warning notification channels are NOT a reason any more:
      // pages/v2/Settings/index.jsx has had its own 'notifications' tab
      // (email / webhook / bark / gotify, seeded from profile.setting and
      // written back with PUT /api/user/setting) since cycle 10, so the
      // sentence that used to stand here was stale by a cycle.
      // /console/personal renders the legacy HeaderBar/SiderBar chrome, not
      // this shell (PageLayout.jsx's v2 bypass only matches /console/v2/*),
      // so clicking this item leaves the rail entirely and nothing
      // highlights on the way back. legacyBridge:true marks that in the UI
      // — see the nav-legacy-tag rendering below.
      {
        id: 'personal',
        href: '/console/personal',
        glyph: LuBell,
        label: 'Notifications & access token',
        key: 'console.nav.personal',
        badge: '',
        legacyBridge: true,
      },
    ],
  },
  {
    h: 'routing & models',
    hKey: 'console.nav.section_routing_models',
    minRole: 10,
    items: [
      // Multi-step setup wizards (new channel / new token) — pages/v2/Flows,
      // wired to the real channel and token backends.
      {
        id: 'flows',
        href: '/console/v2/flows',
        glyph: LuWorkflow,
        label: 'Flows',
        key: 'console.nav.flows',
        badge: '',
      },
      {
        id: 'channels',
        href: '/console/v2/channel',
        glyph: LuRadioTower,
        label: 'Channels',
        key: 'console.nav.channels',
        badge: '',
      },
      {
        id: 'models',
        href: '/console/v2/models',
        glyph: LuBoxes,
        label: 'Models',
        key: 'console.nav.models',
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
      // Job manager that refreshes a channel's free-model catalog from
      // OpenRouter on a schedule. Already had a legacy-rail entry
      // (components/layout/SiderBar.jsx's adminItems, gated by isAdmin()) —
      // this is its first entry in the v2 rail, not its first nav entry
      // anywhere. Server-side, GET is AdminAuth but every mutating endpoint
      // (POST/PUT/DELETE /jobs*, /run-all — api-router.go's
      // openrouter-sync group) is RootAuth, and pages/OpenRouterSync/index.jsx
      // has no client-side role check of its own: it renders the
      // create/edit/delete/run buttons unconditionally. minRole:100 below
      // (matching admin-authz/admin-system-tasks/admin-diagnostics' own
      // per-item overrides in this file) keeps a role-10 admin from landing
      // on a page where every mutating button 403s, until that page grows
      // its own read-only view for role 10. Also renders the legacy
      // HeaderBar/SiderBar chrome, same as /console/personal above —
      // legacyBridge:true marks that.
      {
        id: 'openrouter-sync',
        href: '/console/openrouter-sync',
        glyph: LuRefreshCcw,
        label: 'OpenRouter sync',
        key: 'console.nav.openrouter_sync',
        badge: '',
        minRole: 100,
        legacyBridge: true,
      },
    ],
  },
  {
    h: 'governance',
    hKey: 'console.nav.section_governance',
    minRole: 10,
    items: [
      // Every call this page makes is /api/v2/admin/tenants{,/:id,/:id/stats},
      // which api-v2-router.go puts behind RootJWTAuth. minRole:100 here so a
      // role-10 admin is not shown a destination that can only answer 403.
      {
        id: 'users',
        href: '/console/v2/tenants',
        glyph: LuUsers,
        label: 'Tenants',
        key: 'console.nav.tenants',
        badge: '',
        minRole: 100,
      },
      // /api/v2/admin/users (list, PUT, DELETE) — RootJWTAuth.
      {
        id: 'admin-users',
        href: '/console/v2/admin/users',
        glyph: LuUserCog,
        label: 'Users (admin)',
        key: 'console.nav.admin_users',
        badge: '',
        minRole: 100,
      },
      // Cost-attribution projects (migration 029). Lives in an admin section
      // because creating/renaming/deleting one is tenant-admin gated — reading
      // the list is open to every member (the Token page's picker needs it).
      {
        id: 'projects',
        href: '/console/v2/projects',
        glyph: LuFolderKanban,
        label: 'Projects',
        key: 'console.nav.projects',
        badge: '',
      },
      // Per-(tenant, model) RPM/TPM rate-limit config (migration 026).
      // Reads and writes /api/v2/admin/tenants/:id/model-limits and
      // /model-allowlist, plus the admin tenant list to populate its picker —
      // RootJWTAuth all the way down.
      {
        id: 'admin-model-limits',
        href: '/console/v2/admin/model-limits',
        glyph: LuTimer,
        label: 'Model limits',
        key: 'console.nav.model_limits',
        badge: '',
        minRole: 100,
      },
      {
        id: 'redemption',
        href: '/console/v2/redemption',
        glyph: LuTicket,
        label: 'Redemption',
        key: 'console.nav.redemption',
        badge: '',
      },
      // Audit trail + tamper-evidence chain verifier (migration 024 backend).
      // Now reachable by a delegated admin holding an audit:read grant, not
      // only root (L4, 2026-09-13) — RootOrGranted, not RootJWTAuth, gates
      // the four GETs this page calls.
      {
        id: 'admin-audit',
        href: '/console/v2/admin/audit',
        glyph: LuLink2,
        label: 'Audit trail',
        key: 'console.nav.admin_audit',
        badge: '',
      },
      // Delegated admin permission grants (L4, auth-security-17/18,
      // console-ux-36). Grant MANAGEMENT itself stays root-only
      // server-side (adminRoute/RootJWTAuth) — minRole:100 here mirrors
      // admin-system-tasks' own per-item override for the same reason: the
      // section itself is only minRole:10.
      {
        id: 'admin-authz',
        href: '/console/v2/admin/authz',
        glyph: LuShieldCheck,
        label: 'Permission grants',
        key: 'console.nav.admin_authz',
        badge: '',
        minRole: 100,
      },
    ],
  },
  {
    h: 'operations & insights',
    hKey: 'console.nav.section_operations',
    minRole: 10,
    items: [
      // Live circuit-breaker state per channel (this replica).
      // /api/v2/admin/gateway/health — RootJWTAuth.
      {
        id: 'admin-gateway',
        href: '/console/v2/admin/gateway',
        glyph: LuZap,
        label: 'Gateway health',
        key: 'console.nav.admin_gateway',
        badge: '',
        minRole: 100,
      },
      // Cost-aware-routing savings analyzer (Phase 1).
      // /api/v2/admin/governance/savings — RootJWTAuth.
      {
        id: 'admin-cost',
        href: '/console/v2/admin/cost-intelligence',
        glyph: LuTrendingDown,
        label: 'Cost intelligence',
        key: 'console.nav.cost_intelligence',
        badge: '',
        minRole: 100,
      },
      // Per-model performance analytics (requests/errors/latency).
      // /api/v2/admin/analytics/model-performance plus the admin tenant list —
      // RootJWTAuth. Unlike Rankings below, this page has no tenant-scoped
      // route to fall back to.
      {
        id: 'admin-analytics',
        href: '/console/v2/admin/model-performance',
        glyph: LuActivity,
        label: 'Model performance',
        key: 'console.nav.model_performance',
        badge: '',
        minRole: 100,
      },
      // Period-over-period model/vendor leaderboard (rank/trend/share) —
      // L4, 2026-09-12. Beside admin-analytics per the plan's §8 correction.
      {
        id: 'admin-rankings',
        href: '/console/v2/admin/rankings',
        glyph: LuTrophy,
        label: 'Rankings',
        key: 'console.nav.rankings',
        badge: '',
      },
      // /api/v2/admin/options (GET + PUT) and /api/v2/admin/stats —
      // RootJWTAuth.
      {
        id: 'admin-settings',
        href: '/console/v2/admin/settings',
        glyph: LuSlidersHorizontal,
        label: 'Admin settings',
        key: 'console.nav.admin_settings',
        badge: '',
        minRole: 100,
      },
      // Background-task heartbeats (L3, 2026-09-13): GET
      // /api/v2/admin/system/tasks is RootJWTAuth-gated server-side, so this
      // entry needs a per-item minRole=100 — the section itself is only
      // minRole:10, which would otherwise show a root-only page link to
      // any admin.
      {
        id: 'admin-system-tasks',
        href: '/console/v2/admin/system-tasks',
        glyph: LuHeartPulse,
        label: 'Background tasks',
        key: 'console.nav.system_tasks',
        badge: '',
        minRole: 100,
      },
      // Session-affinity stats/purge + TOTP adoption (L8, cycle 9): both
      // backends sit behind RootJWTAuth server-side (adminRoute), so this
      // entry needs the same per-item minRole:100 override as
      // admin-system-tasks and admin-authz above.
      {
        id: 'admin-diagnostics',
        href: '/console/v2/admin/diagnostics',
        glyph: LuHeartPulse,
        label: 'Diagnostics',
        key: 'console.nav.diagnostics',
        badge: '',
        minRole: 100,
      },
    ],
  },
];

// visibleNavItems applies BOTH the section-level and the per-item minRole
// gate for a given bridged user, in one place — the single source of truth
// for "what nav destinations can this user see", so the rail (below) and
// the command palette (CommandPalette/index.jsx) cannot drift apart. Before
// this existed, the palette applied only the section-level filter with a
// synthetic two-value role (`admin ? 10 : 0`), which cannot distinguish
// admin (10) from root (100) — a role-10 admin was offered the root-only
// "Background tasks" destination (admin-system-tasks, minRole:100) even
// though the rail correctly hides it.
// navItemEnabledByFeatureFlags answers whether a nav id points at a surface
// the deployment has switched on at all, independent of role. Kept separate
// from visibleNavItems (which is purely role-based, and which several tests
// call with no localStorage at all) and exported so the rail and the command
// palette gate on the SAME predicate — a destination the rail hides because
// it would be a permanently empty page must not still be offered by ⌘K.
// Same flags and the same either-is-true semantics as
// components/layout/SiderBar.jsx's workspaceItems.
export const navItemEnabledByFeatureFlags = (id) => {
  if (id !== 'mj-logs') return true;
  try {
    return (
      localStorage.getItem('enable_drawing') === 'true' ||
      localStorage.getItem('enable_task') === 'true'
    );
  } catch (_) {
    return true;
  }
};

export const visibleNavItems = (user) => {
  const role = user?.role ?? 0;
  return NAV_SECTIONS.filter((s) => !s.minRole || role >= s.minRole).map(
    (s) => ({
      ...s,
      items: s.items.filter((it) => !it.minRole || role >= it.minRole),
    }),
  );
};

// Pull the bridged user identity directly from localStorage — set by
// OidcRedirect/zita-bootstrap. The v2 hi-fi shell intentionally
// avoids the StatusContext/UserContext used by the legacy chrome (those
// pull a chunk of v1 state we don't need here); a thin localStorage
// read is enough to surface "who am I" + a logout escape hatch.
export const useBridgedUser = () => {
  // Lazy initializer, not an effect: the role-gated nav sections must be
  // decided on the FIRST paint, or an admin's rail visibly pops in a frame
  // late (and a user's flashes admin entries it then removes).
  const [user] = useState(() => {
    try {
      const raw = localStorage.getItem('user');
      return raw ? JSON.parse(raw) : null;
    } catch (e) {
      // malformed payload — leave user null, the logout button still works
      return null;
    }
  });
  return user;
};

// readTenantSlug is imported, not redeclared: this file carried a third copy
// of the same fallback ('default', the tenant *id* rather than its routing
// slug) while helpers/apiMode.js had a fourth ('lurus'). One concept, one
// answer — see hooks/common/useTenantSlug.js.

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
  const navigate = useNavigate();
  const tasksFeatureEnabled = navItemEnabledByFeatureFlags('mj-logs');

  // ⌘K / Ctrl-K actually opens the palette now. The rail has rendered a ⌘K
  // badge next to the search button since the shell was built, but the repo
  // had exactly one keydown listener in it (Playground's) and none of them was
  // this — so the badge advertised a shortcut that did nothing. It goes to the
  // same route the search button does; the badge and the button are now the
  // same affordance rather than one real and one decorative.
  useEffect(() => {
    const onKey = (e) => {
      if (e.key !== 'k' && e.key !== 'K') return;
      if (!e.metaKey && !e.ctrlKey) return;
      // Browsers bind Ctrl-K to the address bar; this is a deliberate override
      // inside our own console, so preventDefault is required for it to work.
      e.preventDefault();
      closeNav();
      navigate('/console/v2/cmdk');
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [navigate]);

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
          data-testid='shell-search-button'
          aria-label={t('console.shell.search', 'search anything')}
          onClick={() => {
            closeNav();
            navigate('/console/v2/cmdk');
          }}
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

        {visibleNavItems(user).map((s) => (
          <div className='nav-section' key={s.h}>
            <div className='nav-h'>{t(s.hKey, s.h)}</div>
            {s.items
              .filter((it) => it.id !== 'mj-logs' || tasksFeatureEnabled)
              .map((it) => {
                const className =
                  'nav-i' + (activeId === it.id ? ' active' : '');
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
