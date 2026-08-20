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
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    Space: ({ children }) => h('div', { 'data-testid': 'space' }, children),
    Tag: ({ children }) => h('span', { 'data-testid': 'tag' }, children),
    // Popover content is normally behind a hover; render it inline so the
    // overflow bucket is assertable.
    Popover: ({ content, children }) =>
      h(
        'span',
        null,
        h('span', { 'data-testid': 'popover-content' }, content),
        children,
      ),
    Typography: {
      Text: ({ children, style }) =>
        h('span', { style, 'data-testid': 'text' }, children),
    },
    Spin: ({ size, spinning }) =>
      h('div', {
        'data-testid': 'spin',
        'data-size': size,
        'data-spinning': String(!!spinning),
      }),
    Button: ({ onClick, children, className, ...rest }) =>
      h(
        'button',
        {
          type: 'button',
          onClick,
          className,
          'data-extra': rest['data-extra'],
        },
        children,
      ),
  };
});

import { renderLimitedItems, renderDescription } from './RenderUtils';
import CompactModeToggle from './CompactModeToggle';
import Loading from './Loading';

const chip = (item, idx) => <span key={idx}>{`chip-${item}`}</span>;

describe('renderLimitedItems', () => {
  it('returns a dash for nothing to show', () => {
    expect(renderLimitedItems({ items: [], renderItem: chip })).toBe('-');
    expect(renderLimitedItems({ items: null, renderItem: chip })).toBe('-');
    expect(renderLimitedItems({ items: undefined, renderItem: chip })).toBe(
      '-',
    );
  });

  it('renders every item inline while within the display budget', () => {
    render(renderLimitedItems({ items: ['a', 'b', 'c'], renderItem: chip }));

    expect(screen.getByText('chip-a')).toBeInTheDocument();
    expect(screen.getByText('chip-c')).toBeInTheDocument();
    expect(screen.queryByTestId('tag')).not.toBeInTheDocument();
  });

  it('parks the overflow in a popover behind a +N chip', () => {
    render(
      renderLimitedItems({
        items: ['a', 'b', 'c', 'd', 'e'],
        renderItem: chip,
      }),
    );

    expect(screen.getByTestId('tag')).toHaveTextContent('+2');
    const overflow = screen.getByTestId('popover-content');
    expect(overflow).toHaveTextContent('chip-d');
    expect(overflow).toHaveTextContent('chip-e');
    expect(overflow).not.toHaveTextContent('chip-a');
  });

  it('honours a caller-supplied maxDisplay', () => {
    render(
      renderLimitedItems({
        items: ['a', 'b', 'c', 'd'],
        renderItem: chip,
        maxDisplay: 1,
      }),
    );

    expect(screen.getByTestId('tag')).toHaveTextContent('+3');
    expect(screen.getByTestId('popover-content')).toHaveTextContent('chip-b');
  });

  it('passes the item and its index to renderItem', () => {
    const renderItem = vi.fn((item, idx) => <span key={idx}>{item}</span>);
    render(renderLimitedItems({ items: ['x', 'y'], renderItem }));

    expect(renderItem).toHaveBeenCalledTimes(2);
    expect(renderItem).toHaveBeenNthCalledWith(1, 'x', 0);
    expect(renderItem).toHaveBeenNthCalledWith(2, 'y', 1);
  });
});

describe('renderDescription', () => {
  it('renders the text with the default max width', () => {
    render(renderDescription('a long channel description'));

    const el = screen.getByTestId('text');
    expect(el).toHaveTextContent('a long channel description');
    expect(el).toHaveStyle({ maxWidth: '200px' });
  });

  it('accepts a custom max width', () => {
    render(renderDescription('text', 320));

    expect(screen.getByTestId('text')).toHaveStyle({ maxWidth: '320px' });
  });

  it('shows a dash for empty text', () => {
    render(renderDescription(''));

    expect(screen.getByTestId('text')).toHaveTextContent('-');
  });
});

describe('CompactModeToggle', () => {
  let mobile = false;
  beforeEach(() => {
    mobile = false;
    window.matchMedia = (query) => ({
      matches: mobile,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
    });
  });
  afterEach(() => {
    delete window.matchMedia;
  });

  it('offers to switch into compact mode when it is off', () => {
    const setCompactMode = vi.fn();
    render(
      <CompactModeToggle
        compactMode={false}
        setCompactMode={setCompactMode}
        t={(key) => key}
      />,
    );

    const btn = screen.getByRole('button', { name: '紧凑列表' });
    fireEvent.click(btn);
    expect(setCompactMode).toHaveBeenCalledWith(true);
  });

  it('offers to switch back out when compact mode is on', () => {
    const setCompactMode = vi.fn();
    render(
      <CompactModeToggle
        compactMode
        setCompactMode={setCompactMode}
        t={(key) => key}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '自适应列表' }));
    expect(setCompactMode).toHaveBeenCalledWith(false);
  });

  it('forwards extra props and merges the class name', () => {
    render(
      <CompactModeToggle
        compactMode={false}
        setCompactMode={vi.fn()}
        t={(key) => key}
        className='ml-2'
        data-extra='yes'
      />,
    );

    const btn = screen.getByRole('button');
    expect(btn.className).toBe('w-full md:w-auto ml-2');
    expect(btn).toHaveAttribute('data-extra', 'yes');
  });

  it('renders nothing on mobile', () => {
    mobile = true;
    const { container } = render(
      <CompactModeToggle
        compactMode={false}
        setCompactMode={vi.fn()}
        t={(key) => key}
      />,
    );

    expect(container).toBeEmptyDOMElement();
  });
});

describe('Loading', () => {
  it('spins at the small size by default', () => {
    render(<Loading />);

    const spin = screen.getByTestId('spin');
    expect(spin).toHaveAttribute('data-size', 'small');
    expect(spin).toHaveAttribute('data-spinning', 'true');
  });

  it('accepts a larger size', () => {
    render(<Loading size='large' />);

    expect(screen.getByTestId('spin')).toHaveAttribute('data-size', 'large');
  });
});
