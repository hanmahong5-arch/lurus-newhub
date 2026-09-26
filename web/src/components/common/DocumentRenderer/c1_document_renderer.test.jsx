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
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  // Interpolating identity: the component now builds these strings with
  // t('…{{title}}…', { title }) instead of concatenating the title into the
  // key, so a mock that returns the key verbatim would leave {{title}} on
  // screen and the assertions below would be pinning a placeholder.
  useTranslation: () => ({
    t: (key, opts) =>
      opts
        ? String(key).replace(/\{\{(\w+)\}\}/g, (whole, name) =>
            name in opts ? opts[name] : whole,
          )
        : key,
    i18n: { language: 'zh' },
  }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn() },
  showError: vi.fn(),
}));

// Semi UI + its illustration bundle cannot load under jsdom (canvas).
vi.mock('@douyinfe/semi-ui', () => ({
  Empty: ({ title }) => React.createElement('div', { role: 'status' }, title),
  Card: ({ children }) => React.createElement('div', null, children),
  Spin: () =>
    React.createElement('div', { 'data-testid': 'spinner' }, 'loading'),
  Typography: {
    Title: ({ children }) => React.createElement('h2', null, children),
  },
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationConstruction: () => null,
  IllustrationConstructionDark: () => null,
}));

vi.mock('../markdown/MarkdownRenderer', () => ({
  default: ({ content }) =>
    React.createElement('div', { 'data-testid': 'markdown' }, content),
}));

import { API, showError } from '../../../helpers';
import DocumentRenderer from './index';

const CACHE_KEY = 'c1-doc-cache';

const baseProps = {
  apiEndpoint: '/api/about',
  title: '关于',
  cacheKey: CACHE_KEY,
  emptyMessage: '加载失败',
};

const ok = (data) => Promise.resolve({ data: { success: true, data } });
const notOk = (message) =>
  Promise.resolve({ data: { success: false, message, data: null } });

beforeEach(() => {
  localStorage.clear();
  API.get.mockReset();
  showError.mockReset();
  document.getElementById(`document-renderer-styles-${CACHE_KEY}`)?.remove();
});

describe('DocumentRenderer — fetch and cache', () => {
  it('shows a spinner until the first response lands', async () => {
    let resolveGet;
    API.get.mockReturnValue(
      new Promise((res) => {
        resolveGet = res;
      }),
    );

    render(<DocumentRenderer {...baseProps} />);
    expect(screen.getByTestId('spinner')).toBeInTheDocument();

    resolveGet({ data: { success: true, data: 'plain body' } });
    await waitFor(() =>
      expect(screen.queryByTestId('spinner')).not.toBeInTheDocument(),
    );
  });

  it('renders markdown content and writes it to the cache', async () => {
    API.get.mockReturnValue(ok('# heading\n\nbody text'));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() =>
      expect(screen.getByTestId('markdown')).toHaveTextContent('body text'),
    );
    expect(localStorage.getItem(CACHE_KEY)).toBe('# heading\n\nbody text');
    expect(showError).not.toHaveBeenCalled();
  });

  it('paints the cached copy first, then swaps in the fresh one', async () => {
    localStorage.setItem(CACHE_KEY, 'stale cached body');
    API.get.mockReturnValue(ok('fresh body'));

    render(<DocumentRenderer {...baseProps} />);

    // No spinner: the cache short-circuits the loading state.
    expect(screen.queryByTestId('spinner')).not.toBeInTheDocument();
    expect(screen.getByTestId('markdown')).toHaveTextContent(
      'stale cached body',
    );

    await waitFor(() =>
      expect(screen.getByTestId('markdown')).toHaveTextContent('fresh body'),
    );
    expect(localStorage.getItem(CACHE_KEY)).toBe('fresh body');
  });

  it('reports the server message and shows the empty state when there is nothing cached', async () => {
    API.get.mockReturnValue(notOk('文档未配置'));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('文档未配置'));
    expect(screen.getByRole('status')).toHaveTextContent(
      '管理员未设置 关于 内容',
    );
  });

  it('falls back to emptyMessage when the server sends no message', async () => {
    API.get.mockReturnValue(notOk(undefined));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('加载失败'));
  });

  it('reports emptyMessage when the request itself fails', async () => {
    API.get.mockReturnValue(Promise.reject(new Error('network down')));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('加载失败'));
    expect(screen.getByRole('status')).toBeInTheDocument();
  });

  it('keeps the cached copy silently when the refresh request fails', async () => {
    localStorage.setItem(CACHE_KEY, 'cached body');
    API.get.mockReturnValue(Promise.reject(new Error('network down')));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    // A network blip must not blank the page nor nag the reader.
    expect(screen.getByTestId('markdown')).toHaveTextContent('cached body');
    expect(showError).not.toHaveBeenCalled();
    expect(localStorage.getItem(CACHE_KEY)).toBe('cached body');
  });

  it('treats whitespace-only content as empty', async () => {
    API.get.mockReturnValue(ok('   \n  '));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument());
    expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
  });
});

