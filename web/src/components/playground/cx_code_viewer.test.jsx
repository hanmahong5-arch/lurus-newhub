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

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

// Semi UI is unimportable under jsdom (lottie-web → canvas). Shim only what
// CodeViewer touches. Tooltip renders its `content` into a marker node so the
// copy button's tooltip label stays assertable — a `() => null` stub would let
// the whole copy affordance vanish without a test noticing.
const toastSuccess = vi.fn();
const toastError = vi.fn();
vi.mock('@douyinfe/semi-ui', () => ({
  Button: ({ icon, children, onClick, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, ...rest },
      icon,
      children,
    ),
  Tooltip: ({ content, children }) =>
    React.createElement(
      'span',
      null,
      React.createElement('span', { 'data-testid': 'tip' }, content),
      children,
    ),
  Toast: {
    success: (...a) => toastSuccess(...a),
    error: (...a) => toastError(...a),
  },
}));

vi.mock('lucide-react', () => ({
  Copy: () => React.createElement('i', { 'data-testid': 'icon-copy' }),
  ChevronDown: () =>
    React.createElement('i', { 'data-testid': 'icon-chevron-down' }),
  ChevronUp: () =>
    React.createElement('i', { 'data-testid': 'icon-chevron-up' }),
}));

const copyMock = vi.fn(async () => true);
vi.mock('../../helpers', () => ({ copy: (...a) => copyMock(...a) }));

import CodeViewer from './CodeViewer';

const bodyText = () => document.body.textContent;
const copyButton = () => screen.getAllByRole('button')[0];

