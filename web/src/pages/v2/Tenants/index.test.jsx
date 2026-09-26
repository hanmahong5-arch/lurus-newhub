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

// ─── mocks ───────────────────────────────────────────────────────────────────

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm, onCancel, title }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-ok', onClick: onConfirm },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

vi.mock('./CreditPoolDrawer', () => ({
  default: ({ tenantId, tenantName, onClose }) =>
    React.createElement(
      'div',
      { 'data-testid': 'credit-pool-drawer' },
      `pool for ${tenantName} (${tenantId})`,
      React.createElement('button', { onClick: onClose }, 'close'),
    ),
}));

vi.mock('./InvitesDrawer', () => ({
  default: ({ tenantId, tenantName, onClose }) =>
    React.createElement(
      'div',
      { 'data-testid': 'invites-drawer' },
      `invites for ${tenantName} (${tenantId})`,
      React.createElement('button', { onClick: onClose }, 'close'),
    ),
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
vi.mock('react-i18next', () => ({
  // These pages now reach the shared money helpers, which pull in the i18n
  // module; that calls .use(initReactI18next), so the mock has to carry it.
  initReactI18next: { type: '3rdParty', init: () => {} },
  Trans: ({ children }) => children,
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

import HFTenants from './index';
import { API, showSuccess } from '../../../helpers';

// ─── fixtures ────────────────────────────────────────────────────────────────

// All keys MUST be snake_case — matching repo.Tenant JSON tags:
// id, slug, name, status, plan_type, max_users, max_quota,
// zitadel_org_id, created_at, updated_at
// user_count is from TenantStats, not included in list response.
const makeTenant = (overrides = {}) => ({
  id: 'tenant-uuid-1',
  slug: 'acme',
  name: 'Acme Corp',
  status: 1,
  plan_type: 'free',
  max_users: 100,
  max_quota: 0,
  zitadel_org_id: 'zorg-1', // wire 物理列名,待 idp-migration
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  ...overrides,
});

const listResponse = (tenants) => ({
  data: {
    success: true,
    data: { tenants, total: tenants.length, page: 1, page_size: 50 },
  },
});

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showSuccess.mockReset();
});

// ─── β1a: list renders rows with snake_case keys ─────────────────────────────

describe('Tenants page — list renders rows', () => {
  it('renders tenant name and slug from snake_case JSON', async () => {
    const tenant = makeTenant({ name: 'Global Inc', slug: 'global' });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByText('Global Inc'));
    expect(screen.getByText('global')).toBeTruthy();
  });

  it('renders plan_type tag (not camelCase planType)', async () => {
    const tenant = makeTenant({ plan_type: 'enterprise' });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByText('enterprise'));
  });

  it('renders status badge from snake_case status field', async () => {
    const tenant = makeTenant({ status: 1 });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-status-${tenant.id}`));
    expect(screen.getByTestId(`tenant-status-${tenant.id}`).textContent).toBe(
      'active',
    );
  });

  it('renders disabled tenant with enable button', async () => {
    const tenant = makeTenant({ status: 2 });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-enable-btn-${tenant.id}`));
  });

  it('renders suspended tenant with enable button', async () => {
    const tenant = makeTenant({ status: 3 });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-enable-btn-${tenant.id}`));
  });

  it('reads response from tenants key not items key', async () => {
    // Backend returns data.tenants, not data.items
    const tenant = makeTenant();
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByText('Acme Corp'));
  });

  it('shows user_count column (snake_case, not userCount)', async () => {
    const tenant = makeTenant({ user_count: 42 });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByText('42'));
  });
});

// ─── β1b: create form posts snake_case ────────────────────────────────────────

describe('Tenants page — create form', () => {
  it('POST body uses snake_case field names: quota_limit, status', async () => {
    API.get.mockResolvedValue(listResponse([]));
    API.post.mockResolvedValue({
      data: { success: true, data: { id: 'new-id', slug: 'newco' } },
    });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.queryByText('Loading…') === null);

    // Open create modal
    fireEvent.click(screen.getByText('+ new tenant'));

    await waitFor(() => screen.getByPlaceholderText('e.g. Acme Corp'));

    // Fill name and slug (required)
    fireEvent.change(screen.getByPlaceholderText('e.g. Acme Corp'), {
      target: { value: 'NewCo' },
    });
    fireEvent.change(screen.getByPlaceholderText('e.g. acme'), {
      target: { value: 'newco' },
    });

    // Submit via the submit button
    fireEvent.click(screen.getByText('create tenant'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/tenants',
        expect.objectContaining({
          name: 'NewCo',
          slug: 'newco',
          status: 1,
        }),
      );
    });

    // Verify no camelCase fields leaked into POST body
    const [, body] = API.post.mock.calls[0];
    expect(body).not.toHaveProperty('quotaLimit');
    expect(body).not.toHaveProperty('planType');
    expect(body).toHaveProperty('plan');
  });
});

// ─── β1c: enable / disable / suspend fire correct POST ───────────────────────

describe('Tenants page — action buttons', () => {
  it('disable button POSTs to /:id/disable using snake_case id', async () => {
    const tenant = makeTenant({ id: 'uuid-active', status: 1 });
    API.get.mockResolvedValue(listResponse([tenant]));
    API.post.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-disable-btn-${tenant.id}`));

    fireEvent.click(screen.getByTestId(`tenant-disable-btn-${tenant.id}`));

    // ConfirmDialog appears
    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/uuid-active/disable',
        {},
      );
    });
  });

  it('enable button POSTs to /:id/enable for disabled tenant', async () => {
    const tenant = makeTenant({ id: 'uuid-disabled', status: 2 });
    API.get.mockResolvedValue(listResponse([tenant]));
    API.post.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-enable-btn-${tenant.id}`));

    fireEvent.click(screen.getByTestId(`tenant-enable-btn-${tenant.id}`));

    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/uuid-disabled/enable',
        {},
      );
    });
  });

  it('suspend button POSTs to /:id/suspend', async () => {
    const tenant = makeTenant({ id: 'uuid-suspendable', status: 1 });
    API.get.mockResolvedValue(listResponse([tenant]));
    API.post.mockResolvedValue({ data: { success: true } });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-suspend-btn-${tenant.id}`));

    fireEvent.click(screen.getByTestId(`tenant-suspend-btn-${tenant.id}`));

    await waitFor(() => screen.getByTestId('confirm-dialog'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/uuid-suspendable/suspend',
        {},
      );
    });
  });
});

