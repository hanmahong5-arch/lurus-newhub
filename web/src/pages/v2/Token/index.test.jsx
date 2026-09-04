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

// getServerAddress is stubbed rather than imported for real: the real module
// (helpers/token.js) pulls in helpers/api.js → Semi UI → lottie-web, which
// needs a canvas that jsdom does not have. The two halves of the guarantee are
// locked separately and both halves are real:
//   1. the helper returns server_address, else window.location.origin —
//      helpers/h1_api.test.jsx, `describe('getServerAddress')`, 4 cases;
//   2. the page prints whatever the helper returns, never a literal — here.
// The stub therefore returns a value the page could not have hardcoded.
vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getServerAddress: vi.fn(),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => vi.fn(),
}));

vi.mock('react-i18next', () => ({
  // Honor a string defaultValue (t(key, 'English')) like real i18next, so
  // console.* wrapping renders readable English in tests instead of bare keys.
  useTranslation: () => ({
    t: (k, d) => (typeof d === 'string' ? d : k),
  }),
  // formatting.js imports the real i18n instance, which registers this
  // plugin at module load — the mock must expose it or the suite can't load.
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

// Interactive stub: renders a confirm button only when visible so tests can
// drive the typed-confirmation flows without reproducing the Semi Modal.
vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, onConfirm }) =>
    visible
      ? React.createElement(
          'button',
          { 'data-testid': 'confirm-ok', onClick: onConfirm },
          'confirm',
        )
      : null,
}));

import HFToken from './index';
import { API, getServerAddress } from '../../../helpers';

const fakeToken = {
  id: 1,
  name: 'prod',
  key: 'abcd1234efgh',
  status: 1,
  unlimited_quota: true,
  used_quota: 0,
  remain_quota: 0,
  expired_time: -1,
  created_time: Math.floor(Date.now() / 1000) - 3600,
  accessed_time: Math.floor(Date.now() / 1000) - 60,
};

// The page issues TWO GETs: the token list and the tenant's project list
// (cost attribution, migration 029). A URL-agnostic mock would answer both
// with the same payload, so the project <select> would render an <option>
// per token and duplicate every token name in the DOM.
const wireGet = (tokens, projects = []) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/projects')) {
      return Promise.resolve({
        data: { success: true, data: { items: projects } },
      });
    }
    return Promise.resolve({
      data: { success: true, data: { items: tokens } },
    });
  });
};

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  // put/delete were never reset here, so call counts leaked between tests and
  // any toHaveBeenCalledTimes() assertion measured the whole file.
  API.put.mockReset();
  API.delete.mockReset();
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
  // Default to the production-shaped answer: `server_address` is empty in
  // production, so the helper returns the serving origin.
  getServerAddress.mockReset();
  getServerAddress.mockReturnValue(window.location.origin);
  // jsdom has no clipboard by default — provide a spy.
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  });
});

