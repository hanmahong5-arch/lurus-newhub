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
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions, active }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell', 'data-active': active },
      React.createElement('div', { 'data-testid': 'hf-actions' }, actions),
      children,
    ),
}));

// ConfirmDialog — use real implementation so armed/disarmed logic is tested.
// It imports Semi Modal; stub that out.
vi.mock('@douyinfe/semi-ui', () => {
  // Semi Button passes extra props (data-testid etc) through.
  const Button = ({ children, onClick, disabled, loading, ...rest }) =>
    React.createElement(
      'button',
      { onClick, disabled: disabled || loading, ...rest },
      children,
    );
  const Modal = ({ visible, children, footer, title, onCancel, closable }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal' },
          React.createElement('div', null, title),
          children,
          footer,
          closable !== false &&
            React.createElement(
              'button',
              { 'data-testid': 'modal-close', onClick: onCancel },
              'close',
            ),
        )
      : null;
  // Semi Input calls onChange(value) not onChange(event).
  // Wrap to accept both DOM event form (from fireEvent.change) and the raw-value
  // form that ConfirmDialog passes.
  const Input = ({ value, onChange, autoFocus: _af, ...rest }) =>
    React.createElement('input', {
      type: 'text',
      value: value ?? '',
      onChange: (e) => onChange && onChange(e.target.value),
      ...rest,
    });
  const Typography = {
    Text: ({ children }) => React.createElement('span', null, children),
  };
  return { Button, Input, Modal, Typography };
});

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
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

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}));

import HFFlows from './index';
import { API, showError } from '../../../helpers';

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.put.mockReset();
  showError.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
  // Reset form draft between tests.
  window.localStorage.removeItem('flow-newtoken-draft');
});

afterEach(() => {
  vi.useRealTimers();
});

// Helper: click the newToken tab.
function switchToNewToken() {
  fireEvent.click(screen.getByTestId('flows-tab-newToken'));
}

// Helper: fill step-1 fields.
function fillStep1(name, group = '') {
  fireEvent.change(screen.getByTestId('newtoken-name'), {
    target: { value: name },
  });
  if (group) {
    fireEvent.change(screen.getByTestId('newtoken-group'), {
      target: { value: group },
    });
  }
}

// Helper: advance to the next step via nav button.
function clickNext() {
  fireEvent.click(screen.getByTestId('flows-next'));
}

// Helper: fill the newChannel credentials step (step 2). newChannel is the
// default tab so no tab click is needed to reach step 1; this also drives
// the top "next" button from step 1 -> step 2.
function goToChannelCredentials() {
  clickNext(); // step 1 (vendor) -> step 2 (credentials)
}

function fillChannelCredentials({
  name = 'openai/main-2',
  baseURL = '',
  key = 'sk-test-key-12345',
  models = 'gpt-4,gpt-4o',
  orgId = '',
} = {}) {
  fireEvent.change(screen.getByTestId('newchannel-name'), {
    target: { value: name },
  });
  if (baseURL) {
    fireEvent.change(screen.getByTestId('newchannel-baseurl'), {
      target: { value: baseURL },
    });
  }
  fireEvent.change(screen.getByTestId('newchannel-key'), {
    target: { value: key },
  });
  fireEvent.change(screen.getByTestId('newchannel-models'), {
    target: { value: models },
  });
  if (orgId) {
    fireEvent.change(screen.getByTestId('newchannel-orgid'), {
      target: { value: orgId },
    });
  }
}

