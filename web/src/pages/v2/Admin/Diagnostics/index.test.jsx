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

vi.mock('../../../../helpers', () => ({
  API: {
    get: vi.fn(),
    delete: vi.fn(),
  },
  showSuccess: vi.fn(),
  showError: vi.fn(),
}));

vi.mock('../../../../components/hifi/HFShell', () => ({
  default: ({ children }) =>
    React.createElement('div', { 'data-testid': 'hf-shell' }, children),
}));

// `confirmDialogState.lastProps` lets a test inspect exactly what this page
// handed ConfirmDialog — in particular `confirmText` — without pulling in
// ConfirmDialog's own arm/disarm rendering (that contract is
// ConfirmDialog.test.jsx's job). vi.hoisted because vi.mock factories are
// lifted above this file's other module-scope statements (same pattern as
// web/src/components/playground/cx_optimized_components.test.jsx).
const confirmDialogState = vi.hoisted(() => ({ lastProps: null }));

// Same shim ModelRateLimits/index.test.jsx uses: the typed-confirmation
// arm/disarm logic is ConfirmDialog's own unit-tested contract
// (ConfirmDialog.test.jsx); what THIS page's test must prove is that
// nothing is sent to the backend before onConfirm fires, that the dialog
// only opens from the "purge all" button, and that the props this page
// hands to ConfirmDialog actually arm it (confirmText).
vi.mock('../../../../components/common/ConfirmDialog', () => ({
  default: (props) => {
    confirmDialogState.lastProps = props;
    const { visible, onConfirm, onCancel, title } = props;
    return visible
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
      : null;
  },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split('{{' + k + '}}').join(String(v));
        }
      }
      return out;
    },
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import V2AdminDiagnostics from './index';
import { API, showSuccess } from '../../../../helpers';

const AFFINITY_URL = '/api/v2/admin/routing/affinity';
const TOTP_URL = '/api/v2/admin/security/totp-stats';

const affinityResponse = (data, scope = 'replica') => ({
  data: { success: true, scope, data },
});

const totpResponse = (data) => ({
  data: { success: true, message: '', data },
});

// backend defaults to 'redis' — this is deliberately "the redis case" for
// mem_entries (R3): the counter is meaningful when backend=='memory' and
// reads 0 otherwise (doc/product-integration-guide.md), so the default
// fixture must not be the memory case or the redis-hides-mem_entries
// assertion below would not exercise that branch at all.
const AFFINITY_DATA = {
  enabled: true,
  ttl_seconds: 300,
  hit: 137,
  miss: 42,
  stale: 5,
  backend: 'redis',
  mem_entries: 9,
};

const TOTP_DATA = {
  enrolled: 8,
  pending: 3,
  total_users: 20,
  adoption_pct: 40,
  backup_codes_exhausted: 1,
  no_codes_issued: 2,
};

// Branches the shared API.get mock by URL, matching the page's
// Promise.allSettled([affinity, totp]) fetch.
const wireGet = (affinityData = AFFINITY_DATA, totpData = TOTP_DATA) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/routing/affinity')) {
      return Promise.resolve(affinityResponse(affinityData));
    }
    if (String(url).includes('/security/totp-stats')) {
      return Promise.resolve(totpResponse(totpData));
    }
    return Promise.reject(new Error(`unexpected GET ${url}`));
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.delete.mockReset();
  showSuccess.mockReset();
  confirmDialogState.lastProps = null;
});

