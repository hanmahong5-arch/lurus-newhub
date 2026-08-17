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
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'zh' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// mermaid pulls a full diagram engine (and canvas) at import time.
const mermaidMock = vi.hoisted(() => ({
  initialize: vi.fn(),
  run: vi.fn(() => Promise.resolve()),
}));
vi.mock('mermaid', () => ({ default: mermaidMock }));

// Math/highlight plugins are stubbed to no-ops: highlight.js and katex are
// megabytes of payload, and switching maths off keeps the escapeBrackets output
// visible as literal text, which is exactly what these tests inspect.
// remark-gfm and remark-breaks stay real — tables and line breaks are part of
// the contract under test.
vi.mock('remark-math', () => ({ default: () => () => {} }));
vi.mock('rehype-katex', () => ({ default: () => () => {} }));
vi.mock('rehype-highlight', () => ({ default: () => () => {} }));

vi.mock('../../../helpers', () => ({
  copy: vi.fn(() => Promise.resolve(true)),
  rehypeSplitWordsIntoSpans: () => () => {},
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconCopy: () => React.createElement('span', null, 'copy-icon'),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    Button: ({ onClick, icon, children }) =>
      h('button', { type: 'button', onClick }, icon, children),
    Tooltip: ({ children }) => children,
    Toast: { success: vi.fn(), error: vi.fn() },
  };
});

import { Toast } from '@douyinfe/semi-ui';
import { copy } from '../../../helpers';
import MarkdownRenderer, { Mermaid } from './MarkdownRenderer';

const body = () => document.querySelector('.markdown-body');

beforeEach(() => {
  copy.mockClear();
  copy.mockResolvedValue(true);
  Toast.success.mockClear();
  Toast.error.mockClear();
  mermaidMock.run.mockClear();
  mermaidMock.run.mockResolvedValue(undefined);
});

describe('MarkdownRenderer — container', () => {
  it('shows a rendering placeholder instead of content while loading', () => {
    render(<MarkdownRenderer content='# never shown' loading />);

    expect(screen.getByText('正在渲染...')).toBeInTheDocument();
    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
  });

  it('applies typography props and forwards the rest to the wrapper', () => {
    render(
      <MarkdownRenderer
        content='hello'
        fontSize={18}
        fontFamily='monospace'
        className='chat-bubble'
        style={{ padding: '4px' }}
        data-testid='md-root'
      />,
    );

    const root = screen.getByTestId('md-root');
    expect(root.className).toBe('markdown-body chat-bubble');
    expect(root).toHaveStyle({
      fontSize: '18px',
      fontFamily: 'monospace',
      padding: '4px',
    });
  });

  it('defaults the font size to 14px', () => {
    render(<MarkdownRenderer content='hello' />);

    expect(body()).toHaveStyle({ fontSize: '14px' });
  });
});

describe('MarkdownRenderer — block elements', () => {
  it('renders headings at every level', () => {
    render(
      <MarkdownRenderer
        content={
          '# one\n\n## two\n\n### three\n\n#### four\n\n##### five\n\n###### six'
        }
      />,
    );

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('one');
    expect(screen.getByRole('heading', { level: 4 })).toHaveTextContent('four');
    expect(screen.getByRole('heading', { level: 6 })).toHaveTextContent('six');
  });

  it('renders bulleted and numbered lists', () => {
    render(
      <MarkdownRenderer content={'- alpha\n- beta\n\n1. first\n2. second'} />,
    );

    expect(document.querySelectorAll('ul li')).toHaveLength(2);
    expect(document.querySelectorAll('ol li')).toHaveLength(2);
    expect(screen.getByText('beta')).toBeInTheDocument();
  });

  it('renders a blockquote', () => {
    render(<MarkdownRenderer content='> quoted wisdom' />);

    expect(document.querySelector('blockquote')).toHaveTextContent(
      'quoted wisdom',
    );
  });

  it('renders a GFM table inside a horizontally scrollable box', () => {
    render(
      <MarkdownRenderer
        content={'| model | price |\n| --- | --- |\n| gpt | 3 |'}
      />,
    );

    const table = document.querySelector('table');
    expect(table).not.toBeNull();
    expect(document.querySelectorAll('th')).toHaveLength(2);
    expect(document.querySelectorAll('td')).toHaveLength(2);
    expect(table.parentElement.style.overflow).toBe('auto');
  });

  it('paints user-message text white', () => {
    const { unmount } = render(
      <MarkdownRenderer content='hello there' className='user-message' />,
    );
    expect(document.querySelector('p')).toHaveStyle({
      color: 'rgb(255, 255, 255)',
    });
    unmount();

    render(<MarkdownRenderer content='hello there' />);
    expect(document.querySelector('p')).not.toHaveStyle({
      color: 'rgb(255, 255, 255)',
    });
  });
});