describe('Flows page — newToken wizard', () => {
  // 1. Full happy-path: step 1 → step 2 → step 3 review → typed confirm → POST.
  it('navigates through 3 wizard steps and submits', async () => {
    API.post.mockResolvedValueOnce({
      data: {
        success: true,
        data: { id: 7, name: 'my-token', key: 'sk-abc123' },
      },
    });

    render(<HFFlows />);
    switchToNewToken();

    // Step 1 — fill name + group.
    fillStep1('my-token', 'team-a');
    clickNext();

    // Step 2 — uncheck unlimited, set quota.
    fireEvent.click(screen.getByTestId('newtoken-unlimited'));
    fireEvent.change(screen.getByTestId('newtoken-quota'), {
      target: { value: '250000' },
    });
    clickNext();

    // Step 3 — review visible, open confirm dialog.
    expect(screen.getByTestId('newtoken-open-confirm')).toBeTruthy();
    fireEvent.click(screen.getByTestId('newtoken-open-confirm'));

    // ConfirmDialog renders with input — type the token name to arm it.
    const input = screen.getByRole('textbox');
    fireEvent.change(input, { target: { value: 'my-token' } });

    // Click the confirm button (data-testid from ConfirmDialog).
    fireEvent.click(screen.getByTestId('confirm-dialog-confirm'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });

    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/acme/tokens');
    expect(body.name).toBe('my-token');
    expect(body.group).toBe('team-a');
    expect(body.unlimited_quota).toBe(false);
    expect(body.remain_quota).toBe(250000);

    // Success screen renders the returned key.
    await waitFor(() => {
      expect(screen.getByTestId('newtoken-key').textContent).toBe('sk-abc123');
    });
  });

  // 2. Draft persists across step navigation via useFormDraft.
  it('persists draft via useFormDraft across step navigation', () => {
    render(<HFFlows />);
    switchToNewToken();

    // Type a name on step 1.
    fillStep1('persistent-token');

    // Advance to step 2.
    clickNext();

    // Go back to step 1.
    fireEvent.click(screen.getByTestId('flows-back'));

    // Name must still be present (draft restored from localStorage).
    expect(screen.getByTestId('newtoken-name').value).toBe('persistent-token');
  });

  // 3. Submit button in ConfirmDialog stays disabled until name matches exactly.
  it('blocks submit unless ConfirmDialog typed name matches', async () => {
    render(<HFFlows />);
    switchToNewToken();

    fillStep1('exact-name');
    clickNext(); // step 2
    clickNext(); // step 3 review

    fireEvent.click(screen.getByTestId('newtoken-open-confirm'));

    const input = screen.getByRole('textbox');

    // Wrong name — confirm button must be disabled.
    fireEvent.change(input, { target: { value: 'wrong-name' } });
    expect(screen.getByTestId('confirm-dialog-confirm')).toBeDisabled();
    expect(API.post).not.toHaveBeenCalled();

    // Partial match — still disabled.
    fireEvent.change(input, { target: { value: 'exact' } });
    expect(screen.getByTestId('confirm-dialog-confirm')).toBeDisabled();
    expect(API.post).not.toHaveBeenCalled();
  });
});

describe('Flows page — no WIP banners, no dead tabs', () => {
  it('renders zero WIPBanners and exactly the newChannel/newToken tabs', () => {
    render(<HFFlows />);

    // The old copy every WIPBanner rendered ("… wired in v3.") must be gone —
    // that was the false "content is design-mock" claim this lane removes.
    expect(document.body.textContent).not.toMatch(/wired in v3/);
    expect(document.body.textContent).not.toMatch(/WORK IN PROGRESS/);

    // Only two tabs remain — incident and retry were deleted, not hidden.
    expect(screen.getByTestId('flows-tab-newChannel')).toBeTruthy();
    expect(screen.getByTestId('flows-tab-newToken')).toBeTruthy();
    expect(screen.queryByTestId('flows-tab-incident')).toBeNull();
    expect(screen.queryByTestId('flows-tab-retry')).toBeNull();
    expect(screen.getAllByTestId(/^flows-tab-/)).toHaveLength(2);
  });

  it('passes its own HFShell nav item id, not the Channels page id', () => {
    render(<HFFlows />);
    // HFShell's nav registers a real 'flows' item (components/hifi/HFShell.jsx);
    // this page must highlight that item rather than 'channels'.
    expect(screen.getByTestId('hf-shell').getAttribute('data-active')).toBe(
      'flows',
    );
  });
});