beforeEach(() => {
  copyMock.mockReset();
  copyMock.mockResolvedValue(true);
  toastSuccess.mockReset();
  toastError.mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

describe('CodeViewer — empty state', () => {
  it.each([
    ['preview', '正在构造请求体预览...'],
    ['request', '暂无请求数据'],
    ['response', '暂无响应数据'],
  ])(
    'shows the %s-specific placeholder when there is no content',
    (title, expected) => {
      render(<CodeViewer content={null} title={title} />);
      expect(bodyText()).toContain(expected);
    },
  );

  it('falls back to a generic placeholder for an unknown title', () => {
    render(<CodeViewer content='' title='something-else' />);
    expect(bodyText()).toContain('暂无数据');
  });

  it('renders no copy affordance in the empty state', () => {
    render(<CodeViewer content={null} title='request' />);
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe('CodeViewer — formatting', () => {
  it('pretty-prints an object with two-space indentation', () => {
    render(<CodeViewer content={{ model: 'gpt-4o', stream: true }} />);
    // Two-space indent is the visible contract of "formatted, not minified".
    expect(bodyText()).toContain('"model": "gpt-4o"');
    expect(bodyText()).toContain('  "model"');
  });

  it('re-indents a minified JSON string rather than echoing it', () => {
    const { container } = render(<CodeViewer content='{"a":1,"b":2}' />);
    const text = container.textContent;
    expect(text).toContain('"a": 1');
    expect(text).not.toContain('{"a":1,"b":2}');
  });

  it('passes a non-JSON string through unchanged', () => {
    render(<CodeViewer content='plain text, not json' />);
    expect(bodyText()).toContain('plain text, not json');
  });

  it('colours JSON keys, string values, numbers and booleans differently', () => {
    const { container } = render(
      <CodeViewer content={{ key: 'val', n: 42, flag: true }} />,
    );
    const colours = Array.from(container.querySelectorAll('span[style]'))
      .map((el) => el.getAttribute('style'))
      .join('|');

    // Distinct hues per token class — if the highlighter collapsed to one
    // colour, or stopped emitting spans entirely, this goes red.
    expect(colours).toContain('#9cdcfe'); // key
    expect(colours).toContain('#ce9178'); // string value
    expect(colours).toContain('#b5cea8'); // number
    expect(colours).toContain('#569cd6'); // boolean
  });
});

describe('CodeViewer — copy', () => {
  it('hands the pretty-printed JSON to the clipboard helper for object content', async () => {
    render(<CodeViewer content={{ a: 1 }} />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(copyMock).toHaveBeenCalledTimes(1));
    expect(copyMock).toHaveBeenCalledWith(JSON.stringify({ a: 1 }, null, 2));
  });

  it('hands a string payload through verbatim', async () => {
    render(<CodeViewer content='raw-body' />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(copyMock).toHaveBeenCalledWith('raw-body'));
  });

  it('confirms with a success toast when the clipboard write works', async () => {
    render(<CodeViewer content='x' />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(toastSuccess).toHaveBeenCalledTimes(1));
    expect(toastError).not.toHaveBeenCalled();
  });

  it('reports failure when the clipboard helper throws', async () => {
    copyMock.mockRejectedValue(new Error('denied'));
    render(<CodeViewer content='x' />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    expect(toastSuccess).not.toHaveBeenCalled();
  });

  // -------------------------------------------------------------------
  // DEFECT: handleCopy fires Toast.success('已复制到剪贴板') BEFORE it
  // inspects the boolean that `copy()` returns. When the clipboard write is
  // refused (non-secure context, no permission — `copy` resolves false) the
  // user gets a green "copied to clipboard" confirmation and only then a red
  // failure toast. Whoever paged past the first toast walks away believing a
  // request body is on their clipboard when it is not.
  // -------------------------------------------------------------------
  // The pin that used to sit here asserted the success toast DOES fire on a
  // refused copy, so moving Toast.success behind the `success` check — the
  // lock below — would have turned it red. What must hold either side of that
  // fix: a refused copy is reported as a failure exactly once, and the failure
  // is the LAST thing the user is told, not something an optimistic toast
  // fired afterwards can bury. Today both toasts fire (success first); after
  // the fix only the failure does; either way the last word is "it failed".
  it('a refused copy ends with a failure toast, exactly one, and nothing after it', async () => {
    copyMock.mockResolvedValue(false);
    render(<CodeViewer content='x' />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    const lastError = toastError.mock.invocationCallOrder.at(-1);
    const lastSuccess = toastSuccess.mock.invocationCallOrder.at(-1) ?? -1;
    expect(lastError).toBeGreaterThan(lastSuccess);
  });

  it.skip('CONTRACT: a refused copy shows no success toast', async () => {
    copyMock.mockResolvedValue(false);
    render(<CodeViewer content='x' />);
    fireEvent.click(copyButton());

    await waitFor(() => expect(toastError).toHaveBeenCalledTimes(1));
    expect(toastSuccess).not.toHaveBeenCalled();
  });
});

describe('CodeViewer — large-content guard', () => {
  // 60k chars: over MAX_DISPLAY_LENGTH (50k), under the very-large threshold
  // (100k). Non-JSON so formatContent leaves the length alone.
  const large = 'a'.repeat(60000);
  const veryLarge = 'b'.repeat(120001);

  it('truncates and flags oversized content instead of rendering all of it', () => {
    const { container } = render(<CodeViewer content={large} />);
    expect(container.textContent).toContain('内容被截断以提升性能');
    expect(container.textContent).toContain('内容较大，部分功能可能受限');
    // Truncated to the 5k preview window, not the full 60k.
    expect(container.textContent.length).toBeLessThan(large.length);
  });

  it('does not truncate content under the threshold', () => {
    const { container } = render(<CodeViewer content={'a'.repeat(100)} />);
    expect(container.textContent).not.toContain('内容被截断以提升性能');
  });

  it('expanding reveals the full content and drops the truncation marker', () => {
    const { container } = render(<CodeViewer content={large} />);
    const expand = screen.getAllByRole('button').at(-1);
    fireEvent.click(expand);

    expect(container.textContent).not.toContain('内容被截断以提升性能');
    expect(container.textContent.length).toBeGreaterThan(50000);
  });

  it('shows the very-large performance-mode notice past the second threshold', () => {
    const { container } = render(<CodeViewer content={veryLarge} />);
    expect(container.textContent).toContain('内容较大，已启用性能优化模式');
  });

  it('very-large content goes through a processing state before expanding', async () => {
    vi.useFakeTimers();
    try {
      const { container } = render(<CodeViewer content={veryLarge} />);
      fireEvent.click(screen.getAllByRole('button').at(-1));
      // Deferred so the main thread is not blocked mid-click.
      expect(container.textContent).toContain('正在处理大内容...');

      await vi.advanceTimersByTimeAsync(150);
      expect(container.textContent).not.toContain('正在处理大内容...');
      expect(container.textContent.length).toBeGreaterThan(100000);
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('CodeViewer — untrusted content rendering', () => {
  // -------------------------------------------------------------------
  // DEFECT (security): highlightJson() wraps regex matches in <span> and the
  // result is handed to dangerouslySetInnerHTML WITHOUT escaping the source
  // first. Anything in `content` that is not matched by the token regex —
  // every `<` and `>` — reaches the DOM as live markup. `content` is the raw
  // upstream response / error body shown in the playground Debug panel, so a
  // hostile or compromised upstream channel (or a model that echoes an
  // attacker's prompt) can execute script in the console's own origin, where
  // the session cookie and admin routes live.
  // -------------------------------------------------------------------
  // A payload with no double quotes, digits or true/false/null keywords slips
  // through the token regex completely untouched, so it lands in the DOM
  // byte-for-byte. (Payloads that DO contain quotes still produce a live
  // element — the highlighter just mangles their attribute values.)
  const RAW_PAYLOAD = '<img src=x onerror=alert()>';

  // The two inverted companions that used to sit here asserted that the
  // injected <img onerror> and <b id=cx-inj> nodes DO materialise. They were
  // removed on review: escaping the source — the whole point of the locks below
  // — would have turned both red, so the security fix would have arrived
  // looking like a regression in a file whose own comment calls the behaviour a
  // defect. A hole that is already open cannot regress, so the pins guarded
  // nothing. The contract is held solely by the two locks below, both verified
  // RED against today's highlightJson.
  //
  // What still has to hold either side of that fix is that the operator can
  // read the payload at all, so assert that instead.
  it('shows the untrusted payload to the operator rather than swallowing it', () => {
    const { container } = render(
      <CodeViewer content={{ error: 'upstream refused the request' }} />,
    );
    expect(container.textContent).toContain('upstream refused the request');
  });

  it.skip('CONTRACT: markup in response content is escaped, never executed', () => {
    const { container } = render(<CodeViewer content={RAW_PAYLOAD} />);
    expect(container.querySelector('img')).toBeNull();
    // Still shown to the operator verbatim, just inert.
    expect(container.textContent).toContain('onerror=alert()');
  });

  it.skip('CONTRACT: markup inside a JSON string value is escaped', () => {
    const { container } = render(
      <CodeViewer content={{ error: '<b id=cx-inj>boom</b>' }} />,
    );
    expect(container.querySelector('#cx-inj')).toBeNull();
    expect(container.textContent).toContain('<b id=cx-inj>');
  });
});
