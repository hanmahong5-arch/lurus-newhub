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
import { render, screen } from '@testing-library/react';
import HfMarkdown from './HfMarkdown';

describe('HfMarkdown', () => {
  it('renders bold text, lists and fenced code as their DOM elements', () => {
    const content = [
      '**bold text**',
      '',
      '- item one',
      '- item two',
      '',
      '```js',
      'const x = 1;',
      '```',
    ].join('\n');
    render(<HfMarkdown content={content} testId='md' />);

    const root = screen.getByTestId('md');
    const strong = root.querySelector('strong');
    expect(strong?.textContent).toBe('bold text');

    const items = root.querySelectorAll('li');
    expect(items.length).toBe(2);
    expect(items[0].textContent).toBe('item one');

    const codeBlock = root.querySelector('pre.hf-code code');
    expect(codeBlock?.textContent).toContain('const x = 1;');
  });

  it('never turns raw HTML in the source into real DOM elements', () => {
    const content =
      'before <script>window.__pwned = true;</script> after\n' +
      '<img src=x onerror="window.__pwned = true">';
    render(<HfMarkdown content={content} testId='md' />);

    const root = screen.getByTestId('md');
    // No script/img element was ever mounted — react-markdown escapes raw
    // HTML to text without rehype-raw.
    expect(root.querySelector('script')).toBeNull();
    expect(root.querySelector('img')).toBeNull();
    expect(window.__pwned).toBeUndefined();
    // The literal markup is still visible as inert text, not silently
    // dropped.
    expect(root.textContent).toContain('<script>');
    expect(root.textContent).toContain('onerror');
  });

  it('neutralises a javascript: href and keeps the link safe otherwise', () => {
    const content =
      '[danger](javascript:alert(1)) and [safe](https://example.com/x)';
    render(<HfMarkdown content={content} testId='md' />);

    // A neutralised link has no `href` at all, which drops it out of the
    // accessible "link" role — query every anchor tag instead.
    const links = Array.from(screen.getByTestId('md').querySelectorAll('a'));
    expect(links).toHaveLength(2);

    const danger = links.find((a) => a.textContent === 'danger');
    expect(danger?.getAttribute('href')).toBeFalsy();

    const safe = links.find((a) => a.textContent === 'safe');
    expect(safe?.getAttribute('href')).toBe('https://example.com/x');
    expect(safe?.getAttribute('target')).toBe('_blank');
    expect(safe?.getAttribute('rel')).toContain('noopener');
  });

  it('renders a table wrapped for horizontal scroll', () => {
    const content = ['| a | b |', '| - | - |', '| 1 | 2 |'].join('\n');
    render(<HfMarkdown content={content} testId='md' />);

    const root = screen.getByTestId('md');
    expect(root.querySelector('.hf-table-scroll table.t')).toBeTruthy();
    expect(root.textContent).toContain('1');
    expect(root.textContent).toContain('2');
  });

  it('renders nothing for empty content', () => {
    const { container } = render(<HfMarkdown content='' testId='md' />);
    expect(container.querySelector('[data-testid="md"]')).toBeNull();
  });
});
