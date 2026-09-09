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

// Mock helpers BEFORE importing the component — vi.mock is hoisted but the
// component module reads `API.post` at runtime so the mocks resolve at first
// call, not at import.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

// Mock navigator.clipboard so share tests don't fail in jsdom.
Object.defineProperty(navigator, 'clipboard', {
  value: { writeText: vi.fn().mockResolvedValue(undefined) },
  configurable: true,
});

// Mock window.prompt so save tests can supply a preset name.
vi.stubGlobal('prompt', vi.fn());

// HFShell pulls TenantSwitcher → API helper chain → react-router. Stub it
// to a passthrough wrapper so the test focuses on Playground UI/logic.
vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

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

import HFPlayground from './index';
import { API, showError, showSuccess } from '../../../helpers';

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  window.prompt.mockReset();
  navigator.clipboard.writeText.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('user', JSON.stringify({ id: 'pg-tester' }));
  window.localStorage.setItem('tenant_slug', 'acme');
  // Reset URL to avoid cross-test URL pollution from share tests.
  window.history.replaceState({}, '', '/playground');
});

afterEach(() => {
  vi.useRealTimers();
});

const fakeRunResponse = (items) => ({
  data: { success: true, data: { items } },
});

describe('Playground page', () => {
  // 1. system / user are editable inputs (no longer the readOnly spans from
  //    the design-mock era). Typing reflects in the controlled value.
  it('renders editable system + user textareas', () => {
    render(<HFPlayground />);
    const sys = screen.getByTestId('playground-system');
    const usr = screen.getByTestId('playground-user');
    expect(sys.tagName).toBe('TEXTAREA');
    expect(usr.tagName).toBe('TEXTAREA');

    fireEvent.change(sys, { target: { value: 'be terse' } });
    expect(sys.value).toBe('be terse');

    fireEvent.change(usr, { target: { value: 'list 3 primes' } });
    expect(usr.value).toBe('list 3 primes');
  });

  // 2. Click "run all 3" → POST /api/v2/acme/playground/run with current
  //    form state; response items render into 3 columns by index order.
  it('runs fan-out and renders 3 columns', async () => {
    API.post.mockResolvedValueOnce(
      fakeRunResponse([
        {
          model: 'gpt-4o',
          content: 'four',
          latency_ms: 100,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'claude-3.5-sonnet',
          content: 'four.',
          latency_ms: 80,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'gemini-1.5-pro',
          content: '2+2=4',
          latency_ms: 120,
          prompt_tokens: 5,
          completion_tokens: 3,
        },
      ]),
    );

    render(<HFPlayground />);
    fireEvent.click(screen.getByTestId('playground-run'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/acme/playground/run');
    expect(body.models).toEqual([
      'gpt-4o',
      'claude-3.5-sonnet',
      'gemini-1.5-pro',
    ]);
    expect(body.user).toMatch(/capital of Australia/);

    await waitFor(() => {
      expect(screen.getByTestId('playground-col-0').textContent).toContain(
        'four',
      );
      expect(screen.getByTestId('playground-col-1').textContent).toContain(
        'four.',
      );
      expect(screen.getByTestId('playground-col-2').textContent).toContain(
        '2+2=4',
      );
    });
  });

  // 3. Per-column error renders in red with the error code + message —
  //    other columns still show content.
  it('renders per-column errors without blocking other columns', async () => {
    API.post.mockResolvedValueOnce(
      fakeRunResponse([
        {
          model: 'gpt-4o',
          content: 'ok',
          latency_ms: 100,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'claude-3.5-sonnet',
          content: '',
          latency_ms: 200,
          prompt_tokens: 0,
          completion_tokens: 0,
          error_code: 'UPSTREAM_ERROR',
          error_message: 'channel exhausted',
        },
        {
          model: 'gemini-1.5-pro',
          content: 'fine',
          latency_ms: 150,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
      ]),
    );

    render(<HFPlayground />);
    fireEvent.click(screen.getByTestId('playground-run'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-col-1').textContent).toContain(
        'UPSTREAM_ERROR',
      );
      expect(screen.getByTestId('playground-col-1').textContent).toContain(
        'channel exhausted',
      );
      expect(screen.getByTestId('playground-col-0').textContent).toContain(
        'ok',
      );
      expect(screen.getByTestId('playground-col-2').textContent).toContain(
        'fine',
      );
    });
  });

  // 4. Empty user prompt → showError, no POST fired (cheap client-side
  //    guard avoids a server 400 roundtrip for the most common mistake).
  it('refuses to run with empty user prompt', () => {
    render(<HFPlayground />);
    fireEvent.change(screen.getByTestId('playground-user'), {
      target: { value: '   ' },
    });
    fireEvent.click(screen.getByTestId('playground-run'));
    expect(showError).toHaveBeenCalledWith('User prompt cannot be empty');
    expect(API.post).not.toHaveBeenCalled();
  });

  // 5. Run button shows loading state during pending POST + button is
  //    disabled (defense against double-fire while server is working).
  it('disables run button + shows loading text while pending', async () => {
    let resolveFn;
    const pending = new Promise((res) => (resolveFn = res));
    API.post.mockReturnValueOnce(pending);

    render(<HFPlayground />);
    const btn = screen.getByTestId('playground-run');
    fireEvent.click(btn);

    await waitFor(() => {
      expect(btn).toBeDisabled();
      expect(btn.textContent).toMatch(/running/);
    });

    resolveFn(fakeRunResponse([]));
    await waitFor(() => {
      expect(btn).not.toBeDisabled();
    });
  });

  // 6. POST failure (network / 500) surfaces via showError with the
  //    message from the response body, button re-enabled for retry.
  it('shows error on POST failure and re-enables the button', async () => {
    API.post.mockRejectedValueOnce({
      response: { data: { message: 'service unavailable' } },
    });

    render(<HFPlayground />);
    fireEvent.click(screen.getByTestId('playground-run'));

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith('service unavailable');
      expect(screen.getByTestId('playground-run')).not.toBeDisabled();
    });
  });

  // 7. Params (temperature / top_p / max_tokens) round-trip from input to
  //    request body — type coercion + min/max enforcement on max_tokens.
  it('round-trips params into the request body with type coercion', async () => {
    API.post.mockResolvedValueOnce(fakeRunResponse([]));

    render(<HFPlayground />);
    fireEvent.change(screen.getByTestId('playground-temp'), {
      target: { value: '0.3' },
    });
    fireEvent.change(screen.getByTestId('playground-topp'), {
      target: { value: '0.9' },
    });
    fireEvent.change(screen.getByTestId('playground-max'), {
      target: { value: '256' },
    });
    fireEvent.click(screen.getByTestId('playground-run'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
    const [, body] = API.post.mock.calls[0];
    expect(body.params.temperature).toBe(0.3);
    expect(body.params.top_p).toBe(0.9);
    expect(body.params.max_tokens).toBe(256);
  });

  // 8. save — window.prompt returns a name → POST /playground/presets,
  //    showSuccess called on success.
  it('save: posts preset and shows success toast', async () => {
    window.prompt.mockReturnValueOnce('my-save');
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { id: 1, name: 'my-save' } },
    });

    render(<HFPlayground />);
    fireEvent.click(screen.getByTestId('playground-save-btn'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/acme/playground/presets');
    expect(body.name).toBe('my-save');
    await waitFor(() => {
      expect(showSuccess).toHaveBeenCalledWith('Preset saved');
    });
  });

  // 9. blank▾ — opens dropdown, click on a preset item loads it into the
  //    user textarea.
  it('blank: loads preset prompt into user textarea', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: [
          {
            id: 42,
            name: 'test-preset',
            prompt: 'loaded-prompt',
            models: '["gpt-4o"]',
            params: '{"temperature":0.2,"top_p":0.8,"max_tokens":512}',
          },
        ],
      },
    });

    render(<HFPlayground />);
    fireEvent.click(screen.getByTestId('playground-blank-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-blank-dropdown')).toBeTruthy();
    });

    fireEvent.click(screen.getByTestId('playground-preset-item-42'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-user').value).toBe('loaded-prompt');
    });
  });

  // 10. share — copies URL containing current prompt/models/params as query
  //     params to clipboard; showSuccess toast fires.
  it('share: copies URL with form state to clipboard', async () => {
    render(<HFPlayground />);

    // Set a known user prompt so we can assert it appears in the URL.
    fireEvent.change(screen.getByTestId('playground-user'), {
      target: { value: 'share-me' },
    });

    fireEvent.click(screen.getByTestId('playground-share-btn'));

    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1);
    });
    const copiedURL = navigator.clipboard.writeText.mock.calls[0][0];
    expect(copiedURL).toContain('prompt=');
    expect(copiedURL).toContain(encodeURIComponent('share-me'));
    expect(copiedURL).toContain('models=');
    expect(showSuccess).toHaveBeenCalledWith('Share link copied');
  });

  // 11. swap▾ — opens model dropdown, clicking a model toggles it in/out of
  //     form.models. Model already present → removed; absent → appended.
  it('swap: toggles model in the models array', async () => {
    // Real wire shape from GET /api/v2/:tenant_slug/models
    // (v2_models.go:108-116): data is an object with an `items` array, never
    // the array itself.
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [
            { model_name: 'gpt-4o' },
            { model_name: 'claude-3.5-sonnet' },
            { model_name: 'gemini-1.5-pro' },
            { model_name: 'o1-mini' },
          ],
        },
      },
    });

    render(<HFPlayground />);

    // Open swap dropdown for column 0.
    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-dropdown-0')).toBeTruthy();
    });

    // Click o1-mini (not in default models) to add it — the request is async,
    // so the button only appears once the hook resolves.
    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-model-o1-mini')).toBeTruthy();
    });
    fireEvent.click(screen.getByTestId('playground-swap-model-o1-mini'));

    // After clicking, the model count in the header should have increased.
    await waitFor(() => {
      expect(screen.getByText(/4 models, one prompt/i)).toBeTruthy();
    });
  });

  // 12. swap▾ with a tenant that has no models — the loading text must not
  //     linger forever (that was the original bug); an honest "no models
  //     available" line replaces it once the fetch resolves empty.
  it('swap: shows "no models available" once loaded with an empty catalog', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: { items: [] } },
    });

    render(<HFPlayground />);

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-dropdown-0')).toBeTruthy();
    });

    await waitFor(() => {
      expect(screen.getByText('no models available')).toBeTruthy();
    });
    expect(screen.queryByText('loading…')).toBeNull();
  });

  // Lock for the loading block itself (Playground/index.jsx:757-767) — while
  // the request is in flight the dropdown must show the loading line, not
  // jump straight to the empty state.
  it('swap: shows the loading line while the models request is in flight', async () => {
    let resolveGet;
    API.get.mockReturnValueOnce(
      new Promise((r) => {
        resolveGet = r;
      }),
    );

    render(<HFPlayground />);

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-loading')).toBeTruthy();
    });
    expect(screen.queryByTestId('playground-swap-empty')).toBeNull();

    resolveGet({ data: { success: true, data: { items: [] } } });
    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-empty')).toBeTruthy();
    });
  });

  // Lock for the .filter(Boolean) on availableModels (Playground/index.jsx
  // ~130) — a catalogue entry with no model_name must not render a blank
  // swap row.
  it('swap: drops a catalogue entry with no model_name instead of rendering a blank row', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [
            { model_name: 'gpt-4o' },
            { vendor: 'OpenAI' },
            { model_name: '' },
          ],
        },
      },
    });

    render(<HFPlayground />);

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-model-gpt-4o')).toBeTruthy();
    });
    const dropdown = screen.getByTestId('playground-swap-dropdown-0');
    // Exactly one row rendered — the entry with no name and the entry with
    // an empty-string name are both dropped, not rendered as blank buttons.
    expect(dropdown.querySelectorAll('button').length).toBe(1);
  });

  // Lock for the distinct failure line (item 2) — a failed fetch must not
  // read as "no models available".
  it('swap: shows a failure line, not the empty-catalog line, when the fetch fails', async () => {
    API.get.mockRejectedValueOnce(new Error('network down'));

    render(<HFPlayground />);

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-error')).toBeTruthy();
    });
    expect(screen.queryByTestId('playground-swap-empty')).toBeNull();
    expect(screen.queryByText('no models available')).toBeNull();
  });
});
