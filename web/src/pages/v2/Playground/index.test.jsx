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

// Neutral fixture ids (this cycle's convention, not vendor model names) —
// three by default so defaultCompareModels(routableModels) (max=3) fills
// the compare draft exactly like the old DEFAULT_MODELS literal used to,
// without hardcoding a vendor name anywhere in this file.
const ROUTABLE_DEFAULT = [
  {
    id: 'rt-alpha',
    owned_by: 'Vendor A',
    supported_endpoint_types: ['openai'],
  },
  { id: 'rt-beta', owned_by: 'Vendor B', supported_endpoint_types: ['openai'] },
  {
    id: 'rt-gamma',
    owned_by: 'Vendor C',
    supported_endpoint_types: ['openai'],
  },
];

const routableResponse = (items = ROUTABLE_DEFAULT) => ({
  data: { success: true, data: { items } },
});

// GET now serves two concerns (routable models AND presets/list-load) from
// the same mocked function — route by URL, not call order, since
// useRoutableModels' effect fires on every mount regardless of what a given
// test is trying to exercise. routableItemsOrFn lets a test override or
// react dynamically (e.g. count calls) to the routable response while
// keeping the rest of otherHandler's routing untouched.
const wireGet = (otherHandler, routableItemsOrFn = ROUTABLE_DEFAULT) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/models/routable')) {
      const items =
        typeof routableItemsOrFn === 'function'
          ? routableItemsOrFn(url)
          : routableItemsOrFn;
      return Promise.resolve(routableResponse(items));
    }
    return otherHandler
      ? otherHandler(url)
      : Promise.reject(new Error(`unexpected GET ${url}`));
  });
};

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
  // Default: the routable list resolves to the 3-item fixture above; any
  // other GET (presets) 404s until a test wires its own.
  wireGet();
});

afterEach(() => {
  vi.useRealTimers();
});

const fakeRunResponse = (items) => ({
  data: { success: true, data: { items } },
});

// The compare draft is now populated asynchronously (routable-draft-sync
// effect), where it used to be the DEFAULT_MODELS literal available at
// first render — tests that click run (or read the model count) right
// after render() must wait for that effect to land first.
const waitForModelsReady = () =>
  waitFor(() =>
    expect(screen.getByTestId('playground-run')).not.toBeDisabled(),
  );

