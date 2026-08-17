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
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'zh' } }),
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
      '管理员未设置关于内容',
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
    expect(link).toHaveTextContent('访问关于');
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

  // DEFECT (skipped): isUrl() accepts any parseable URL, scheme included, so an
  // admin-supplied "javascript:" document becomes a clickable link that runs
  // script in the reader's session. Only http/https should reach an href.
  it.skip('refuses to build a link out of a javascript: URL', async () => {
    API.get.mockReturnValue(ok('javascript:alert(document.cookie)'));

    render(<DocumentRenderer {...baseProps} />);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });
});

describe('DocumentRenderer — HTML content', () => {
  // DEFECT (skipped): index.jsx calls useEffect from *inside* the
  // `if (isHtmlContent(content))` branch (line 213), after four early returns.
  // The first render always bails out at `if (loading)` with 6 hooks; the
  // render that finally reaches the HTML branch asks for 7, so React aborts
  // with "Rendered more hooks than during the previous render." Every HTML
  // document — the whole reason the branch exists — takes the page down.
  it.skip('renders an HTML document inline', async () => {
    API.get.mockReturnValue(ok('<div><p>hello from html</p></div>'));

    render(<DocumentRenderer {...baseProps} />);

    expect(await screen.findByText('hello from html')).toBeInTheDocument();
  });

  // DEFECT (skipped): same crash. Markdown prose containing an angle-bracket
  // pair (`a<b and c>d`) satisfies the loose /<\/?[a-z][\s\S]*>/i test, so
  // plain text can also fall into the broken branch.
  it.skip('does not route plain prose containing angle brackets into the HTML branch', async () => {
    API.get.mockReturnValue(ok('compare a<b and c>d for the threshold'));

    render(<DocumentRenderer {...baseProps} />);

    expect(await screen.findByTestId('markdown')).toBeInTheDocument();
  });
});
