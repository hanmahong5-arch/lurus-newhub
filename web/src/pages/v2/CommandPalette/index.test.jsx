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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

const mockIsAdmin = vi.fn(() => false);
vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  isAdmin: (...a) => mockIsAdmin(...a),
}));

const mockNavigate = vi.fn();
vi.mock('react-router-dom', () => ({
  useNavigate: () => mockNavigate,
}));

// HFShell is stubbed, but NAV_SECTIONS/visibleNavItems/useBridgedUser are
// NOT: the navigate group is supposed to reuse the rail's real section list
// and role-gating logic, and stubbing them would let the palette drift back
// to a hand-maintained copy without any test noticing.
//
// visibleNavItems is wrapped in a spy (delegating to the real
// implementation) so tests can assert what user object the palette actually
// passed it — the wiring B-F2 was about. A DOM-text assertion on the
// rendered "navigate" group cannot see this: HFCmdK slices every group's
// rows to MAX_PER_GROUP=6 (index.jsx), and the two unconditional sections
// (workspace + my account) alone already total 8 items, so anything in a
// minRole-gated section — admin or root — is always past the visible cap
// regardless of which role produced it. That truncation is pre-existing
// and out of this lane's scope; the spy tests the actual mechanism instead.
const { visibleNavItemsSpy } = vi.hoisted(() => ({
  visibleNavItemsSpy: vi.fn(),
}));
vi.mock('../../../components/hifi/HFShell', async (importOriginal) => {
  const actual = await importOriginal();
  visibleNavItemsSpy.mockImplementation((...args) =>
    actual.visibleNavItems(...args),
  );
  return {
    default: ({ children }) =>
      React.createElement('div', { 'data-testid': 'hf-shell' }, children),
    NAV_SECTIONS: actual.NAV_SECTIONS,
    visibleNavItems: visibleNavItemsSpy,
    navItemEnabledByFeatureFlags: actual.navItemEnabledByFeatureFlags,
    useBridgedUser: actual.useBridgedUser,
  };
});

vi.mock('react-i18next', () => ({
  initReactI18next: { type: '3rdParty', init: () => {} },
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split(`{{${k}}}`).join(String(v));
        }
      }
      return out;
    },
  }),
}));

import HFCmdK from './index';
import { API } from '../../../helpers';

const wireGet = ({
  models = [{ model_name: 'deepseek-chat', vendor: 'DeepSeek' }],
  pricing = [{ model_name: 'deepseek-chat', quota_type: 0, model_ratio: 0.5 }],
  tokens = [{ name: 'prod-key', unlimited_quota: true }],
  logs = [{ model_name: 'deepseek-chat', total_latency_ms: 412 }],
  channels = [{ name: 'deepseek-primary', status: 1 }],
} = {}) => {
  API.get.mockImplementation((url) => {
    const ok = (data) => Promise.resolve({ data: { success: true, data } });
    if (url.includes('/models')) return ok({ items: models });
    if (url.includes('/pricing')) return ok({ pricing });
    if (url.includes('/tokens')) return ok({ items: tokens });
    if (url.includes('/logs')) return ok({ logs });
    if (url.includes('/channels')) return ok({ items: channels });
    return ok({});
  });
};

beforeEach(() => {
  API.get.mockReset();
  mockNavigate.mockReset();
  mockIsAdmin.mockReset();
  mockIsAdmin.mockReturnValue(false);
  visibleNavItemsSpy.mockClear();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
});

const setBridgedUser = (role) =>
  window.localStorage.setItem(
    'user',
    JSON.stringify({ role, username: 'palette-test' }),
  );