describe('DocumentRenderer — URL content', () => {
  it('renders a link card when the document is just a URL', async () => {
    API.get.mockReturnValue(ok('  https://docs.example.com/guide  '));

    render(<DocumentRenderer {...baseProps} />);

    const link = await screen.findByRole('link');
    expect(link).toHaveAttribute('href', 'https://docs.example.com/guide');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(link).toHaveTextContent('访问 关于');
    expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
  });

  it('does not mistake ordinary prose for a URL', async () => {
    API.get.mockReturnValue(ok('访问 https://example.com 了解更多'));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() =>
      expect(screen.getByTestId('markdown')).toBeInTheDocument(),
    );
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  // isUrl() used to accept any parseable URL, scheme included, so an
  // admin-supplied "javascript:" document became a clickable link that ran
  // script in the reader's session. Only http/https may reach an href.
  it('refuses to build a link out of a javascript: URL', async () => {
    API.get.mockReturnValue(ok('javascript:alert(document.cookie)'));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });
});

describe('DocumentRenderer — HTML content', () => {
  // Was skipped as a DEFECT lock: index.jsx used to call useEffect from
  // *inside* the `if (isHtmlContent(content))` branch, after four early
  // returns. The first render always bailed out at `if (loading)` with 6
  // hooks; the render that finally reached the HTML branch asked for 7, so
  // React aborted with "Rendered more hooks than during the previous
  // render." Every HTML document — the whole reason the branch exists —
  // took the page down. Cycle 12 L3 removed that effect (the <style> blocks
  // it republished do not survive helpers/sanitize), so the lock is live.
  it('renders an HTML document inline', async () => {
    API.get.mockReturnValue(ok('<div><p>hello from html</p></div>'));

    render(<DocumentRenderer {...baseProps} />);

    expect(await screen.findByText('hello from html')).toBeInTheDocument();
  });

  // The operator-authored legal pages are the only writers here, but they
  // are still writers: an XSS in /privacy-policy is an XSS in every session
  // that opens it.
  it('strips an event handler out of an HTML document', async () => {
    API.get.mockReturnValue(
      ok('<div><img src=x onerror=alert(1)><p>policy text</p></div>'),
    );

    render(<DocumentRenderer {...baseProps} />);

    const para = await screen.findByText('policy text');
    const host = para.closest('.prose');
    expect(host.querySelector('img')).not.toBeNull();
    expect(host.innerHTML).not.toContain('onerror');
  });

  // STILL SKIPPED, and no longer for the reason above: the crash is gone,
  // but isHtmlContent's loose /<\/?[a-z][\s\S]*>/i test still routes prose
  // with an angle-bracket pair (`a<b and c>d`) into the HTML branch, where
  // the parser swallows the pseudo-tag. That predates this change and is
  // unchanged by it — the pre-cycle-12 code lost the same characters through
  // its own tempDiv.innerHTML round-trip, on top of crashing. Narrowing the
  // detector is an owner call (it decides what an operator may paste into
  // About / Privacy policy), tracked as cycle 12 L3 open question.
  it.skip('does not route plain prose containing angle brackets into the HTML branch', async () => {
    API.get.mockReturnValue(ok('compare a<b and c>d for the threshold'));

    render(<DocumentRenderer {...baseProps} />);

    expect(await screen.findByTestId('markdown')).toBeInTheDocument();
  });
});