describe('Admin Diagnostics page — affinity + TOTP panels', () => {
  it('calls both endpoints on mount', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-hit'));

    const calledUrls = API.get.mock.calls.map((c) => String(c[0]));
    expect(calledUrls).toContain(AFFINITY_URL);
    expect(calledUrls).toContain(TOTP_URL);
  });

  // The trap oracle for mutation #1 (hard-coding a rendered counter): every
  // value asserted here is a distinctive number that cannot coincidentally
  // match a plausible hard-coded default.
  it('renders the real affinity counters returned by the API, not a placeholder', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-hit'));

    expect(screen.getByTestId('diag-affinity-hit').textContent).toBe('137');
    expect(screen.getByTestId('diag-affinity-miss').textContent).toBe('42');
    expect(screen.getByTestId('diag-affinity-stale').textContent).toBe('5');
    expect(screen.getByTestId('diag-affinity-backend').textContent).toBe(
      'redis',
    );
    expect(screen.getByTestId('diag-affinity-ttl').textContent).toBe('300');
    expect(screen.getByTestId('diag-affinity-enabled').textContent).toBe('yes');
    // R6: scope is rendered from the response, not a hard-coded string.
    expect(screen.getByTestId('diag-affinity-scope-note').textContent).toMatch(
      /replica/,
    );
  });

  // R3 (B-2): mem_entries counts the in-process fallback map and is
  // meaningless while the live backend is redis — the fixture above uses
  // backend:'redis', so this asserts the counter is NOT presented as a
  // binding count in that case, only the caveat sub-label is.
  it('does not render mem_entries as a binding count when backend is redis', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-hit'));

    expect(screen.queryByTestId('diag-affinity-mementries')).toBeNull();
    expect(
      screen.getByTestId('diag-affinity-mementries-note').textContent,
    ).toMatch(/redis/i);
  });

  it('renders mem_entries as a real count when backend is memory', async () => {
    wireGet({ ...AFFINITY_DATA, backend: 'memory', mem_entries: 9 });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-mementries'));

    expect(screen.getByTestId('diag-affinity-mementries').textContent).toBe(
      '9',
    );
    expect(screen.queryByTestId('diag-affinity-mementries-note')).toBeNull();
  });

  // R5 (A-5): the `no` half of yesNo has no assertion in the default
  // fixture (enabled:true) — assert it explicitly for enabled:false.
  it('renders "no" for affinity.enabled:false, not a truthy default', async () => {
    wireGet({ ...AFFINITY_DATA, enabled: false });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-enabled'));
    expect(screen.getByTestId('diag-affinity-enabled').textContent).toBe('no');
  });

  it('renders the real TOTP adoption numbers returned by the API, not a placeholder', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-totp-enrolled'));

    expect(screen.getByTestId('diag-totp-enrolled').textContent).toBe('8');
    expect(screen.getByTestId('diag-totp-pending').textContent).toBe('3');
    expect(screen.getByTestId('diag-totp-total').textContent).toBe('20');
    expect(screen.getByTestId('diag-totp-adoption-pct').textContent).toBe(
      '40.0%',
    );
    expect(screen.getByTestId('diag-totp-exhausted').textContent).toBe('1');
    expect(screen.getByTestId('diag-totp-no-codes').textContent).toBe('2');
  });

  // Honesty guard: the plan explicitly forbids inventing a backend field.
  // This asserts the page SAYS the number is unavailable instead of
  // silently omitting the whole idea (which would look like nobody thought
  // of it) or fabricating a count.
  it('states plainly that the no-second-factor privileged-account count is not available from this API', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-totp-no-factor-note'));
    expect(screen.getByTestId('diag-totp-no-factor-note').textContent).toMatch(
      /not exposed/i,
    );
  });

  it('shows a permission notice when either endpoint answers success:false', async () => {
    API.get.mockImplementation((url) => {
      if (String(url).includes('/routing/affinity')) {
        return Promise.resolve(affinityResponse(AFFINITY_DATA));
      }
      return Promise.resolve({
        data: { success: false, message: 'root required' },
      });
    });

    render(<V2AdminDiagnostics />);

    await waitFor(() =>
      screen.getByText(/You do not have permission to view diagnostics/),
    );
    expect(screen.queryByTestId('diag-affinity-hit')).toBeNull();
  });

  // R5 (A-3/A-4): a rejected GET with status 403 (RootJWTAuth's Bearer-JWT
  // branch) must hit the same forbidden panel as a 200 {success:false} —
  // deleting the 403 branch in classifySettled must turn this red.
  it('shows the permission notice when a GET rejects with HTTP 403', async () => {
    API.get.mockImplementation((url) => {
      if (String(url).includes('/routing/affinity')) {
        return Promise.reject({ response: { status: 403 } });
      }
      return Promise.resolve(totpResponse(TOTP_DATA));
    });

    render(<V2AdminDiagnostics />);

    await waitFor(() =>
      screen.getByText(/You do not have permission to view diagnostics/),
    );
    expect(screen.queryByTestId('diag-affinity-hit')).toBeNull();
  });

  // R2 (B-1): a genuine backend failure on ONE endpoint must not blank or
  // fabricate numbers for either panel. Promise.all (unlike allSettled)
  // would reject here and both panels would render nothing — this is the
  // trap oracle for that regression.
  it('renders an explicit unavailable marker for a panel whose GET failed, and still renders the other panel', async () => {
    API.get.mockImplementation((url) => {
      if (String(url).includes('/routing/affinity')) {
        return Promise.resolve(affinityResponse(AFFINITY_DATA));
      }
      return Promise.reject({ response: { status: 500 } });
    });

    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-totp-unavailable'));

    // The failed panel must not show a fabricated 0.0%/no default anywhere.
    expect(screen.queryByTestId('diag-totp-adoption-pct')).toBeNull();
    expect(screen.queryByTestId('diag-totp-enrolled')).toBeNull();
    // The other panel, whose GET succeeded, still renders its real numbers.
    expect(screen.getByTestId('diag-affinity-hit').textContent).toBe('137');
  });
});