describe('Token page — multi-format client URLs', () => {
  // These used to assert the literal 'https://api.lurus.cn', a domain retired
  // in 2026-04 that no longer resolves — so the page's own tests certified an
  // integration guide that could not work. The host must now track the server
  // the console is served by.
  it('renders the client base URLs and copies the OpenAI base url', async () => {
    wireGet([fakeToken]);
    getServerAddress.mockReturnValue('https://hub.example.test');

    render(<HFToken />);

    await waitFor(() => screen.getByText('client base urls'));

    // Gemini base url is unique to the client-endpoints panel.
    expect(screen.getByText('https://hub.example.test/v1beta')).toBeTruthy();

    // Copy the OpenAI-compatible endpoint.
    const copyBtn = screen.getByTestId('copy-endpoint-OpenAI / compatible');
    fireEvent.click(copyBtn);

    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
        'https://hub.example.test/v1',
      );
    });
  });

  // Production's `server_address` is an empty string, so the helper returns the
  // serving origin (locked in helpers/h1_api.test.jsx) — that is the path real
  // customers hit, and it must reach the page too, not just the configured one.
  it('renders the serving origin when that is what the helper resolves to', async () => {
    wireGet([fakeToken]);
    // beforeEach already set the mock to window.location.origin.

    render(<HFToken />);

    await waitFor(() => screen.getByText('client base urls'));

    expect(screen.getByText(`${window.location.origin}/v1beta`)).toBeTruthy();

    fireEvent.click(screen.getByTestId('copy-endpoint-Anthropic · Claude SDK'));
    await waitFor(() => {
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
        window.location.origin,
      );
    });
  });

  // The snippets are what customers actually paste. Locking the panel alone
  // would let a hardcoded host survive inside buildSnippets().
  it('builds the curl snippet against the resolved host, not a literal', async () => {
    wireGet([fakeToken]);
    getServerAddress.mockReturnValue('https://hub.example.test/');

    // The first token is selected by default and the snippet tab defaults to
    // curl, so the snippet renders without any interaction.
    render(<HFToken />);
    await waitFor(() => screen.getByText('client base urls'));

    const shown = document.body.textContent;
    // Trailing slash on server_address must not yield '//v1'.
    expect(shown).toContain('https://hub.example.test/v1/chat/completions');
    expect(shown).not.toContain('api.lurus.cn');
  });

  it('offers a copy control for each supported SDK base url', async () => {
    wireGet([fakeToken]);

    render(<HFToken />);
    await waitFor(() => screen.getByText('client base urls'));

    for (const label of [
      'OpenAI / compatible',
      'Anthropic · Claude SDK',
      'Gemini',
    ]) {
      expect(screen.getByTestId(`copy-endpoint-${label}`)).toBeTruthy();
    }
  });
});

describe('Token page — batch operations', () => {
  const t1 = { ...fakeToken, id: 1, name: 'one' };
  const t2 = { ...fakeToken, id: 2, name: 'two' };

  it('hides the batch bar until tokens are selected, then deletes via one POST', async () => {
    wireGet([t1, t2]);
    API.post.mockResolvedValue({ data: { success: true, deleted: 2 } });

    render(<HFToken />);
    await waitFor(() => screen.getByText('one'));

    // No batch bar with an empty selection.
    expect(screen.queryByTestId('token-batch-bar')).toBeNull();

    // Select both tokens.
    fireEvent.click(screen.getByTestId('token-check-1'));
    fireEvent.click(screen.getByTestId('token-check-2'));

    // Batch bar now visible.
    expect(screen.getByTestId('token-batch-bar')).toBeTruthy();

    // Delete → confirm.
    fireEvent.click(screen.getByTestId('token-batch-delete-btn'));
    fireEvent.click(screen.getByTestId('confirm-ok'));

    await waitFor(() => {
      const postCall = API.post.mock.calls.find(([url]) =>
        String(url).includes('/tokens/batch-delete'),
      );
      expect(postCall).toBeTruthy();
      expect(postCall[1].ids).toEqual([1, 2]);
    });
  });

  it('keeps batch copy disabled with an honest deferral reason', async () => {
    wireGet([t1]);

    render(<HFToken />);
    await waitFor(() => screen.getByText('one'));
    fireEvent.click(screen.getByTestId('token-check-1'));

    const copyBtn = screen.getByTestId('token-batch-copy-btn');
    expect(copyBtn.disabled).toBe(true);
    expect(copyBtn.getAttribute('title')).toMatch(/key-reveal endpoint/i);
  });
});