// useFormDraft reads localStorage SYNCHRONOUSLY on mount (its useState
// initializer), so seeding a valid envelope here lets a test render with a
// non-empty form.models before the routable fetch has resolved — the only
// way to have a compare column exist while that fetch is still pending.
// Mirrors useFormDraft's own storage layout (buildFullKey + envelope).
const seedPlaygroundDraft = (models = ['rt-alpha', 'rt-beta', 'rt-gamma']) => {
  window.localStorage.setItem(
    'lurus-hub:draft:pg-tester:playground-form',
    JSON.stringify({
      v: 1,
      t: Date.now(),
      d: {
        system: 'You are a helpful, concise assistant.',
        user: 'What is the capital of Australia? Briefly.',
        temperature: 0.7,
        top_p: 1.0,
        max_tokens: 1024,
        models,
      },
    }),
  );
};

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

  // 2. Click "run all 3" → POST /api/v2/~/playground/run with current
  //    form state; response items render into 3 columns by index order.
  it('runs fan-out and renders 3 columns', async () => {
    API.post.mockResolvedValueOnce(
      fakeRunResponse([
        {
          model: 'rt-alpha',
          content: 'four',
          latency_ms: 100,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'rt-beta',
          content: 'four.',
          latency_ms: 80,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'rt-gamma',
          content: '2+2=4',
          latency_ms: 120,
          prompt_tokens: 5,
          completion_tokens: 3,
        },
      ]),
    );

    render(<HFPlayground />);
    await waitForModelsReady();
    fireEvent.click(screen.getByTestId('playground-run'));

    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
    const [url, body] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/~/playground/run');
    expect(body.models).toEqual(['rt-alpha', 'rt-beta', 'rt-gamma']);
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
          model: 'rt-alpha',
          content: 'ok',
          latency_ms: 100,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
        {
          model: 'rt-beta',
          content: '',
          latency_ms: 200,
          prompt_tokens: 0,
          completion_tokens: 0,
          error_code: 'UPSTREAM_ERROR',
          error_message: 'channel exhausted',
        },
        {
          model: 'rt-gamma',
          content: 'fine',
          latency_ms: 150,
          prompt_tokens: 5,
          completion_tokens: 1,
        },
      ]),
    );

    render(<HFPlayground />);
    await waitForModelsReady();
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
  it('refuses to run with empty user prompt', async () => {
    render(<HFPlayground />);
    await waitForModelsReady();
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
    await waitForModelsReady();
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
    await waitForModelsReady();
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
    await waitForModelsReady();
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
    expect(url).toBe('/api/v2/~/playground/presets');
    expect(body.name).toBe('my-save');
    await waitFor(() => {
      expect(showSuccess).toHaveBeenCalledWith('Preset saved');
    });
  });

  // 9. blank▾ — opens dropdown, click on a preset item loads it into the
  //    user textarea.
  it('blank: loads preset prompt into user textarea', async () => {
    // wireGet (not mockResolvedValueOnce): useRoutableModels' effect also
    // fires a GET on mount, so a blind "next call" override could answer
    // either one — route by URL.
    wireGet((url) => {
      if (String(url).includes('/playground/presets')) {
        return Promise.resolve({
          data: {
            success: true,
            data: [
              {
                id: 42,
                name: 'test-preset',
                prompt: 'loaded-prompt',
                models: '["rt-alpha"]',
                params: '{"temperature":0.2,"top_p":0.8,"max_tokens":512}',
              },
            ],
          },
        });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    render(<HFPlayground />);
    await waitForModelsReady();
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
    // Real wire shape from GET .../models/routable
    // (v2_models_routable.go): data is an object with an `items` array,
    // never the array itself.
    wireGet(undefined, [
      ...ROUTABLE_DEFAULT,
      {
        id: 'rt-delta',
        owned_by: 'Vendor D',
        supported_endpoint_types: ['openai'],
      },
    ]);

    render(<HFPlayground />);
    await waitForModelsReady();

    // Open swap dropdown for column 0.
    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-dropdown-0')).toBeTruthy();
    });

    // Click rt-delta (not in the default 3-model draft) to add it.
    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-model-rt-delta')).toBeTruthy();
    });
    fireEvent.click(screen.getByTestId('playground-swap-model-rt-delta'));

    // After clicking, the model count in the header should have increased.
    await waitFor(() => {
      expect(screen.getByText(/4 models, one prompt/i)).toBeTruthy();
    });
  });

  // 12a. Genuinely zero routable models: no columns render at all, and the
  //     TOP-LEVEL "no models available" banner is what a real customer with
  //     nothing routable sees (there is no per-column swap▾ to open when
  //     there are zero columns).
  it('shows the top-level "no models available" banner with a genuinely empty catalog', async () => {
    wireGet(undefined, []);

    render(<HFPlayground />);

    await waitFor(() =>
      expect(screen.getByTestId('playground-no-models')).toBeTruthy(),
    );
    expect(screen.queryByTestId('playground-swap-btn-0')).toBeNull();
    expect(screen.getByTestId('playground-run')).toBeDisabled();
  });

  // 12b. The per-column swap▾ dropdown's OWN empty state is still real
  //     code — it fires when a column exists (routableModels is non-empty)
  //     but every entry's id filters out as falsy (a malformed backend
  //     row), which is a different case from "nothing routable at all".
  it('swap: shows "no models available" inside the dropdown when every routable id is blank', async () => {
    wireGet(undefined, [
      { id: '', owned_by: 'Vendor Unknown', supported_endpoint_types: [] },
    ]);

    render(<HFPlayground />);
    await waitFor(() =>
      expect(screen.getByTestId('playground-swap-btn-0')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-dropdown-0')).toBeTruthy();
    });

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-empty')).toBeTruthy();
    });
    expect(screen.queryByText('loading…')).toBeNull();
  });

  // Lock for the loading block itself — while the routable fetch is still
  // in flight the dropdown must show the loading line, not jump straight
  // to the empty state (the fetch now starts on MOUNT, not on swap▾'s
  // first click, but the dropdown's own loading/error/empty rendering is
  // unchanged — it just reads the same hook state).
  it('swap: shows the loading line while the models request is in flight', async () => {
    // Columns only exist once form.models is non-empty, and the draft
    // starts empty until the routable fetch resolves — seed a restored
    // draft (useFormDraft reads localStorage synchronously on mount, see
    // seedPlaygroundDraft) so column 0 exists WHILE the fetch is still
    // pending, which is the only way to open its swap▾ before resolution.
    seedPlaygroundDraft();
    let resolveGet;
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return new Promise((r) => {
          resolveGet = r;
        });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    render(<HFPlayground />);

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-loading')).toBeTruthy();
    });
    expect(screen.queryByTestId('playground-swap-empty')).toBeNull();

    // Resolving to an empty routable list clears the draft (routable-
    // draft-sync effect) — the column itself disappears and the TOP-LEVEL
    // "no models available" banner is what replaces it, not a per-column
    // empty state inside a dropdown that no longer has a column to belong
    // to (see the 12a/12b split above for why).
    resolveGet(routableResponse([]));
    await waitFor(() => {
      expect(screen.getByTestId('playground-no-models')).toBeTruthy();
    });
    expect(screen.queryByTestId('playground-swap-btn-0')).toBeNull();
  });

  // Lock for the .filter(Boolean) on availableModels — a routable entry
  // with an empty id must not render a blank swap row.
  it('swap: drops a routable entry with no id instead of rendering a blank row', async () => {
    wireGet(undefined, [
      {
        id: 'rt-alpha',
        owned_by: 'Vendor A',
        supported_endpoint_types: ['openai'],
      },
      { id: '', owned_by: 'Vendor Unknown', supported_endpoint_types: [] },
    ]);

    render(<HFPlayground />);
    await waitForModelsReady();

    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));

    await waitFor(() => {
      expect(screen.getByTestId('playground-swap-model-rt-alpha')).toBeTruthy();
    });
    const dropdown = screen.getByTestId('playground-swap-dropdown-0');
    // Exactly one row rendered — the entry with the empty id is dropped,
    // not rendered as a blank button.
    expect(dropdown.querySelectorAll('button').length).toBe(1);
  });

  // Lock for the distinct top-level failure banner — a failed routable
  // fetch must not read as "no models available" (that phrase is reserved
  // for a resolved, genuinely empty catalogue). With zero models resolved
  // there are also zero compare columns, so there is no swap▾ to open —
  // the page-level banner is the only signal available.
  it('shows a failure banner, not the empty-catalog banner, when the routable fetch fails', async () => {
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.reject(new Error('network down'));
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    render(<HFPlayground />);

    await waitFor(() => {
      expect(screen.getByTestId('playground-models-error')).toBeTruthy();
    });
    expect(screen.queryByTestId('playground-no-models')).toBeNull();
    expect(screen.queryByText('no models available')).toBeNull();
    expect(screen.queryByTestId('playground-swap-btn-0')).toBeNull();
  });

  // L1 (cycle-11) — explicit locks for the mutation list:

  // "first three routable by default": a fresh visitor with no draft gets
  // exactly the first three routable models, not a literal list.
  it('defaults the compare draft to the first three routable models, not a literal', async () => {
    wireGet(undefined, [
      ...ROUTABLE_DEFAULT,
      {
        id: 'rt-delta',
        owned_by: 'Vendor D',
        supported_endpoint_types: ['openai'],
      },
    ]);

    render(<HFPlayground />);
    await waitForModelsReady();

    expect(screen.getByText(/3 models, one prompt/i)).toBeTruthy();
    // The three columns are the routable fixture ids, not a literal list.
    expect(document.body.textContent).toContain('rt-alpha');
    expect(document.body.textContent).toContain('rt-beta');
    expect(document.body.textContent).toContain('rt-gamma');
    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));
    await waitFor(() =>
      expect(screen.getByTestId('playground-swap-dropdown-0')).toBeTruthy(),
    );
    // rt-delta (the 4th routable model) is available to swap IN — proving
    // it was excluded from the initial draft, not merely absent from the
    // catalogue.
    expect(screen.getByTestId('playground-swap-model-rt-delta')).toBeTruthy();
  });

  // Before the routable fetch resolves, the compare draft must be empty
  // and run disabled — the pre-resolve window is exactly where a literal
  // DEFAULT_FORM.models list would be observable and runnable, and is the
  // window the previous test could not see because it always waited for
  // the fetch to resolve first.
  it('shows an empty compare draft and disables run before the routable list resolves', async () => {
    // Never resolves — pins the component in the pre-resolve state for the
    // life of the test.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return new Promise(() => {});
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    render(<HFPlayground />);

    const runBtn = screen.getByTestId('playground-run');
    // The count baked into the button's own label is the most direct
    // oracle for form.models.length — a literal DEFAULT_FORM.models would
    // show "run all 3" here even though nothing has resolved yet.
    expect(runBtn.textContent).toContain('run all 0');
    expect(runBtn.disabled).toBe(true);
    fireEvent.click(runBtn);
    expect(API.post).not.toHaveBeenCalled();
  });

  // "reads ?prefill_model=": the Models page's "try ↗" link.
  it('reads ?prefill_model= from the URL and adopts it as the sole compare draft', async () => {
    window.history.replaceState({}, '', '/playground?prefill_model=rt-beta');

    render(<HFPlayground />);
    await waitForModelsReady();

    expect(screen.getByText(/1 models, one prompt/i)).toBeTruthy();
    // The single column's model select/label is rt-beta — verified via the
    // swap dropdown's checkmark rather than the column header text (which
    // also appears in the run-request body assertion below).
    fireEvent.click(screen.getByTestId('playground-swap-btn-0'));
    await waitFor(() =>
      expect(
        screen.getByTestId('playground-swap-model-rt-beta').textContent,
      ).toContain('✓'),
    );
  });

  // "drops non-routable draft models": a restored draft naming a model
  // that is no longer routable must lose only that model, keeping the
  // ones that ARE still routable.
  it('drops a non-routable model from a restored draft, keeping the routable ones', async () => {
    seedPlaygroundDraft(['rt-alpha', 'rt-zzz-gone', 'rt-beta']);

    render(<HFPlayground />);
    await waitForModelsReady();

    expect(screen.getByText(/2 models, one prompt/i)).toBeTruthy();
    const [, body] = await (async () => {
      fireEvent.click(screen.getByTestId('playground-run'));
      await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
      return API.post.mock.calls[0];
    })();
    expect(body.models).toEqual(['rt-alpha', 'rt-beta']);
  });

  // "if that empties the draft take the first three": every draft model is
  // stale — the draft falls back to defaultCompareModels, not an empty
  // grid.
  it('falls back to the first three routable models when every draft model is stale', async () => {
    seedPlaygroundDraft(['rt-zzz-gone-1', 'rt-zzz-gone-2']);

    render(<HFPlayground />);
    await waitForModelsReady();

    expect(screen.getByText(/3 models, one prompt/i)).toBeTruthy();
  });

  // "refuses to run when nothing routable": Run stays disabled and a click
  // (even via keyboard shortcut) does not POST.
  it('refuses to run when nothing is routable', async () => {
    wireGet(undefined, []);

    render(<HFPlayground />);
    await waitFor(() =>
      expect(screen.getByTestId('playground-no-models')).toBeTruthy(),
    );

    const btn = screen.getByTestId('playground-run');
    expect(btn).toBeDisabled();
    fireEvent.click(btn);
    expect(API.post).not.toHaveBeenCalled();
  });

  // prefill of an unroutable model -> console.playground.prefill_unroutable
  // hint, and the draft falls back to the default compare set instead of
  // silently rendering a single dead column.
  it('shows a prefill_unroutable hint when ?prefill_model= names a model that is not routable', async () => {
    window.history.replaceState(
      {},
      '',
      '/playground?prefill_model=rt-does-not-exist',
    );

    render(<HFPlayground />);
    await waitForModelsReady();

    await waitFor(() =>
      expect(screen.getByTestId('playground-prefill-unroutable')).toBeTruthy(),
    );
    expect(
      screen.getByTestId('playground-prefill-unroutable').textContent,
    ).toContain('rt-does-not-exist');
    // Falls back to the default 3-model set rather than staying on the
    // dead single-model draft.
    expect(screen.getByText(/3 models, one prompt/i)).toBeTruthy();
  });
});
