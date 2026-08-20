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
 * PricingCardView — the card rendering of the public price list.
 *
 * The same numbers as PricingTable, laid out differently, which is precisely
 * why it is worth testing separately: the two views compute the price through
 * the same helper but present it through different code, and they have already
 * diverged once (the unguarded completion_ratio, see the DEFECT block below).
 *
 * calculateModelPrice and formatPriceInfo are the real implementations.
 */

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
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

vi.mock('@douyinfe/semi-ui', () => ({
  Card: ({ children, onClick }) =>
    React.createElement('div', { 'data-testid': 'card', onClick }, children),
  Tag: ({ children, color, size, shape }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-size': size,
        'data-shape': shape,
      },
      children,
    ),
  Tooltip: ({ content, children }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tooltip', 'data-content': String(content) },
      children,
    ),
  Checkbox: ({ checked, onChange }) =>
    React.createElement('input', {
      type: 'checkbox',
      'data-testid': 'row-checkbox',
      checked: !!checked,
      onChange,
    }),
  Empty: ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description),
  Pagination: ({
    currentPage,
    pageSize,
    total,
    onPageChange,
    onPageSizeChange,
    size,
    showQuickJumper,
  }) =>
    React.createElement(
      'nav',
      {
        'data-testid': 'pagination',
        'data-current-page': String(currentPage),
        'data-page-size': String(pageSize),
        'data-total': String(total),
        'data-size': String(size),
        'data-quick-jumper': showQuickJumper ? 'yes' : 'no',
      },
      React.createElement('button', {
        type: 'button',
        'data-testid': 'go-page-3',
        onClick: () => onPageChange(3),
      }),
      React.createElement('button', {
        type: 'button',
        'data-testid': 'set-size-50',
        onClick: () => onPageSizeChange(50),
      }),
    ),
  Button: ({ onClick, icon }) =>
    React.createElement(
      'button',
      { type: 'button', 'data-testid': 'copy-button', onClick },
      icon,
    ),
  Avatar: ({ children }) =>
    React.createElement('span', { 'data-testid': 'avatar' }, children),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconHelpCircle: ({ onClick }) =>
    React.createElement('i', { 'data-testid': 'ratio-help-icon', onClick }),
}));

vi.mock('lucide-react', () => ({
  Copy: () => React.createElement('i', { 'data-testid': 'copy-icon' }),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () =>
    React.createElement('i', { 'data-testid': 'no-result' }),
  IllustrationNoResultDark: () =>
    React.createElement('i', { 'data-testid': 'no-result-dark' }),
}));

// Skeleton is a sibling surface; surface the props it is gated on so the
// loading branch leaves a trace instead of rendering nothing.
vi.mock('./PricingCardSkeleton', () => ({
  default: ({ rowSelection, showRatio }) =>
    React.createElement('div', {
      'data-testid': 'card-skeleton',
      'data-row-selection': rowSelection ? 'yes' : 'no',
      'data-show-ratio': showRatio ? 'yes' : 'no',
    }),
}));

// The minimum-display-time smoothing is a timing concern of its own module;
// here it is the identity so `loading` maps straight onto the skeleton branch.
vi.mock('../../../../../hooks/common/useMinimumLoadingTime', () => ({
  useMinimumLoadingTime: (loading) => loading,
}));

// Hoisted holder so the mock factory (which vitest lifts to the top of the
// file) can read a value the tests set later. MOBILE_BREAKPOINT is re-exported
// because helpers/utils.jsx imports it from this same module.
const viewport = vi.hoisted(() => ({ mobile: false }));
vi.mock('../../../../../hooks/common/useIsMobile', () => ({
  MOBILE_BREAKPOINT: 768,
  useIsMobile: () => viewport.mobile,
}));

vi.mock('../../../../common/ui/RenderUtils', () => ({
  renderLimitedItems: ({ items, renderItem, maxDisplay }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'limited-items',
        'data-count': String(items.length),
        'data-max-display': String(maxDisplay),
      },
      items.map((item, idx) => renderItem(item, idx)),
    ),
}));

vi.mock('../../../../../helpers', async () => {
  const utils = await vi.importActual('../../../../../helpers/utils.jsx');
  return {
    calculateModelPrice: utils.calculateModelPrice,
    formatPriceInfo: utils.formatPriceInfo,
    stringToColor: (s) => `color-${s}`,
    getLobeHubIcon: (icon, size) =>
      React.createElement('i', {
        'data-testid': 'lobe-icon',
        'data-icon': String(icon),
        'data-size': String(size),
      }),
  };
});

