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

vi.mock('@douyinfe/semi-icons', () => ({
  IconChevronDown: () => React.createElement('span', null, 'chev-down'),
  IconChevronUp: () => React.createElement('span', null, 'chev-up'),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  const Skeleton = ({ placeholder }) =>
    h('div', { 'data-testid': 'skeleton' }, placeholder);
  Skeleton.Title = () => h('div', { 'data-testid': 'skeleton-title' });
  return {
    Divider: ({ children }) => h('div', { 'data-testid': 'divider' }, children),
    Button: ({ onClick, disabled, type, theme, icon, children }) =>
      h(
        'button',
        {
          type: 'button',
          onClick,
          disabled: !!disabled,
          'data-semi-type': type,
          'data-semi-theme': theme,
        },
        icon,
        children,
      ),
    Tag: ({ children }) => h('span', { 'data-testid': 'tag' }, children),
    Row: ({ children }) => h('div', { 'data-testid': 'grid' }, children),
    Col: ({ span, children }) =>
      h('div', { 'data-testid': 'cell', 'data-span': String(span) }, children),
    Collapsible: ({ isOpen, children }) =>
      h(
        'div',
        { 'data-testid': 'collapsible', 'data-open': String(!!isOpen) },
        children,
      ),
    Checkbox: ({ checked, onChange, disabled }) =>
      h('input', {
        type: 'checkbox',
        checked: !!checked,
        disabled: !!disabled,
        onChange: () => onChange?.(),
      }),
    Skeleton,
    Tooltip: ({ content, children }) =>
      h('span', { 'data-tooltip': content }, children),
  };
});

import SelectableButtonGroup from './SelectableButtonGroup';

// useContainerWidth constructs a ResizeObserver unconditionally; jsdom has none.
class NoopResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// Container width drives the responsive column maths. jsdom reports 0 for every
// box, so override the measurement the hook takes on mount.
const withWidth = (width) => {
  const original = Element.prototype.getBoundingClientRect;
  Element.prototype.getBoundingClientRect = function rect() {
    return { width, height: 0, top: 0, left: 0, right: width, bottom: 0 };
  };
  return () => {
    Element.prototype.getBoundingClientRect = original;
  };
};

const ITEMS = [
  { value: 'openai', label: 'OpenAI', tagCount: 12 },
  { value: 'claude', label: 'Claude', tagCount: 4 },
  { value: 'gemini', label: 'Gemini', tagCount: 7 },
];

const buttons = () => screen.getAllByRole('button');
const labelOf = (btn) => btn.textContent;

beforeEach(() => {
  globalThis.ResizeObserver = NoopResizeObserver;
});
afterEach(() => {
  delete globalThis.ResizeObserver;
});

describe('SelectableButtonGroup — selection', () => {
  it('renders one button per item and reports the clicked value', () => {
    const onChange = vi.fn();
    render(
      <SelectableButtonGroup
        items={ITEMS}
        activeValue='openai'
        onChange={onChange}
      />,
    );

    expect(buttons()).toHaveLength(3);
    fireEvent.click(screen.getByText('Claude'));
    expect(onChange).toHaveBeenCalledWith('claude');
  });

  it('marks exactly the active item as primary', () => {
    render(
      <SelectableButtonGroup
        items={ITEMS}
        activeValue='claude'
        onChange={vi.fn()}
      />,
    );

    const active = buttons().filter(
      (b) => b.getAttribute('data-semi-type') === 'primary',
    );
    expect(active).toHaveLength(1);
    expect(labelOf(active[0])).toContain('Claude');
    expect(active[0].getAttribute('data-semi-theme')).toBe('light');
  });

  it('supports multi-select through an array activeValue', () => {
    render(
      <SelectableButtonGroup
        items={ITEMS}
        activeValue={['openai', 'gemini']}
        onChange={vi.fn()}
      />,
    );

    const active = buttons()
      .filter((b) => b.getAttribute('data-semi-type') === 'primary')
      .map(labelOf);
    expect(active).toHaveLength(2);
    expect(active.join()).toContain('OpenAI');
    expect(active.join()).toContain('Gemini');
  });

  it('nothing is active when activeValue matches no item', () => {
    render(
      <SelectableButtonGroup
        items={ITEMS}
        activeValue='none'
        onChange={vi.fn()}
      />,
    );

    expect(
      buttons().filter((b) => b.getAttribute('data-semi-type') === 'primary'),
    ).toHaveLength(0);
  });

  it('disables items flagged disabled and items with a zero tag count', () => {
    render(
      <SelectableButtonGroup
        items={[
          { value: 'a', label: 'A', disabled: true },
          { value: 'b', label: 'B', tagCount: 0 },
          { value: 'c', label: 'C', tagCount: 3 },
        ]}
        activeValue='c'
        onChange={vi.fn()}
      />,
    );

    const state = buttons().map((b) => [labelOf(b), b.disabled]);
    expect(state.find(([l]) => l.includes('A'))[1]).toBe(true);
    expect(state.find(([l]) => l.includes('B'))[1]).toBe(true);
    expect(state.find(([l]) => l.includes('C'))[1]).toBe(false);
  });

  it('renders an empty group without crashing', () => {
    render(<SelectableButtonGroup items={[]} onChange={vi.fn()} />);

    expect(screen.queryAllByRole('button')).toHaveLength(0);
    expect(screen.getByTestId('grid')).toBeInTheDocument();
  });
});

describe('SelectableButtonGroup — checkbox mode', () => {
  it('routes selection through the checkbox, not the button body', () => {
    const onChange = vi.fn();
    render(
      <SelectableButtonGroup
        items={ITEMS}
        activeValue={['openai']}
        onChange={onChange}
        withCheckbox
      />,
    );

    const boxes = screen.getAllByRole('checkbox');
    expect(boxes.map((b) => b.checked)).toEqual([true, false, false]);

    // Clicking the surrounding button is deliberately inert.
    fireEvent.click(screen.getByText('Claude'));
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(boxes[1]);
    expect(onChange).toHaveBeenCalledWith('claude');
  });

  it('disables the checkbox for an item with a zero tag count', () => {
    render(
      <SelectableButtonGroup
        items={[{ value: 'a', label: 'A', tagCount: 0 }]}
        activeValue={[]}
        onChange={vi.fn()}
        withCheckbox
      />,
    );

    expect(screen.getByRole('checkbox')).toBeDisabled();
  });
});

describe('SelectableButtonGroup — responsive layout', () => {
  it('uses a single full-width column when the container is very narrow', () => {
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    // No measurable width → 0 → the <=280px branch → 1 column of span 24.
    expect(
      screen.getAllByTestId('cell').map((c) => c.getAttribute('data-span')),
    ).toEqual(['24', '24', '24']);
    expect(screen.getAllByTestId('tag')).toHaveLength(3);
  });

  it('drops to two columns on a narrow container', () => {
    const restore = withWidth(320);
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    expect(screen.getAllByTestId('cell')[0].getAttribute('data-span')).toBe(
      '12',
    );
    expect(screen.getAllByTestId('tag')).toHaveLength(3);
    restore();
  });

  it('hides the count tags in the cramped three-column band', () => {
    const restore = withWidth(420);
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    expect(screen.getAllByTestId('cell')[0].getAttribute('data-span')).toBe(
      '8',
    );
    // 380 < width <= 460 is the one band that sacrifices the tags.
    expect(screen.queryAllByTestId('tag')).toHaveLength(0);
    restore();
  });

  it('brings the tags back once there is room', () => {
    const restore = withWidth(600);
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    expect(screen.getAllByTestId('cell')[0].getAttribute('data-span')).toBe(
      '8',
    );
    expect(screen.getAllByTestId('tag')).toHaveLength(3);
    restore();
  });

  it('omits the tag entirely for items that carry no count', () => {
    render(
      <SelectableButtonGroup
        items={[{ value: 'a', label: 'A' }]}
        onChange={vi.fn()}
      />,
    );

    expect(screen.queryAllByTestId('tag')).toHaveLength(0);
  });
});

describe('SelectableButtonGroup — collapsing', () => {
  const many = Array.from({ length: 9 }, (_, i) => ({
    value: `v${i}`,
    label: `item ${i}`,
  }));

  it('collapses long lists and toggles between expand and collapse', () => {
    render(
      <SelectableButtonGroup
        items={many}
        onChange={vi.fn()}
        t={(key) => key}
      />,
    );

    expect(screen.getByTestId('collapsible')).toHaveAttribute(
      'data-open',
      'false',
    );
    fireEvent.click(screen.getByText('展开更多'));

    expect(screen.getByTestId('collapsible')).toHaveAttribute(
      'data-open',
      'true',
    );
    expect(screen.getByText('收起')).toBeInTheDocument();
    expect(screen.queryByText('展开更多')).not.toBeInTheDocument();
  });

  it('never collapses when the caller opts out', () => {
    render(
      <SelectableButtonGroup
        items={many}
        onChange={vi.fn()}
        collapsible={false}
      />,
    );

    expect(screen.queryByTestId('collapsible')).not.toBeInTheDocument();
    expect(screen.queryByText('展开更多')).not.toBeInTheDocument();
  });

  it('derives the visible-row budget from collapseHeight', () => {
    // 64px / 32px per row = 2 rows; at 1 column that is 2 items, so 3 collapse.
    render(
      <SelectableButtonGroup
        items={ITEMS}
        onChange={vi.fn()}
        collapseHeight={64}
      />,
    );

    expect(screen.getByText('展开更多')).toBeInTheDocument();
  });

  it('leaves a list that fits uncollapsed', () => {
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    expect(screen.queryByTestId('collapsible')).not.toBeInTheDocument();
  });
});

describe('SelectableButtonGroup — title and loading', () => {
  it('renders the title in a divider', () => {
    render(
      <SelectableButtonGroup items={ITEMS} title='供应商' onChange={vi.fn()} />,
    );

    expect(screen.getByTestId('divider')).toHaveTextContent('供应商');
  });

  it('omits the divider when no title is given', () => {
    render(<SelectableButtonGroup items={ITEMS} onChange={vi.fn()} />);

    expect(screen.queryByTestId('divider')).not.toBeInTheDocument();
  });

  it('replaces the buttons with a skeleton while loading', () => {
    render(
      <SelectableButtonGroup
        items={ITEMS}
        title='供应商'
        onChange={vi.fn()}
        loading
      />,
    );

    expect(screen.getByTestId('skeleton')).toBeInTheDocument();
    expect(screen.queryByText('OpenAI')).not.toBeInTheDocument();
    // 12 placeholder rows, plus one standing in for the title.
    expect(screen.getAllByTestId('skeleton-title')).toHaveLength(13);
  });

  it('adds a checkbox placeholder per skeleton row in checkbox mode', () => {
    render(
      <SelectableButtonGroup
        items={ITEMS}
        onChange={vi.fn()}
        withCheckbox
        loading
      />,
    );

    // 12 rows × (checkbox + label) with no title placeholder.
    expect(screen.getAllByTestId('skeleton-title')).toHaveLength(24);
  });
});