describe('Admin Diagnostics page — purge one binding', () => {
  it('sends a DELETE with the typed key and refetches on success', async () => {
    wireGet();
    API.delete.mockResolvedValue({ status: 204 });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-key-input'));
    fireEvent.change(screen.getByTestId('diag-affinity-purge-key-input'), {
      target: { value: 'abc123' },
    });
    fireEvent.click(screen.getByTestId('diag-affinity-purge-key-btn'));

    await waitFor(() =>
      expect(API.delete).toHaveBeenCalledWith(
        '/api/v2/admin/routing/affinity/abc123',
      ),
    );
    // Refetch: two GETs on mount + two more after a successful purge.
    await waitFor(() => expect(API.get.mock.calls.length).toBeGreaterThan(2));
  });

  it('reports "no such binding" on a 404 without touching the affinity numbers', async () => {
    wireGet();
    API.delete.mockRejectedValue({ response: { status: 404 } });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-key-input'));
    fireEvent.change(screen.getByTestId('diag-affinity-purge-key-input'), {
      target: { value: 'stale-key' },
    });
    fireEvent.click(screen.getByTestId('diag-affinity-purge-key-btn'));

    await waitFor(() =>
      expect(
        screen.getByTestId('diag-affinity-purge-key-msg').textContent,
      ).toMatch(/no such binding/i),
    );
    expect(screen.getByTestId('diag-affinity-hit').textContent).toBe('137');
  });

  it('does not send a request when the key field is empty', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-key-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-key-btn'));

    expect(API.delete).not.toHaveBeenCalled();
  });

  // R5 (A-6): a logged-in non-root session answers this DELETE with HTTP
  // 200 {"success":false,"message":...} — neither 204 nor a rejection.
  // Without the non-204 else-branch the button silently does nothing.
  it('shows the server message on a non-204 response', async () => {
    wireGet();
    API.delete.mockResolvedValue({
      status: 200,
      data: { success: false, message: 'root required for this action' },
    });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-key-input'));
    fireEvent.change(screen.getByTestId('diag-affinity-purge-key-input'), {
      target: { value: 'abc123' },
    });
    fireEvent.click(screen.getByTestId('diag-affinity-purge-key-btn'));

    await waitFor(() =>
      expect(
        screen.getByTestId('diag-affinity-purge-key-msg').textContent,
      ).toMatch(/root required for this action/),
    );
  });
});

describe('Admin Diagnostics page — purge all (confirmation-gated)', () => {
  // The trap oracle for mutation #2 (removing the confirmation step):
  // clicking the visible "purge all" button alone must never reach the
  // backend.
  it('sends nothing until the confirmation dialog is opened AND confirmed', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-all-btn'));

    // The button click alone must only open the dialog, never call the API.
    expect(API.delete).not.toHaveBeenCalled();
    expect(screen.getByTestId('confirm-dialog')).toBeInTheDocument();

    API.delete.mockResolvedValue({
      data: { success: true, data: { purged: 4, complete: true } },
    });
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() =>
      expect(API.delete).toHaveBeenCalledWith(
        '/api/v2/admin/routing/affinity?all=true',
      ),
    );
    // R5 (A-5): the dialog closes after a successful confirm — without
    // setPurgeAllOpen(false) it would stay mounted.
    await waitFor(() =>
      expect(screen.queryByTestId('confirm-dialog')).toBeNull(),
    );
  });

  // R1 (A-1): the confirmation contract is only real if this page actually
  // hands ConfirmDialog the literal text that arms its Confirm button
  // (ConfirmDialog.jsx's `armed = inputValue === confirmText`). A missing
  // or wrong confirmText prop makes purge-all permanently unusable in
  // production while every test using the local click-through stub still
  // passes — so this asserts the prop directly.
  it('passes the literal confirmText "PURGE ALL" to ConfirmDialog', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-all-btn'));

    expect(confirmDialogState.lastProps.confirmText).toBe('PURGE ALL');
  });

  it('sends nothing when the confirmation is cancelled', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('confirm-cancel'));

    expect(API.delete).not.toHaveBeenCalled();
    expect(screen.queryByTestId('confirm-dialog')).toBeNull();
  });

  it('surfaces an incomplete purge instead of reporting it as fully done', async () => {
    wireGet();
    API.delete.mockResolvedValue({
      data: { success: true, data: { purged: 2, complete: false } },
    });
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    expect(String(showSuccess.mock.calls[0][0])).toMatch(/incomplete/i);
  });

  // R4 (B-3): doPurgeAll has no catch upstream of ConfirmDialog, so a
  // rejected DELETE (e.g. Redis down) must not throw out of the handler —
  // it must leave the dialog open, matching the ModelRateLimits precedent
  // (index.jsx's `catch (_) { // error toast from the interceptor }`).
  it('leaves the confirmation dialog open when the purge-all request is rejected', async () => {
    wireGet();
    render(<V2AdminDiagnostics />);

    await waitFor(() => screen.getByTestId('diag-affinity-purge-all-btn'));
    fireEvent.click(screen.getByTestId('diag-affinity-purge-all-btn'));

    API.delete.mockRejectedValue({ response: { status: 500 } });
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => expect(API.delete).toHaveBeenCalled());
    // Give the rejected promise's catch a turn to run, then assert the
    // dialog is still mounted (no uncaught rejection prevented cleanup).
    await waitFor(() =>
      expect(screen.getByTestId('confirm-dialog')).toBeInTheDocument(),
    );
  });
});