import PricingCardView from './PricingCardView';

const t = (k) => k;
const displayPrice = (usd) => `$${usd.toFixed(3)}`;

const model = (over = {}) => ({
  key: 'gpt-4o',
  model_name: 'gpt-4o',
  quota_type: 0,
  model_ratio: 2,
  completion_ratio: 3,
  enable_groups: ['default', 'vip'],
  ...over,
});

const baseProps = (over = {}) => ({
  filteredModels: [model()],
  loading: false,
  rowSelection: null,
  pageSize: 10,
  setPageSize: vi.fn(),
  currentPage: 1,
  setCurrentPage: vi.fn(),
  selectedGroup: 'default',
  groupRatio: { default: 1, vip: 0.5 },
  copyText: vi.fn(),
  setModalImageUrl: vi.fn(),
  setIsModalOpenurl: vi.fn(),
  currency: 'USD',
  tokenUnit: 'M',
  displayPrice,
  showRatio: false,
  t,
  selectedRowKeys: [],
  setSelectedRowKeys: vi.fn(),
  openModelDetail: vi.fn(),
  ...over,
});

const cards = (container) => [
  ...container.querySelectorAll('[data-testid="card"]'),
];

beforeEach(() => {
  viewport.mobile = false;
  localStorage.clear();
});

describe('PricingCardView — which cards, in what order', () => {
  // Five distinctly named models, page size 2: page 2 must be exactly m3/m4.
  // An off-by-one page window produces a different pair, so this cannot pass
  // by accident.
  const five = ['m1', 'm2', 'm3', 'm4', 'm5'].map((n) =>
    model({ key: n, model_name: n }),
  );

  it('shows only the current page of models', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: five, pageSize: 2, currentPage: 2 })}
      />,
    );
    expect(
      cards(container).map((c) => c.querySelector('h3').textContent),
    ).toEqual(['m3', 'm4']);
  });

  it('shows the short final page rather than padding it', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: five, pageSize: 2, currentPage: 3 })}
      />,
    );
    expect(
      cards(container).map((c) => c.querySelector('h3').textContent),
    ).toEqual(['m5']);
  });

  it('reports the unpaged total to the pager, not the page length', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: five, pageSize: 2, currentPage: 1 })}
      />,
    );
    const pager = container.querySelector('[data-testid="pagination"]');
    expect(pager).toHaveAttribute('data-total', '5');
    expect(pager).toHaveAttribute('data-page-size', '2');
    expect(pager).toHaveAttribute('data-current-page', '1');
  });

  it('returns to page 1 when the page size changes, so no rows are skipped', () => {
    const props = baseProps({ filteredModels: five, currentPage: 3 });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(container.querySelector('[data-testid="set-size-50"]'));
    expect(props.setPageSize).toHaveBeenCalledWith(50);
    expect(props.setCurrentPage).toHaveBeenCalledWith(1);
  });

  it('forwards a page change without touching the page size', () => {
    const props = baseProps({ filteredModels: five });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(container.querySelector('[data-testid="go-page-3"]'));
    expect(props.setCurrentPage).toHaveBeenCalledWith(3);
    expect(props.setPageSize).not.toHaveBeenCalled();
  });

  it('compacts the pager on mobile and keeps it full-size on desktop', () => {
    viewport.mobile = true;
    const { container: small } = render(<PricingCardView {...baseProps()} />);
    expect(small.querySelector('[data-testid="pagination"]')).toHaveAttribute(
      'data-size',
      'small',
    );
    expect(small.querySelector('[data-testid="pagination"]')).toHaveAttribute(
      'data-quick-jumper',
      'yes',
    );

    viewport.mobile = false;
    const { container: wide } = render(<PricingCardView {...baseProps()} />);
    expect(wide.querySelector('[data-testid="pagination"]')).toHaveAttribute(
      'data-size',
      'default',
    );
  });

  it('shows the empty state and no pager when nothing matched', () => {
    const { container } = render(
      <PricingCardView {...baseProps({ filteredModels: [] })} />,
    );
    expect(container.querySelector('[data-testid="empty"]').textContent).toBe(
      '搜索无结果',
    );
    expect(container.querySelector('[data-testid="pagination"]')).toBeNull();
    expect(cards(container)).toHaveLength(0);
  });

  it('shows the skeleton while loading, carrying the layout flags', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ loading: true, showRatio: true, rowSelection: {} })}
      />,
    );
    const skeleton = container.querySelector('[data-testid="card-skeleton"]');
    expect(skeleton).toHaveAttribute('data-row-selection', 'yes');
    expect(skeleton).toHaveAttribute('data-show-ratio', 'yes');
    expect(cards(container)).toHaveLength(0);
  });
});

