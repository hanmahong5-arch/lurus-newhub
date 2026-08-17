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

const captured = vi.hoisted(() => ({ card: null }));

vi.mock('@douyinfe/semi-icons', () => ({
  IconEyeOpened: () => React.createElement('span', null, 'eye-open'),
  IconEyeClosed: () => React.createElement('span', null, 'eye-closed'),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    Card: (props) => {
      captured.card = props;
      return h(
        'section',
        null,
        h('div', { 'data-testid': 'card-title' }, props.title),
        h('div', { 'data-testid': 'card-body' }, props.children),
        h('div', { 'data-testid': 'card-footer' }, props.footer),
      );
    },
    Divider: () => h('hr', { 'data-testid': 'divider' }),
    Button: ({ onClick, icon, children }) =>
      h('button', { type: 'button', onClick }, icon, children),
    Typography: { Text: ({ children }) => h('span', null, children) },
  };
});

import CardPro from './CardPro';

let mobile = false;
beforeEach(() => {
  mobile = false;
  captured.card = null;
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

const title = () => screen.getByTestId('card-title');
const footer = () => screen.getByTestId('card-footer');

describe('CardPro — area routing per layout type', () => {
  it('type1 shows description + actions + search, and ignores stats', () => {
    render(
      <CardPro
        type='type1'
        statsArea={<div>stats-block</div>}
        descriptionArea={<div>description-block</div>}
        tabsArea={<div>tabs-block</div>}
        actionsArea={<div>actions-block</div>}
        searchArea={<div>search-block</div>}
      >
        <div>table-body</div>
      </CardPro>,
    );

    expect(title()).toHaveTextContent('description-block');
    expect(title()).toHaveTextContent('actions-block');
    expect(title()).toHaveTextContent('search-block');
    expect(title()).not.toHaveTextContent('stats-block');
    expect(title()).not.toHaveTextContent('tabs-block');
    expect(screen.getByTestId('card-body')).toHaveTextContent('table-body');
  });

  it('type2 shows stats + search and drops description, tabs and actions', () => {
    render(
      <CardPro
        type='type2'
        statsArea={<div>stats-block</div>}
        descriptionArea={<div>description-block</div>}
        tabsArea={<div>tabs-block</div>}
        actionsArea={<div>actions-block</div>}
        searchArea={<div>search-block</div>}
      />,
    );

    expect(title()).toHaveTextContent('stats-block');
    expect(title()).toHaveTextContent('search-block');
    expect(title()).not.toHaveTextContent('description-block');
    expect(title()).not.toHaveTextContent('actions-block');
  });

  it('type3 adds the tabs strip on top of the type1 areas', () => {
    render(
      <CardPro
        type='type3'
        descriptionArea={<div>description-block</div>}
        tabsArea={<div>tabs-block</div>}
        actionsArea={<div>actions-block</div>}
      />,
    );

    expect(title()).toHaveTextContent('tabs-block');
    expect(title()).toHaveTextContent('description-block');
    expect(title()).toHaveTextContent('actions-block');
  });

  it('renders no header at all when every area is empty', () => {
    render(
      <CardPro>
        <div>only-body</div>
      </CardPro>,
    );

    expect(captured.card.title).toBeNull();
    expect(screen.getByTestId('card-body')).toHaveTextContent('only-body');
  });
});

describe('CardPro — dividers and footer', () => {
  it('separates description from the action block', () => {
    render(
      <CardPro
        type='type1'
        descriptionArea={<div>d</div>}
        actionsArea={<div>a</div>}
        searchArea={<div>s</div>}
      />,
    );

    // One after the description, one between actions and search.
    expect(screen.getAllByTestId('divider')).toHaveLength(2);
  });

  it('skips the leading divider when there is no description or stats', () => {
    render(<CardPro type='type1' searchArea={<div>s</div>} />);

    expect(screen.queryAllByTestId('divider')).toHaveLength(0);
  });

  it('interleaves dividers between multiple action rows', () => {
    render(
      <CardPro
        type='type1'
        actionsArea={[<div key='a'>row-a</div>, <div key='b'>row-b</div>]}
      />,
    );

    expect(title()).toHaveTextContent('row-a');
    expect(title()).toHaveTextContent('row-b');
    // Exactly one divider — between the two rows, not before the first.
    expect(screen.getAllByTestId('divider')).toHaveLength(1);
  });

  it('renders the pagination area as the card footer, or nothing', () => {
    const { unmount } = render(
      <CardPro paginationArea={<div>pager</div>}>body</CardPro>,
    );
    expect(footer()).toHaveTextContent('pager');
    unmount();

    render(<CardPro>body</CardPro>);
    expect(captured.card.footer).toBeNull();
  });

  it('forwards card styling props through to Semi Card', () => {
    render(
      <CardPro
        className='my-card'
        shadows='hover'
        bordered={false}
        style={{ width: 10 }}
      />,
    );

    expect(captured.card.className).toContain('my-card');
    expect(captured.card.shadows).toBe('hover');
    expect(captured.card.bordered).toBe(false);
    expect(captured.card.style).toEqual({ width: 10 });
  });
});

describe('CardPro — mobile action toggle', () => {
  beforeEach(() => {
    mobile = true;
  });

  it('hides the action/search block behind a toggle', () => {
    render(
      <CardPro
        type='type1'
        actionsArea={<div>actions-block</div>}
        searchArea={<div>search-block</div>}
        t={(key) => key}
      />,
    );

    const toggle = screen.getByRole('button');
    expect(toggle).toHaveTextContent('显示操作项');

    fireEvent.click(toggle);
    expect(screen.getByRole('button')).toHaveTextContent('隐藏操作项');

    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByRole('button')).toHaveTextContent('显示操作项');
  });

  it('offers no toggle when there is nothing hideable', () => {
    render(
      <CardPro
        type='type2'
        statsArea={<div>stats-block</div>}
        t={(key) => key}
      />,
    );

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