describe('Flows page — newChannel wizard (real handler-chain contract)', () => {
  // The request/response shapes asserted below are not invented — they are
  // copied from the real gin-router-driven backend tests that already prove
  // this contract against the actual handlers:
  //   create success  -> internal/adapter/handler/v2_channel_test.go:121 (TestCreateChannelV2_Success)
  //   create 403      -> internal/adapter/handler/channel_sensitive_write_test.go:741 (TestCreateChannelV2_NonRootAdminWithoutGrant403)
  //   create 400      -> internal/adapter/handler/v2_channel_test.go:153 (TestCreateChannelV2_NameTooLong)
  //   discovery       -> internal/adapter/handler/v2_channel_actions_test.go:131 (TestV2UpstreamModels_Happy)
  //   test connection -> internal/adapter/handler/v2_channel_actions_test.go:67/248 (TestV2ChannelTest_Happy / ResponseShape)

  it('creates a channel with the exact payload CreateChannelV2 parses, vendor type included', async () => {
    API.post.mockResolvedValueOnce({
      data: {
        success: true,
        message: 'Channel created successfully',
        data: { id: 42, name: 'openai/main-2' },
      },
    });

    render(<HFFlows />);
    // newChannel is the default tab and default step (vendor pick).
    fireEvent.click(screen.getByTestId('newchannel-vendor-anthropic'));
    goToChannelCredentials();

    fillChannelCredentials({
      name: 'openai/main-2',
      key: 'sk-test-key-12345',
      models: 'claude-3-opus,claude-3-sonnet',
      orgId: 'org-acme-prod',
    });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/acme/channels');
    expect(body.name).toBe('openai/main-2');
    expect(body.key).toBe('sk-test-key-12345');
    expect(body.models).toBe('claude-3-opus,claude-3-sonnet');
    // Selecting the Anthropic vendor card set type=14 (constants/channel.constants.js).
    expect(body.type).toBe(14);
    // Anthropic's base_url preset prefilled (CHANNEL_PRESETS[14].base_url).
    expect(body.base_url).toBe('https://api.anthropic.com');
    expect(body.openai_organization).toBe('org-acme-prod');

    // Success moves the wizard to step 3 — the create button is gone.
    await waitFor(() => {
      expect(screen.queryByTestId('newchannel-create-btn')).toBeNull();
      expect(screen.getByTestId('newchannel-discover-btn')).toBeTruthy();
    });

    // Going back to step 2 shows the persisted-channel note and locked fields.
    fireEvent.click(screen.getByTestId('flows-back'));
    expect(screen.getByTestId('newchannel-created-note').textContent).toMatch(
      /42/,
    );
    expect(screen.getByTestId('newchannel-name')).toBeDisabled();
  });

  it('omits openai_organization when the field is left blank', async () => {
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 1, name: 'openai/main' } },
    });
    render(<HFFlows />);
    goToChannelCredentials();
    fillChannelCredentials({ orgId: '' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const [, body] = API.post.mock.calls[0];
    expect(body.openai_organization).toBeUndefined();
  });

  it('rejects a multi-line key with an inline error and never calls API.post', async () => {
    render(<HFFlows />);
    goToChannelCredentials();
    // A pasted multi-key blob: no leading/trailing whitespace (so .trim()
    // would not catch it), just an internal newline.
    fillChannelCredentials({ key: 'sk-key-one\nsk-key-two' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('newchannel-create-error').textContent).toMatch(
        /one key per channel/i,
      );
    });
    expect(API.post).not.toHaveBeenCalled();
    // Stays on step 2 — no channel was created.
    expect(screen.getByTestId('newchannel-create-btn')).toBeTruthy();
  });

  it('blocks create for a vendor with no default upstream host when base url is left blank', async () => {
    render(<HFFlows />);
    // 'custom' (type 8) has no entry in CHANNEL_PRESETS and no backend
    // default in constant.ChannelBaseURLs.
    fireEvent.click(screen.getByTestId('newchannel-vendor-custom'));
    goToChannelCredentials();
    fillChannelCredentials({ baseURL: '' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('newchannel-create-error').textContent).toMatch(
        /no default upstream host/i,
      );
    });
    expect(API.post).not.toHaveBeenCalled();
  });

  it('re-picking a vendor overwrites a preset-derived base url from the previous vendor', async () => {
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 7, name: 'zhipu/main' } },
    });
    render(<HFFlows />);
    // First pick Anthropic (prefills base_url from its preset), then change
    // to Zhipu without ever typing into the base url field ourselves.
    fireEvent.click(screen.getByTestId('newchannel-vendor-anthropic'));
    fireEvent.click(screen.getByTestId('newchannel-vendor-zhipu'));
    goToChannelCredentials();
    fillChannelCredentials({ baseURL: '' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const [, body] = API.post.mock.calls[0];
    expect(body.type).toBe(26); // Zhipu (constants/channel.constants.js)
    // Zhipu's own preset, not left over from the earlier Anthropic click.
    expect(body.base_url).toBe('https://open.bigmodel.cn');
  });

  it('keeps a user-typed base url across a later vendor pick', async () => {
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 8, name: 'zhipu/proxy' } },
    });
    render(<HFFlows />);
    fireEvent.click(screen.getByTestId('newchannel-vendor-anthropic'));
    goToChannelCredentials();
    fireEvent.change(screen.getByTestId('newchannel-baseurl'), {
      target: { value: 'https://my-proxy.internal' },
    });
    fireEvent.click(screen.getByTestId('flows-back'));
    fireEvent.click(screen.getByTestId('newchannel-vendor-zhipu'));
    goToChannelCredentials();
    fillChannelCredentials({ baseURL: '' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const [, body] = API.post.mock.calls[0];
    expect(body.base_url).toBe('https://my-proxy.internal');
  });

  it('surfaces a 403 PERMISSION_DENIED create response naming the missing grant', async () => {
    API.post.mockRejectedValueOnce({
      response: {
        status: 403,
        data: {
          success: false,
          message: 'insufficient permission',
          error_code: 'PERMISSION_DENIED',
        },
      },
    });

    render(<HFFlows />);
    goToChannelCredentials();
    fillChannelCredentials();
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('newchannel-create-error').textContent).toMatch(
        /channel:sensitive_write/,
      );
    });
    // A generic "insufficient permission" alone (the raw backend message)
    // does not name the grant — the page must say more than that.
    expect(screen.getByTestId('newchannel-create-error').textContent).not.toBe(
      'insufficient permission',
    );
    // Stays on step 2 — no channel was created.
    expect(screen.getByTestId('newchannel-create-btn')).toBeTruthy();
  });

  it('surfaces a generic 400 validation failure using the real backend message', async () => {
    API.post.mockRejectedValueOnce({
      response: {
        status: 400,
        data: {
          success: false,
          message: 'Channel name too long (max 100 characters)',
        },
      },
    });

    render(<HFFlows />);
    goToChannelCredentials();
    fillChannelCredentials();
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('newchannel-create-error').textContent).toBe(
        'Channel name too long (max 100 characters)',
      );
    });
  });

  // Helper: drive the wizard through a successful create so step 3/4 tests
  // start from a real createdChannel id, exactly like a real submit would.
  async function createChannelAndReachStep3() {
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 99, name: 'openai/main-2' } },
    });
    render(<HFFlows />);
    goToChannelCredentials();
    fillChannelCredentials({ models: 'gpt-4' });
    fireEvent.click(screen.getByTestId('newchannel-create-btn'));
    await waitFor(() =>
      expect(screen.getByTestId('newchannel-discover-btn')).toBeTruthy(),
    );
  }

  it('discovers upstream models and PUTs the merged model list on apply', async () => {
    await createChannelAndReachStep3();

    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: { upstream: ['gpt-4', 'gpt-4o'], new: ['gpt-4o'], missing: [] },
      },
    });
    fireEvent.click(screen.getByTestId('newchannel-discover-btn'));

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    expect(API.get.mock.calls[0][0]).toBe(
      '/api/v2/acme/channels/99/upstream-models',
    );

    // The "new" model is pre-selected and shown with a checkbox; the
    // already-configured one has no checkbox (nothing to add).
    await waitFor(() => {
      expect(screen.getByTestId('newchannel-model-gpt-4o')).toBeTruthy();
    });
    expect(screen.getByTestId('newchannel-model-gpt-4o').checked).toBe(true);
    expect(screen.queryByTestId('newchannel-model-gpt-4')).toBeNull();

    API.put.mockResolvedValueOnce({ data: { success: true } });
    fireEvent.click(screen.getByTestId('newchannel-apply-models-btn'));

    await waitFor(() => expect(API.put).toHaveBeenCalledTimes(1));
    const [url, body] = API.put.mock.calls[0];
    expect(url).toBe('/api/v2/acme/channels/99');
    // Merge of the channel's original models ('gpt-4', set at create) with
    // the selected new model ('gpt-4o') — not a blind overwrite.
    expect(body.models).toBe('gpt-4,gpt-4o');

    // Lands on step 4 (review) — the test-channel button is there.
    await waitFor(() => {
      expect(screen.getByTestId('newchannel-test-btn')).toBeTruthy();
    });
  });

  it('skip-models button reaches step 4 without calling PUT', async () => {
    await createChannelAndReachStep3();
    fireEvent.click(screen.getByTestId('newchannel-skip-models-btn'));
    await waitFor(() =>
      expect(screen.getByTestId('newchannel-test-btn')).toBeTruthy(),
    );
    expect(API.put).not.toHaveBeenCalled();
  });

  it('tests the created channel: success shows latency, failure shows the error', async () => {
    await createChannelAndReachStep3();
    fireEvent.click(screen.getByTestId('newchannel-skip-models-btn'));
    await waitFor(() =>
      expect(screen.getByTestId('newchannel-test-btn')).toBeTruthy(),
    );

    API.post.mockResolvedValueOnce({ data: { success: true, latency_ms: 88 } });
    fireEvent.click(screen.getByTestId('newchannel-test-btn'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledWith(
        '/api/v2/acme/channels/99/test',
        {},
      );
      expect(screen.getByTestId('newchannel-test-result').textContent).toMatch(
        /88ms/,
      );
    });

    // TestChannelV2's own failure path returns HTTP 200 with success:false
    // (v2_channel_actions.go:159/178/188) — not a rejected promise.
    API.post.mockResolvedValueOnce({
      data: {
        success: false,
        latency_ms: 12,
        error: 'upstream returned HTTP 500',
      },
    });
    fireEvent.click(screen.getByTestId('newchannel-test-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('newchannel-test-result').textContent).toMatch(
        /upstream returned HTTP 500/,
      );
    });
  });
});