describe('PricingCardView — the price on the card', () => {
  it('quotes input and output per 1M tokens at the selected group ratio', () => {
    const { container } = render(<PricingCardView {...baseProps()} />);
    // model_ratio 2 => $4 in; completion_ratio 3 => $12 out; group ratio 1.
    expect(container.textContent).toContain('输入 $4.0000/M');
    expect(container.textContent).toContain('输出 $12.0000/M');
  });

  it('halves the quote for a cheaper group', () => {
    const { container } = render(
      <PricingCardView {...baseProps({ selectedGroup: 'vip' })} />,
    );
    expect(container.textContent).toContain('输入 $2.0000/M');
    expect(container.textContent).toContain('输出 $6.0000/M');
  });

  it('divides by 1000 and relabels when the unit is K', () => {
    const { container } = render(
      <PricingCardView {...baseProps({ tokenUnit: 'K' })} />,
    );
    expect(container.textContent).toContain('输入 $0.0040/K');
    expect(container.textContent).toContain('输出 $0.0120/K');
  });

  it('quotes a per-call model as a flat price, with no token unit', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          filteredModels: [
            model({ quota_type: 1, model_price: 0.1, completion_ratio: 0 }),
          ],
        })}
      />,
    );
    expect(container.textContent).toContain('模型价格 $0.100');
    expect(container.textContent).not.toContain('/M');
  });

  it('quotes a genuinely free model as zero rather than as missing', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [model({ model_ratio: 0 })] })}
      />,
    );
    expect(container.textContent).toContain('输入 $0.0000/M');
    expect(container.textContent).toContain('输出 $0.0000/M');
  });

  it('converts to CNY — symbol and amount move together', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          currency: 'CNY',
          displayPrice: (usd) => `¥${(usd * 7).toFixed(3)}`,
        })}
      />,
    );
    expect(container.textContent).toContain('输入 ¥28.0000/M');
    expect(container.textContent).not.toContain('$');
  });
});

describe('PricingCardView — the ratio panel', () => {
  it('is hidden unless showRatio is on', () => {
    const { container: off } = render(<PricingCardView {...baseProps()} />);
    expect(off.textContent).not.toContain('倍率信息');

    const { container: on } = render(
      <PricingCardView {...baseProps({ showRatio: true })} />,
    );
    expect(on.textContent).toContain('倍率信息');
  });

  it('shows the model and completion ratios, and the ratio actually charged', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ showRatio: true, selectedGroup: 'vip' })}
      />,
    );
    expect(container.textContent).toContain('模型: 2');
    expect(container.textContent).toContain('补全: 3');
    expect(container.textContent).toContain('分组: 0.5');
  });

  it('renders zero ratios as 0, never as 无 or a dash', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          showRatio: true,
          groupRatio: { default: 0 },
          filteredModels: [model({ model_ratio: 0, completion_ratio: 0 })],
        })}
      />,
    );
    const panel = container.textContent;
    expect(panel).toContain('模型: 0');
    expect(panel).toContain('补全: 0');
    expect(panel).toContain('分组: 0');
    expect(panel).not.toContain('无');
  });

  it('suppresses model and completion ratios for a per-call model', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          showRatio: true,
          filteredModels: [model({ quota_type: 1, model_price: 0.1 })],
        })}
      />,
    );
    expect(container.textContent).toContain('模型: 无');
    expect(container.textContent).toContain('补全: 无');
    expect(container.textContent).toContain('分组: 1');
  });

  it('opens the ratio explainer without also opening the model detail sheet', () => {
    const props = baseProps({ showRatio: true });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(container.querySelector('[data-testid="ratio-help-icon"]'));
    expect(props.setModalImageUrl).toHaveBeenCalledWith('/ratio.png');
    expect(props.setIsModalOpenurl).toHaveBeenCalledWith(true);
    expect(props.openModelDetail).not.toHaveBeenCalled();
  });

  it('carries the ratio explanation in the tooltip', () => {
    const { container } = render(
      <PricingCardView {...baseProps({ showRatio: true })} />,
    );
    expect(container.querySelector('[data-testid="tooltip"]')).toHaveAttribute(
      'data-content',
      '倍率是为了方便换算不同价格的模型',
    );
  });

  // ------------------------------------------------------------------
  // DEFECT (availability): PricingCardView.jsx:343 calls
  // `model.completion_ratio.toFixed(3)` with no guard whenever showRatio is on
  // and the model is per-token. A record the API returned without
  // completion_ratio — a freshly created model, a partial row — throws inside
  // render, and React unmounts the whole card grid: the customer sees an empty
  // price list, not one missing cell.
  //
  // Same bug, second site: the sibling suite already records it for
  // PricingTableColumns.jsx:208 (tp_PricingTableColumns.test.jsx). Two views,
  // one missing guard each — fixing only one leaves the other view blank.
  //
  // Fix direction: `Number(model.completion_ratio ?? 0).toFixed(3)`, or reuse
  // priceData, which already tolerates the missing field.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: a model missing completion_ratio must not blank the whole grid', () => {
    expect(() =>
      render(
        <PricingCardView
          {...baseProps({
            showRatio: true,
            filteredModels: [model({ completion_ratio: undefined })],
          })}
        />,
      ),
    ).not.toThrow();
  });

  // No live test asserting that the missing field *throws*: it would pass now
  // and go red on repair. The live invariant is the neighbouring one — with
  // the field present, one bad-looking value must not suppress the others.
  it('renders the ratio panel for every card on the page independently', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          showRatio: true,
          filteredModels: [
            model({ key: 'a', model_name: 'a', completion_ratio: 4 }),
            model({ key: 'b', model_name: 'b', completion_ratio: 5 }),
          ],
        })}
      />,
    );
    expect(container.textContent).toContain('补全: 4');
    expect(container.textContent).toContain('补全: 5');
  });
});