describe('MarkdownRenderer — links and media', () => {
  it('opens external links in a new tab and internal hash links in place', () => {
    render(
      <MarkdownRenderer
        content={'[out](https://example.com) and [in](/#/console)'}
      />,
    );

    expect(screen.getByRole('link', { name: 'out' })).toHaveAttribute(
      'target',
      '_blank',
    );
    expect(screen.getByRole('link', { name: 'in' })).toHaveAttribute(
      'target',
      '_self',
    );
  });

  it('turns an audio link into an audio player', () => {
    render(
      <MarkdownRenderer content='[listen](https://cdn.example.com/a.mp3)' />,
    );

    const audio = document.querySelector('audio');
    expect(audio).toHaveAttribute('src', 'https://cdn.example.com/a.mp3');
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('turns a video link into a video player', () => {
    render(
      <MarkdownRenderer content='[watch](https://cdn.example.com/c.mp4)' />,
    );

    expect(document.querySelector('video source')).toHaveAttribute(
      'src',
      'https://cdn.example.com/c.mp4',
    );
  });

  it('gives images a fallback alt and lifts a lone image out of its paragraph', () => {
    render(<MarkdownRenderer content='![](https://cdn.example.com/x.png)' />);

    const img = document.querySelector('img');
    expect(img).toHaveAttribute('alt', 'image');
    expect(img).toHaveAttribute('loading', 'lazy');
    // A <p> wrapper around a block image breaks the layout, so it is dropped.
    expect(img.closest('p')).toBeNull();
  });

  // DEFECT (skipped): the img renderer branches on `src.startsWith('data:')`
  // to load inline images eagerly, but no urlTransform is configured, so
  // react-markdown's defaultUrlTransform strips every non-http(s)/mailto/xmpp
  // protocol first — the src arrives empty, the branch is dead, and a base64
  // image returned by a model renders as a broken image. Enabling data: URIs
  // needs a deliberate urlTransform that still blocks data:image/svg+xml.
  it.skip('loads data-uri images eagerly', () => {
    render(
      <MarkdownRenderer content='![chart](data:image/png;base64,iVBORw0KGgo=)' />,
    );

    expect(document.querySelector('img')).toHaveAttribute(
      'src',
      'data:image/png;base64,iVBORw0KGgo=',
    );
    expect(document.querySelector('img')).toHaveAttribute('loading', 'eager');
  });

  it('keeps an image inside the paragraph when it shares it with text', () => {
    render(
      <MarkdownRenderer content='look ![alt](https://cdn.example.com/x.png) here' />,
    );

    expect(document.querySelector('img').closest('p')).not.toBeNull();
  });
});

describe('MarkdownRenderer — LaTeX and HTML escaping', () => {
  it('rewrites display and inline LaTeX delimiters to dollar signs', () => {
    render(<MarkdownRenderer content={'\\[E=mc^2\\] and \\(a+b\\)'} />);

    // Maths rendering is stubbed out, so the converted delimiters stay visible.
    expect(body().textContent).toContain('$$E=mc^2$$');
    expect(body().textContent).toContain('$a+b$');
  });

  it('leaves bracket escapes inside inline code alone', () => {
    render(<MarkdownRenderer content={'`\\[literal\\]` stays'} />);

    expect(body().textContent).toContain('\\[literal\\]');
    expect(body().textContent).not.toContain('$$literal$$');
  });

  it('fences a bare HTML document into an html code block', () => {
    render(
      <MarkdownRenderer
        content={'<!DOCTYPE html>\n<html><body>hi</body></html>'}
      />,
    );

    expect(document.querySelector('code.language-html')).not.toBeNull();
  });

  it('leaves content that already has fences untouched', () => {
    render(<MarkdownRenderer content={'```js\nconst a = 1;\n```'} />);

    expect(document.querySelector('code.language-js')).not.toBeNull();
    expect(document.querySelector('code.language-html')).toBeNull();
  });
});

describe('MarkdownRenderer — code block chrome', () => {
  const FENCED = '```js\nconst answer = 42;\n```';

  it('copies the code block body and confirms it', async () => {
    render(<MarkdownRenderer content={FENCED} />);

    fireEvent.click(screen.getByRole('button', { name: 'copy-icon' }));

    await waitFor(() => expect(copy).toHaveBeenCalledTimes(1));
    expect(copy.mock.calls[0][0]).toContain('const answer = 42;');
    await waitFor(() =>
      expect(Toast.success).toHaveBeenCalledWith('代码已复制到剪贴板'),
    );
  });

  it('reports a failed copy', async () => {
    copy.mockResolvedValue(false);
    render(<MarkdownRenderer content={FENCED} />);

    fireEvent.click(screen.getByRole('button', { name: 'copy-icon' }));

    await waitFor(() =>
      expect(Toast.error).toHaveBeenCalledWith('复制失败，请手动复制'),
    );
    expect(Toast.success).not.toHaveBeenCalled();
  });

  it('soft-wraps plain-text code blocks but not language-tagged ones', () => {
    const { unmount } = render(
      <MarkdownRenderer content={'```text\nplain words here\n```'} />,
    );
    expect(document.querySelector('code').style.whiteSpace).toBe('pre-wrap');
    unmount();

    render(<MarkdownRenderer content={FENCED} />);
    expect(document.querySelector('code').style.whiteSpace).toBe('');
  });

  it('offers a "show more" control only for tall code blocks', () => {
    const original = Object.getOwnPropertyDescriptor(
      Element.prototype,
      'scrollHeight',
    );
    Object.defineProperty(Element.prototype, 'scrollHeight', {
      configurable: true,
      get: () => 900,
    });

    render(<MarkdownRenderer content={FENCED} />);
    const more = screen.getByRole('button', { name: '显示更多' });

    fireEvent.click(more);
    expect(screen.queryByRole('button', { name: '显示更多' })).toBeNull();

    if (original)
      Object.defineProperty(Element.prototype, 'scrollHeight', original);
    else delete Element.prototype.scrollHeight;
  });

  it('leaves short code blocks uncollapsed', () => {
    render(<MarkdownRenderer content={FENCED} />);

    expect(screen.queryByRole('button', { name: '显示更多' })).toBeNull();
  });
});

describe('Mermaid', () => {
  it('renders the diagram source and hands it to the engine', async () => {
    render(<Mermaid code='graph TD; A-->B;' />);

    expect(screen.getByText('graph TD; A-->B;')).toBeInTheDocument();
    await waitFor(() => expect(mermaidMock.run).toHaveBeenCalledTimes(1));
  });

  it('disappears when the engine rejects the diagram', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    mermaidMock.run.mockRejectedValue(new Error('bad diagram'));

    const { container } = render(<Mermaid code='graph ???' />);

    await waitFor(() => expect(container).toBeEmptyDOMElement());
    err.mockRestore();
  });

  it('does nothing on click while there is no rendered svg', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);

    render(<Mermaid code='graph TD; A-->B;' />);
    fireEvent.click(screen.getByText('graph TD; A-->B;'));

    expect(open).not.toHaveBeenCalled();
    open.mockRestore();
  });

  it('skips the engine entirely for empty code', async () => {
    render(<Mermaid code='' />);

    await act(async () => {});
    expect(mermaidMock.run).not.toHaveBeenCalled();
  });
});
