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

/**
 * PricingTable — the wrapper that memoises the money columns and hands them
 * to Semi's Table.
 *
 * getPricingTableColumns is used for real here (not mocked): the interesting
 * property of this component is *when* it calls that factory, and a mock
 * would hide it.
 */

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, fireEvent } from '@testing-library/react';

vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

// Table shim publishes the column shape it was handed as attributes, so
// "which columns, pinned how, filtered by what" is assertable from the DOM
// rather than inferred from the fact that render() did not blow up.
vi.mock('@douyinfe/semi-ui', () => ({
  Card: ({ children }) =>
    React.createElement('div', { 'data-testid': 'card' }, children),
  Empty: ({ description, image }) =>
    React.createElement('div', { 'data-testid': 'empty' }, image, description),
  Table: ({ columns, dataSource, loading, scroll, onRow, empty, pagination }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'table',
        'data-loading': String(!!loading),
        'data-row-count': String((dataSource || []).length),
        'data-scroll-x': scroll ? String(scroll.x) : 'none',
        'data-page-size': String(pagination?.pageSize),
        'data-columns': JSON.stringify(
          (columns || []).map((c) => ({
            dataIndex: c.dataIndex,
            fixed: c.fixed ?? null,
            filteredValue: c.filteredValue ?? null,
          })),
        ),
      },
      React.createElement('div', { 'data-testid': 'table-empty' }, empty),
      (dataSource || []).map((record, i) =>
        React.createElement('button', {
          type: 'button',
          key: record.key ?? i,
          'data-testid': `row-${record.key ?? i}`,
          onClick: onRow?.(record)?.onClick,
        }),
      ),
    ),
  Tag: ({ children }) =>
    React.createElement('span', { 'data-testid': 'tag' }, children),
  Space: ({ children }) => React.createElement('div', null, children),
  Tooltip: ({ children }) => React.createElement('span', null, children),
  Toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() },
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () =>
    React.createElement('i', { 'data-testid': 'no-result-illustration' }),
  IllustrationNoResultDark: () =>
    React.createElement('i', { 'data-testid': 'no-result-illustration-dark' }),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconHelpCircle: (props) =>
    React.createElement('i', { 'data-testid': 'help-icon', ...props }),
}));

vi.mock('../../../../common/ui/RenderUtils', () => ({
  renderLimitedItems: ({ items }) =>
    React.createElement(
      'div',
      { 'data-testid': 'limited-items' },
      items.length,
    ),
  renderDescription: (text) =>
    React.createElement('span', { 'data-testid': 'description' }, text || '-'),
}));

vi.mock('../../../../../helpers', async () => {
  const utils = await vi.importActual('../../../../../helpers/utils.jsx');
  return {
    calculateModelPrice: utils.calculateModelPrice,
    renderModelTag: (name) =>
      React.createElement('span', { 'data-testid': 'model-tag' }, name),
    stringToColor: (s) => `color-${s}`,
    getLobeHubIcon: () => React.createElement('i', null),
  };
});

import PricingTable from './PricingTable';

const t = (k) => k;
const displayPrice = (usd) => `$${usd.toFixed(3)}`;

const model = (name) => ({
  key: name,
  model_name: name,
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
  supported_endpoint_types: [],
});

const baseProps = (over = {}) => ({
  filteredModels: [model('a')],
  loading: false,
  rowSelection: null,
  pageSize: 20,
  setPageSize: vi.fn(),
  selectedGroup: 'default',
  groupRatio: { default: 1 },
  copyText: vi.fn(),
  setModalImageUrl: vi.fn(),
  setIsModalOpenurl: vi.fn(),
  currency: 'USD',
  tokenUnit: 'M',
  displayPrice,
  searchValue: '',
  showRatio: false,
  compactMode: false,
  openModelDetail: vi.fn(),
  t,
  ...over,
});

const columnsOf = (container) =>
  JSON.parse(
    container
      .querySelector('[data-testid="table"]')
      .getAttribute('data-columns'),
  );

let consoleErrorSpy;
beforeEach(() => {
  // React logs the hook-order violation through console.error before it
  // rethrows; silence it so the intentional-failure test is not noisy.
  consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
});
afterEach(() => {
  consoleErrorSpy.mockRestore();
});