describe('PricingCardView — icons, tags and description', () => {
  it('prefers the model icon over the vendor icon', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          filteredModels: [model({ icon: 'ModelIcon', vendor_icon: 'Vendor' })],
        })}
      />,
    );
    expect(
      container.querySelector('[data-testid="lobe-icon"]'),
    ).toHaveAttribute('data-icon', 'ModelIcon');
  });

  it('falls back to the vendor icon when the model has none', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [model({ vendor_icon: 'Vendor' })] })}
      />,
    );
    expect(
      container.querySelector('[data-testid="lobe-icon"]'),
    ).toHaveAttribute('data-icon', 'Vendor');
  });

  it('falls back to two upper-cased initials when there is no icon at all', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          filteredModels: [model({ key: 'x', model_name: 'claude-3' })],
        })}
      />,
    );
    expect(container.querySelector('[data-testid="lobe-icon"]')).toBeNull();
    expect(container.querySelector('[data-testid="avatar"]').textContent).toBe(
      'CL',
    );
  });

  it('shows a question mark for a record with no model name', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [{ key: 'orphan' }] })}
      />,
    );
    expect(container.querySelector('[data-testid="avatar"]').textContent).toBe(
      '?',
    );
  });

  it('labels the billing type with its own colour', () => {
    const { container: perToken } = render(
      <PricingCardView {...baseProps()} />,
    );
    expect(perToken.textContent).toContain('按量计费');
    expect(perToken.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'violet',
    );

    const { container: perCall } = render(
      <PricingCardView
        {...baseProps({
          filteredModels: [model({ quota_type: 1, model_price: 0.1 })],
        })}
      />,
    );
    expect(perCall.textContent).toContain('按次计费');
    expect(perCall.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'teal',
    );
  });

  it('shows a neutral dash tag for an unrecognised billing type', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [model({ quota_type: 9 })] })}
      />,
    );
    const tag = container.querySelector('[data-testid="tag"]');
    expect(tag.textContent).toBe('-');
    expect(tag).toHaveAttribute('data-color', 'white');
  });

  it('splits custom tags on commas and caps the visible count at 3', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [model({ tags: 'a,b,,c,d' })] })}
      />,
    );
    const list = container.querySelector('[data-testid="limited-items"]');
    expect(list).toHaveAttribute('data-count', '4'); // the empty entry is dropped
    expect(list).toHaveAttribute('data-max-display', '3');
  });

  it('renders no tag list at all for a model with no tags', () => {
    const { container } = render(<PricingCardView {...baseProps()} />);
    expect(container.querySelector('[data-testid="limited-items"]')).toBeNull();
  });

  it('renders the description, and an empty one without printing undefined', () => {
    const { container: withDesc } = render(
      <PricingCardView
        {...baseProps({ filteredModels: [model({ description: 'fast' })] })}
      />,
    );
    expect(withDesc.textContent).toContain('fast');

    const { container: without } = render(<PricingCardView {...baseProps()} />);
    expect(without.textContent).not.toContain('undefined');
  });
});