// ─── β1d: stats drawer calls /stats endpoint with snake_case id ──────────────

describe('Tenants page — stats drawer', () => {
  it('opens stats drawer and fetches /api/v2/admin/tenants/:id/stats', async () => {
    const tenant = makeTenant({ id: 'stats-tenant', name: 'Stats Co' });
    API.get.mockImplementation((url) => {
      if (url.includes('/stats')) {
        return Promise.resolve({
          data: {
            success: true,
            data: { user_count: 5, token_count: 12 },
          },
        });
      }
      return Promise.resolve(listResponse([tenant]));
    });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-stats-btn-${tenant.id}`));

    fireEvent.click(screen.getByTestId(`tenant-stats-btn-${tenant.id}`));

    await waitFor(() => {
      expect(API.get).toHaveBeenCalledWith(
        '/api/v2/admin/tenants/stats-tenant/stats',
      );
    });

    // Stats drawer labels each value in words, never with the wire field name
    await waitFor(() => screen.getByText('Stats Co · stats'));
    await waitFor(() => screen.getByText('Seats used'));
    expect(screen.queryByText('user_count')).toBeNull();
  });
});

// ─── L6: invites drawer opens with the clicked tenant's id/name ─────────────

describe('Tenants page — invites drawer', () => {
  it('opens the invites drawer for the clicked tenant row', async () => {
    const tenant = makeTenant({ id: 'invites-tenant', name: 'Invites Co' });
    API.get.mockResolvedValue(listResponse([tenant]));

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId(`tenant-invites-btn-${tenant.id}`));
    expect(screen.queryByTestId('invites-drawer')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId(`tenant-invites-btn-${tenant.id}`));

    await waitFor(() => screen.getByTestId('invites-drawer'));
    expect(screen.getByTestId('invites-drawer')).toHaveTextContent(
      'invites for Invites Co (invites-tenant)',
    );

    fireEvent.click(screen.getByText('close'));
    await waitFor(() =>
      expect(screen.queryByTestId('invites-drawer')).not.toBeInTheDocument(),
    );
  });
});

// ─── β1e: 403 shows forbidden panel ─────────────────────────────────────────

describe('Tenants page — 403 guard', () => {
  it('shows Admin access required on 403', async () => {
    API.get.mockRejectedValue({ response: { status: 403 } });

    render(React.createElement(HFTenants));

    await waitFor(() => {
      expect(
        screen.getAllByText('Admin access required').length,
      ).toBeGreaterThan(0);
    });
  });
});

// ─── cycle-14 L8: a failed read is not an empty tenant table ────────────────
//
// fetchTenants used to set `forbidden` on 403 and swallow every other
// outcome, leaving `tenants` at [] — so a 500 drew "0 tenants · $0.00 used"
// in the page header. The money figure is the part the operator quotes.

describe('Tenants page — a failed read is not a zero', () => {
  it('does not render a tenant count or a dollar figure when the GET 500s', async () => {
    API.get.mockRejectedValue({
      response: { status: 500, data: { success: false } },
    });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId('tenants-load-error'));
    // `/tenants/` alone would match the page's own crumb label, so the
    // assertion is on the COUNT shape — "0 tenants" — not the word.
    expect(screen.queryByText(/\d+ tenants/)).toBeNull();
    expect(screen.queryByText(/\$0\.00 used/)).toBeNull();
    expect(
      screen.queryByText('No tenants yet. Create one to get started.'),
    ).toBeNull();
    // A transport failure is not a permission refusal.
    expect(screen.queryByText('Admin access required')).toBeNull();
  });

  it('treats a 200 carrying success:false as a failed read, not as zero tenants', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'tenant store unavailable' },
    });

    render(React.createElement(HFTenants));

    await waitFor(() => screen.getByTestId('tenants-load-error'));
    expect(screen.queryByText(/\$0\.00 used/)).toBeNull();
  });

  it('clears the error and shows the count once a retry succeeds', async () => {
    let fail = true;
    API.get.mockImplementation(() =>
      fail
        ? Promise.reject({ response: { status: 500 } })
        : Promise.resolve(listResponse([makeTenant({ used_quota: 0 })])),
    );

    render(React.createElement(HFTenants));
    await waitFor(() => screen.getByTestId('tenants-load-error'));

    fail = false;
    fireEvent.click(screen.getByTestId('tenants-retry'));

    await waitFor(() => screen.getByTestId('tenant-row-tenant-uuid-1'));
    expect(screen.queryByTestId('tenants-load-error')).toBeNull();
  });
});