describe('Token page — project attribution (migration 029)', () => {
  const projects = [
    { id: 7, name: 'Marketing', description: '' },
    { id: 8, name: 'Research', description: '' },
  ];

  it('POSTs the selected project_id when creating a token', async () => {
    wireGet([], projects);
    API.post.mockResolvedValue({
      data: { success: true, data: { id: 9, name: 'k', key: 'sk-new' } },
    });

    render(<HFToken />);
    await waitFor(() => screen.getByText('+ new token'));
    fireEvent.click(screen.getByText('+ new token'));

    await waitFor(() => screen.getByTestId('token-project-select'));
    fireEvent.change(screen.getByTestId('token-name-input'), {
      target: { value: 'tagged' },
    });
    fireEvent.change(screen.getByTestId('token-project-select'), {
      target: { value: '8' },
    });
    fireEvent.click(screen.getByTestId('token-create-submit'));

    await waitFor(() => {
      const call = API.post.mock.calls.find(([url]) =>
        String(url).endsWith('/tokens'),
      );
      expect(call).toBeTruthy();
      expect(call[1].project_id).toBe(8);
    });
  });

  it('sends project_id 0 when left unassigned', async () => {
    wireGet([], projects);
    API.post.mockResolvedValue({
      data: { success: true, data: { id: 9, name: 'k', key: 'sk-new' } },
    });

    render(<HFToken />);
    await waitFor(() => screen.getByText('+ new token'));
    fireEvent.click(screen.getByText('+ new token'));

    await waitFor(() => screen.getByTestId('token-project-select'));
    fireEvent.change(screen.getByTestId('token-name-input'), {
      target: { value: 'untagged' },
    });
    fireEvent.click(screen.getByTestId('token-create-submit'));

    await waitFor(() => {
      const call = API.post.mock.calls.find(([url]) =>
        String(url).endsWith('/tokens'),
      );
      expect(call).toBeTruthy();
      // 0 = unassigned, never null/undefined — the column is NOT NULL.
      expect(call[1].project_id).toBe(0);
    });
  });

  it('re-attributes an existing token via PUT from the detail panel', async () => {
    wireGet([{ ...fakeToken, project_id: 7 }], projects);
    API.put.mockResolvedValue({ data: { success: true } });

    render(<HFToken />);
    await waitFor(() => screen.getByTestId('token-detail-project-select'));

    const select = screen.getByTestId('token-detail-project-select');
    expect(select.value).toBe('7');

    fireEvent.change(select, { target: { value: '8' } });

    await waitFor(() => {
      const call = API.put.mock.calls.find(([url]) =>
        String(url).includes('/tokens/1'),
      );
      expect(call).toBeTruthy();
      expect(call[1]).toEqual({ project_id: 8 });
    });
  });

  it('keeps rendering a retired project the token still points at', async () => {
    // The project was deleted, so it is absent from the picker list — the
    // token must still show its own value instead of silently resetting to
    // "unassigned" the moment the row is opened.
    wireGet([{ ...fakeToken, project_id: 99 }], projects);

    render(<HFToken />);
    await waitFor(() => screen.getByTestId('token-detail-project-select'));

    expect(screen.getByTestId('token-detail-project-select').value).toBe('99');
  });
});

describe('Token page — repeat-safety', () => {
  const deferred = () => {
    let resolve;
    const promise = new Promise((r) => {
      resolve = r;
    });
    return { promise, resolve };
  };

  it('mints one token when create is clicked three times', async () => {
    // A duplicate relay key is not a cosmetic bug: it is a second live
    // credential the user never asked for and may never notice.
    wireGet([], []);
    const d = deferred();
    API.post.mockReturnValue(d.promise);

    render(<HFToken />);
    await waitFor(() => screen.getByText('+ new token'));
    fireEvent.click(screen.getByText('+ new token'));
    await waitFor(() => screen.getByTestId('token-create-submit'));

    fireEvent.change(screen.getByTestId('token-name-input'), {
      target: { value: 'prod' },
    });
    const submit = screen.getByTestId('token-create-submit');
    fireEvent.click(submit);
    fireEvent.click(submit);
    fireEvent.click(submit);

    d.resolve({
      data: { success: true, data: { id: 9, name: 'prod', key: 'sk-new' } },
    });
    await waitFor(() => {
      expect(API.post).toHaveBeenCalledTimes(1);
    });
  });

  it('sends one PUT when the project select is changed twice in flight', async () => {
    wireGet(
      [{ ...fakeToken, project_id: 0 }],
      [
        { id: 7, name: 'Marketing' },
        { id: 8, name: 'Research' },
      ],
    );
    const d = deferred();
    API.put.mockReturnValue(d.promise);

    render(<HFToken />);
    await waitFor(() => screen.getByTestId('token-detail-project-select'));

    const select = screen.getByTestId('token-detail-project-select');
    fireEvent.change(select, { target: { value: '7' } });
    fireEvent.change(select, { target: { value: '8' } });

    d.resolve({ data: { success: true } });
    await waitFor(() => {
      expect(API.put).toHaveBeenCalledTimes(1);
    });
  });
});