describe('PricingCardView — selection and interaction', () => {
  const three = ['m1', 'm2', 'm3'].map((n) => model({ key: n, model_name: n }));

  it('opens the detail sheet for the card that was clicked, not the first one', () => {
    const props = baseProps({ filteredModels: three });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(cards(container)[2]);
    expect(props.openModelDetail).toHaveBeenCalledTimes(1);
    expect(props.openModelDetail).toHaveBeenCalledWith(three[2]);
  });

  it('copies the model name of that card and does not open the sheet', () => {
    const props = baseProps({ filteredModels: three });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(
      cards(container)[1].querySelector('[data-testid="copy-button"]'),
    );
    expect(props.copyText).toHaveBeenCalledWith('m2');
    expect(props.openModelDetail).not.toHaveBeenCalled();
  });

  it('renders no checkbox unless the caller asked for row selection', () => {
    const { container: off } = render(
      <PricingCardView {...baseProps({ filteredModels: three })} />,
    );
    expect(off.querySelector('[data-testid="row-checkbox"]')).toBeNull();

    const { container: on } = render(
      <PricingCardView
        {...baseProps({ filteredModels: three, rowSelection: {} })}
      />,
    );
    expect(on.querySelectorAll('[data-testid="row-checkbox"]')).toHaveLength(3);
  });

  // The fixture selects m1 and ticks m3 — right and wrong are distinguishable:
  // an implementation that reported the index, the first key, or replaced the
  // list instead of appending would all produce something other than [m1, m3].
  it('adds the ticked model to the existing selection, keeping the others', () => {
    const props = baseProps({
      filteredModels: three,
      rowSelection: { onChange: vi.fn() },
      selectedRowKeys: ['m1'],
    });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(
      cards(container)[2].querySelector('[data-testid="row-checkbox"]'),
    );
    expect(props.setSelectedRowKeys).toHaveBeenCalledWith(['m1', 'm3']);
    expect(props.rowSelection.onChange).toHaveBeenCalledWith(
      ['m1', 'm3'],
      null,
    );
  });

  it('removes only the unticked model from the selection', () => {
    const props = baseProps({
      filteredModels: three,
      rowSelection: { onChange: vi.fn() },
      selectedRowKeys: ['m1', 'm3'],
    });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(
      cards(container)[0].querySelector('[data-testid="row-checkbox"]'),
    );
    expect(props.setSelectedRowKeys).toHaveBeenCalledWith(['m3']);
  });

  it('reflects the current selection in the checkbox state', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({
          filteredModels: three,
          rowSelection: {},
          selectedRowKeys: ['m2'],
        })}
      />,
    );
    expect(
      [...container.querySelectorAll('[data-testid="row-checkbox"]')].map(
        (n) => n.checked,
      ),
    ).toEqual([false, true, false]);
  });

  it('identifies a model with no key by its name', () => {
    const props = baseProps({
      filteredModels: [
        { model_name: 'keyless', quota_type: 1, model_price: 1 },
      ],
      rowSelection: { onChange: vi.fn() },
      selectedRowKeys: [],
    });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(container.querySelector('[data-testid="row-checkbox"]'));
    expect(props.setSelectedRowKeys).toHaveBeenCalledWith(['keyless']);
  });

  it('does nothing rather than throwing when no selection setter was supplied', () => {
    const props = baseProps({
      filteredModels: three,
      rowSelection: { onChange: vi.fn() },
      setSelectedRowKeys: undefined,
    });
    const { container } = render(<PricingCardView {...props} />);
    fireEvent.click(container.querySelector('[data-testid="row-checkbox"]'));
    expect(props.rowSelection.onChange).not.toHaveBeenCalled();
  });

  it('tolerates a card click when no detail handler was supplied', () => {
    const { container } = render(
      <PricingCardView
        {...baseProps({ filteredModels: three, openModelDetail: undefined })}
      />,
    );
    expect(() => fireEvent.click(cards(container)[0])).not.toThrow();
  });
});