// Every group in this palette used to be hardcoded demo data invented on a
// design canvas: channels "openai/main · $412.80 / 1h · 99.4% healthy", models
// "gpt-4o · $2.50 / $10 · 128k ctx", requests "req_1f4a…e90c · 504". The
// palette described channels that do not exist at prices we do not charge.
describe('CommandPalette — real data', () => {
  it('renders models, tokens and recent requests from the API, not demo strings', async () => {
    wireGet();

    render(<HFCmdK />);

    await waitFor(() => {
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0);
    });
    expect(screen.getByText('prod-key')).toBeTruthy();

    const body = document.body.textContent;
    // The invented fixtures must be gone.
    for (const fake of [
      'openai/main',
      '$412.80',
      '99.4% healthy',
      'anthropic/eu',
      'vertex/asia',
      '128k ctx',
      'req_1f4a',
    ]) {
      expect(body).not.toContain(fake);
    }
  });

  it('joins /pricing onto /models instead of inventing a price', async () => {
    wireGet();

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    // vendor · ratio, both from the API.
    expect(document.body.textContent).toContain('DeepSeek · ratio 0.5');
  });

  it('shows a model with no pricing row without fabricating one', async () => {
    wireGet({ pricing: [] });

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    expect(document.body.textContent).toContain('DeepSeek');
    expect(document.body.textContent).not.toContain('ratio');
  });

  it('does not request or show channels for a non-admin', async () => {
    wireGet();

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    // /channels is behind AdminAuth; asking would just 403.
    const urls = API.get.mock.calls.map(([u]) => u);
    expect(urls.some((u) => u.includes('/channels'))).toBe(false);
    expect(screen.queryByTestId('palette-row-channels')).toBeNull();
  });

  it('shows channels for an admin', async () => {
    mockIsAdmin.mockReturnValue(true);
    wireGet();

    render(<HFCmdK />);

    await waitFor(() => {
      expect(screen.getByText('deepseek-primary')).toBeTruthy();
    });
  });

  it('drops a group entirely rather than showing an empty one', async () => {
    // An empty "tokens" heading reads as "you have no tokens", which we cannot
    // claim when the request may simply have failed.
    wireGet({ tokens: [] });

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    expect(screen.queryByTestId('palette-row-tokens')).toBeNull();
  });

  it('reuses the shell nav sections for the navigate group', async () => {
    wireGet();

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    // Sourced from HFShell.NAV_SECTIONS (not stubbed), so the palette and the
    // rail cannot drift apart.
    const rows = screen.getAllByTestId('palette-row-navigate');
    expect(rows.length).toBeGreaterThan(0);
    expect(document.body.textContent).toContain('/console/v2/dashboard');
  });

  // The rail hides "MJ / Task logs" when a deployment has both enable_drawing
  // and enable_task off, because the page has nothing to show. The palette
  // reads the same NAV_SECTIONS, so without the same gate it kept offering
  // that destination — the rail's honesty fix, undone one keystroke away.
  // Both surfaces now call navItemEnabledByFeatureFlags.
  it('does not offer MJ / Task logs when the deployment has both task flags off', async () => {
    wireGet();
    window.localStorage.setItem('enable_drawing', 'false');
    window.localStorage.setItem('enable_task', 'false');

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    expect(document.body.textContent).not.toContain('/console/v2/tasks');
  });

  it('offers MJ / Task logs when either task flag is on', async () => {
    wireGet();
    window.localStorage.setItem('enable_drawing', 'false');
    window.localStorage.setItem('enable_task', 'true');

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );
    expect(document.body.textContent).toContain('/console/v2/tasks');
  });

  // B-F2/A-F3 regression: the palette used to gate the navigate group on
  // `admin ? 10 : 0`, a two-value stand-in that cannot distinguish admin
  // (role 10) from root (role 100) — a role-10 admin was offered the
  // root-only "Background tasks" destination (admin-system-tasks,
  // minRole:100 within the minRole:10 "operations & insights" section)
  // that the rail itself correctly hid.
  //
  // These assert against visibleNavItemsSpy's call arguments and return
  // value directly, not the rendered DOM: HFCmdK slices the navigate
  // group's rows to MAX_PER_GROUP=6, and the two unconditional sections
  // (workspace + my account) already total 8 items, so "Background tasks"
  // (deep in the fifth, last section) is never within the visible cap
  // regardless of role — a DOM assertion here would pass or fail for
  // reasons unrelated to role gating. Asserting on visibleNavItems' own
  // input/output proves the actual mechanism this finding is about.
  it('calls visibleNavItems with the real bridged role, and it excludes "Background tasks" for role 10', async () => {
    setBridgedUser(10);
    wireGet();

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );

    expect(visibleNavItemsSpy).toHaveBeenCalled();
    const [calledWith] = visibleNavItemsSpy.mock.calls[0];
    expect(calledWith?.role).toBe(10);

    const sections = visibleNavItemsSpy.mock.results[0].value;
    const allHrefs = sections.flatMap((s) => s.items.map((it) => it.href));
    // The section itself (minRole:10) is visible — a sibling item proves it.
    // Rankings is the sibling to use: cycle 12 L3 moved every OTHER item in
    // "operations & insights" to minRole:100 because each one's only backend
    // call is under /api/v2/admin (RootJWTAuth). Rankings stayed at the
    // section's own minRole because it falls back to the tenant-scoped
    // /api/v2/:tenant_slug/analytics/rankings for a non-root caller.
    expect(allHrefs).toContain('/console/v2/admin/rankings');
    // The per-item minRole:100 destinations are not.
    expect(allHrefs).not.toContain('/console/v2/admin/system-tasks');
    expect(allHrefs).not.toContain('/console/v2/admin/gateway');
  });

  it('calls visibleNavItems with the real bridged role, and it includes "Background tasks" for role 100', async () => {
    setBridgedUser(100);
    wireGet();

    render(<HFCmdK />);

    await waitFor(() =>
      expect(screen.getAllByText('deepseek-chat').length).toBeGreaterThan(0),
    );

    expect(visibleNavItemsSpy).toHaveBeenCalled();
    const [calledWith] = visibleNavItemsSpy.mock.calls[0];
    expect(calledWith?.role).toBe(100);

    const sections = visibleNavItemsSpy.mock.results[0].value;
    const allHrefs = sections.flatMap((s) => s.items.map((it) => it.href));
    expect(allHrefs).toContain('/console/v2/admin/system-tasks');
  });

  it('filters as you type instead of ignoring the search box', async () => {
    wireGet();

    render(<HFCmdK />);
    await waitFor(() => expect(screen.getByText('prod-key')).toBeTruthy());

    fireEvent.change(screen.getByTestId('palette-input'), {
      target: { value: 'prod-key' },
    });

    await waitFor(() => {
      expect(screen.queryAllByText('deepseek-chat')).toHaveLength(0);
    });
    expect(screen.getByText('prod-key')).toBeTruthy();
  });

  it('reports the real result count, not a constant', async () => {
    wireGet();

    render(<HFCmdK />);
    await waitFor(() => expect(screen.getByText('prod-key')).toBeTruthy());

    // The footer used to read "72 results" no matter what was on screen.
    const before = screen.getByTestId('palette-count').textContent;
    expect(before).not.toContain('72');

    fireEvent.change(screen.getByTestId('palette-input'), {
      target: { value: 'prod-key' },
    });
    await waitFor(() => {
      expect(screen.getByTestId('palette-count').textContent).toContain('1');
    });
  });

  it('navigates when a row is chosen — rows are actions, not decoration', async () => {
    wireGet();

    render(<HFCmdK />);
    await waitFor(() => expect(screen.getByText('prod-key')).toBeTruthy());

    fireEvent.click(screen.getByText('prod-key'));

    await waitFor(() => {
      expect(mockNavigate).toHaveBeenCalledWith('/console/v2/token');
    });
  });

  it('offers no action without a backend behind it', async () => {
    wireGet();

    render(<HFCmdK />);
    await waitFor(() => expect(screen.getByText('prod-key')).toBeTruthy());

    const body = document.body.textContent;
    // migration 029 gives projects no budget column; there is no rotate flow
    // reachable from here.
    expect(body).not.toContain('Set monthly budget');
    expect(body).not.toContain('Rotate api key');
  });

  it('advertises only shortcuts that exist', async () => {
    wireGet();

    render(<HFCmdK />);
    await waitFor(() => expect(screen.getByText('prod-key')).toBeTruthy());

    const body = document.body.textContent;
    expect(body).toContain('open palette');
    // These seven were listed in the keyboard reference and bound nowhere.
    for (const fake of [
      'go to dashboard',
      'go to logs',
      'go to channels',
      'go to tokens',
      'go to playground',
      'new token',
      'toggle theme',
    ]) {
      expect(body).not.toContain(fake);
    }
  });

  // Lock for `const loading = otherLoading || modelsLoading` — the palette
  // must still report "loading…" while only the models request (routed
  // through the shared hook) is still in flight.
  it('reports loading while only the models request is still in flight', async () => {
    let resolveModels;
    API.get.mockImplementation((url) => {
      const ok = (data) => Promise.resolve({ data: { success: true, data } });
      if (url.includes('/models'))
        return new Promise((r) => {
          resolveModels = r;
        });
      if (url.includes('/pricing')) return ok({ pricing: [] });
      if (url.includes('/tokens')) return ok({ items: [] });
      if (url.includes('/logs')) return ok({ logs: [] });
      return ok({});
    });

    render(<HFCmdK />);

    // Let the other three requests (already-resolved mocks) fully settle
    // through Promise.allSettled — otherLoading becomes false — while the
    // models request (routed through the hook) is still pending. If
    // `loading` dropped the modelsLoading half of the union, this is exactly
    // the window where it would flip to false too early.
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.getByText('loading…')).toBeTruthy();

    resolveModels({ data: { success: true, data: { items: [] } } });
    await waitFor(() => {
      expect(screen.queryByText('loading…')).toBeNull();
    });
  });
});