describe('PricingTable — column plumbing', () => {
  it('hands the full money column set to the table', () => {
    const { container } = render(<PricingTable {...baseProps()} />);
    expect(columnsOf(container).map((c) => c.dataIndex)).toEqual([
      'model_name',
      'vendor_name',
      'description',
      'tags',
      'quota_type',
      'supported_endpoint_types',
      'model_price',
    ]);
  });

  it('adds the ratio column when showRatio is on', () => {
    const { container } = render(
      <PricingTable {...baseProps({ showRatio: true })} />,
    );
    expect(columnsOf(container).map((c) => c.dataIndex)).toContain(
      'model_ratio',
    );
  });

  // Positive/negative pair: the search term must land on the model-name column
  // and nowhere else, or the table filters on the wrong field.
  it('routes searchValue to the model_name column only', () => {
    const { container } = render(
      <PricingTable {...baseProps({ searchValue: 'gpt' })} />,
    );
    const columns = columnsOf(container);
    expect(
      columns.find((c) => c.dataIndex === 'model_name').filteredValue,
    ).toEqual(['gpt']);
    expect(
      columns.find((c) => c.dataIndex === 'model_price').filteredValue,
    ).toBeNull();
  });

  it('clears the model_name filter when the search box is emptied', () => {
    const { container } = render(<PricingTable {...baseProps()} />);
    expect(
      columnsOf(container).find((c) => c.dataIndex === 'model_name')
        .filteredValue,
    ).toEqual([]);
  });

  it('pins the price column on desktop but strips the pin in compact mode', () => {
    const { container: wide } = render(<PricingTable {...baseProps()} />);
    expect(
      columnsOf(wide).find((c) => c.dataIndex === 'model_price').fixed,
    ).toBe('right');

    const { container: compact } = render(
      <PricingTable {...baseProps({ compactMode: true })} />,
    );
    expect(
      columnsOf(compact).find((c) => c.dataIndex === 'model_price').fixed,
    ).toBeNull();
  });

  // The viewport query lives here now (the column factory takes isMobile as a
  // plain argument), so this is the only place the mobile wiring is provable.
  it('unpins the price column when the viewport is mobile', () => {
    const originalMatchMedia = window.matchMedia;
    window.matchMedia = (query) => ({
      matches: true,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
    try {
      const { container } = render(<PricingTable {...baseProps()} />);
      expect(
        columnsOf(container).find((c) => c.dataIndex === 'model_price').fixed,
      ).toBeNull();
    } finally {
      window.matchMedia = originalMatchMedia;
    }
  });

  it('drops horizontal scroll in compact mode and keeps it otherwise', () => {
    const { container: wide } = render(<PricingTable {...baseProps()} />);
    expect(wide.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-scroll-x',
      'max-content',
    );

    const { container: compact } = render(
      <PricingTable {...baseProps({ compactMode: true })} />,
    );
    expect(compact.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-scroll-x',
      'none',
    );
  });

  it('forwards loading, page size and row count to the table', () => {
    const { container } = render(
      <PricingTable
        {...baseProps({
          loading: true,
          pageSize: 50,
          filteredModels: [model('a'), model('b')],
        })}
      />,
    );
    const table = container.querySelector('[data-testid="table"]');
    expect(table).toHaveAttribute('data-loading', 'true');
    expect(table).toHaveAttribute('data-page-size', '50');
    expect(table).toHaveAttribute('data-row-count', '2');
  });

  it('opens the model detail sheet with the clicked record', () => {
    const openModelDetail = vi.fn();
    const records = [model('a'), model('b')];
    const { container } = render(
      <PricingTable
        {...baseProps({ filteredModels: records, openModelDetail })}
      />,
    );
    fireEvent.click(container.querySelector('[data-testid="row-b"]'));
    expect(openModelDetail).toHaveBeenCalledTimes(1);
    expect(openModelDetail).toHaveBeenCalledWith(records[1]);
  });

  it('renders the empty-state copy for an empty result set', () => {
    const { container } = render(
      <PricingTable {...baseProps({ filteredModels: [] })} />,
    );
    expect(
      container.querySelector('[data-testid="table-empty"]').textContent,
    ).toContain('搜索无结果');
    expect(
      container.querySelector('[data-testid="no-result-illustration"]'),
    ).not.toBeNull();
  });
});

// ----------------------------------------------------------------------
// DEFECT: getPricingTableColumns (PricingTableColumns.jsx:115) calls the
// useIsMobile() hook, and PricingTable.jsx:48 calls that factory from inside
// a useMemo. The factory only runs when the memo's deps change, so on any
// re-render where they do not — a new page of models, a new selection, a
// parent state change — the hook is skipped and React's hook list comes up
// short. React then throws "Rendered fewer hooks than expected" and unmounts
// the tree: the whole pricing table disappears mid-session.
//
// Fix direction: hoist useIsMobile() into PricingTable (or into a component
// wrapper) and pass isMobile into the factory as a plain argument.
// ----------------------------------------------------------------------
describe('PricingTable — hook safety across re-renders', () => {
  it('CONTRACT: survives a re-render that does not invalidate the column memo', () => {
    const props = baseProps();
    const { rerender, container } = render(<PricingTable {...props} />);
    // Only filteredModels changes; every dep of the columns useMemo is stable.
    rerender(
      <PricingTable {...props} filteredModels={[model('a'), model('b')]} />,
    );
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '2',
    );
  });

  // NOTE: there is deliberately no live test asserting that the re-render
  // *throws*. Such a test passes today and turns RED the moment the defect
  // above is repaired, which makes the repair read as a regression. The
  // skipped CONTRACT lock records the defect; the test below is the live
  // invariant that must hold before and after the fix.

  // Negative half of the pair: prove the crash is caused by the memo being
  // *skipped*, not by re-rendering as such. Changing a column dep re-runs the
  // factory, the hook is called again, and the same re-render is fine.
  it('re-renders cleanly when a column dependency also changes', () => {
    const props = baseProps();
    const { rerender, container } = render(<PricingTable {...props} />);
    rerender(
      <PricingTable
        {...props}
        filteredModels={[model('a'), model('b')]}
        showRatio={true}
      />,
    );
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '2',
    );
  });
});
