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
 * getPricingTableColumns — the money column factory for the pricing table.
 *
 * Everything the customer reads before they spend goes through here: the
 * per-token input/output price, the unit it is quoted in, and the three
 * ratios (model / completion / group) that justify it. The real
 * `calculateModelPrice` is used deliberately — mocking it would turn these
 * into tests of the mock, and the whole risk in this file is that the number
 * rendered here disagrees with the number computed there.
 */

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// helpers/utils.jsx touches window.matchMedia at module scope, and useIsMobile
// subscribes to it. Stub before any import evaluates.
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

// Semi UI's barrel pulls lottie-web, which needs a canvas jsdom has not got.
// Each shim renders a MARKER carrying the props under test as attributes, so
// a branch that executes always leaves a trace in the DOM.
vi.mock('@douyinfe/semi-ui', () => ({
  Tag: ({ children, color, shape, size, prefixIcon }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-shape': shape,
        'data-size': size,
        'data-has-prefix-icon': prefixIcon ? 'yes' : 'no',
      },
      prefixIcon,
      children,
    ),
  Space: ({ children }) =>
    React.createElement('div', { 'data-testid': 'space' }, children),
  // content is surfaced, not dropped — the tooltip copy is the only place the
  // "what is a ratio" explanation lives.
  Tooltip: ({ content, children }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tooltip', 'data-content': String(content) },
      children,
    ),
  Toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() },
  Pagination: (props) =>
    React.createElement('nav', { 'data-testid': 'pagination', ...{} }),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconHelpCircle: ({ onClick, className }) =>
    React.createElement('i', {
      'data-testid': 'ratio-help-icon',
      className,
      onClick,
    }),
}));

// RenderUtils belongs to another surface; shim it to markers that echo what
// was handed in so "did the column pass the right items through" is provable.
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
  renderDescription: (text, maxWidth) =>
    React.createElement(
      'span',
      { 'data-testid': 'description', 'data-max-width': String(maxWidth) },
      text || '-',
    ),
}));

// Real calculateModelPrice, stubbed presentation helpers.
vi.mock('../../../../../helpers', async () => {
  const utils = await vi.importActual('../../../../../helpers/utils.jsx');
  return {
    calculateModelPrice: utils.calculateModelPrice,
    renderModelTag: (name, opts) =>
      React.createElement(
        'button',
        { type: 'button', 'data-testid': 'model-tag', onClick: opts?.onClick },
        name,
      ),
    stringToColor: (s) => `color-${s}`,
    getLobeHubIcon: (icon, size) =>
      React.createElement('i', {
        'data-testid': 'vendor-icon',
        'data-icon': icon,
        'data-size': String(size),
      }),
  };
});

import { getPricingTableColumns } from './PricingTableColumns';

const t = (k) => k;

// The real displayPrice from useModelPricingData, reproduced so the assertions
// below exercise the same USD -> display-currency path production uses.
const makeDisplayPrice = ({
  currency = 'USD',
  showWithRecharge = false,
  priceRate = 1,
  usdExchangeRate = 7,
  customSymbol = '¤',
  customRate = 1,
} = {}) => {
  return (usdPrice) => {
    let priceInUSD = usdPrice;
    if (showWithRecharge) {
      priceInUSD = (usdPrice * priceRate) / usdExchangeRate;
    }
    if (currency === 'CNY') {
      return `¥${(priceInUSD * usdExchangeRate).toFixed(3)}`;
    }
    if (currency === 'CUSTOM') {
      return `${customSymbol}${(priceInUSD * customRate).toFixed(3)}`;
    }
    return `$${priceInUSD.toFixed(3)}`;
  };
};

const perTokenModel = (over = {}) => ({
  key: 'gpt-4o',
  model_name: 'gpt-4o',
  quota_type: 0,
  model_ratio: 2,
  completion_ratio: 3,
  enable_groups: ['default', 'vip'],
  supported_endpoint_types: ['/v1/chat/completions'],
  vendor_name: 'OpenAI',
  vendor_icon: 'OpenAI',
  tags: 'vision,tools',
  description: 'a model',
  ...over,
});

const perCallModel = (over = {}) => ({
  key: 'midjourney',
  model_name: 'midjourney',
  quota_type: 1,
  model_ratio: 0,
  completion_ratio: 0,
  model_price: 0.1,
  enable_groups: ['default'],
  ...over,
});

// getPricingTableColumns is a plain factory — it takes isMobile as an argument
// instead of calling the hook itself, so it can be invoked outside a render.
const buildColumns = (opts = {}) =>
  getPricingTableColumns({
    t,
    selectedGroup: 'default',
    groupRatio: { default: 1, vip: 0.5 },
    copyText: vi.fn(),
    setModalImageUrl: vi.fn(),
    setIsModalOpenurl: vi.fn(),
    currency: 'USD',
    tokenUnit: 'M',
    displayPrice: makeDisplayPrice(),
    showRatio: true,
    isMobile: false,
    ...opts,
  });

const columnFor = (columns, dataIndex) =>
  columns.find((c) => c.dataIndex === dataIndex);

const renderCell = (column, record) => {
  const view = render(
    <div data-testid='cell'>
      {column.render(record[column.dataIndex], record, 0)}
    </div>,
  );
  // Scoped to this render's own container — several cells are rendered inside
  // one test, and a document-wide query would match all of them.
  return view.container.querySelector('[data-testid="cell"]');
};

beforeEach(() => {
  localStorage.clear();
});

describe('getPricingTableColumns — column set assembly', () => {
  it('emits the base columns, endpoints and price, in that order', () => {
    const columns = buildColumns({ showRatio: false });
    expect(columns.map((c) => c.dataIndex)).toEqual([
      'model_name',
      'vendor_name',
      'description',
      'tags',
      'quota_type',
      'supported_endpoint_types',
      'model_price',
    ]);
  });

  // Positive/negative pair: showRatio must *gate* the ratio column, not merely
  // be present in the code path.
  it('inserts the ratio column only when showRatio is on', () => {
    expect(buildColumns({ showRatio: true }).map((c) => c.dataIndex)).toContain(
      'model_ratio',
    );
    expect(
      buildColumns({ showRatio: false }).map((c) => c.dataIndex),
    ).not.toContain('model_ratio');
  });

  it('pins the price column to the right on desktop', () => {
    const columns = buildColumns({ isMobile: false });
    expect(columnFor(columns, 'model_price').fixed).toBe('right');
  });

  it('unpins the price column on mobile so it cannot cover the table', () => {
    const columns = buildColumns({ isMobile: true });
    expect(columnFor(columns, 'model_price').fixed).toBeUndefined();
  });
});

describe('price column — per-token quoting', () => {
  it('quotes input and output per 1M tokens at the selected group ratio', () => {
    // model_ratio 2 * 2 = $4 in, * completion_ratio 3 = $12 out, group ratio 1.
    const columns = buildColumns({ selectedGroup: 'default' });
    const cell = renderCell(columnFor(columns, 'model_price'), perTokenModel());
    expect(cell.textContent).toContain('输入 $4.0000 / 1M tokens');
    expect(cell.textContent).toContain('输出 $12.0000 / 1M tokens');
  });

  // The unit toggle must divide the price, not just relabel it. A regression
  // that changed only the label would leave "$4.0000 / 1K tokens" — 1000x the
  // real cost — and this catches exactly that.
  it('divides the price by 1000 when the unit is switched to K', () => {
    const columns = buildColumns({ tokenUnit: 'K' });
    const cell = renderCell(columnFor(columns, 'model_price'), perTokenModel());
    expect(cell.textContent).toContain('输入 $0.0040 / 1K tokens');
    expect(cell.textContent).toContain('输出 $0.0120 / 1K tokens');
  });

  it('halves the quoted price when a cheaper group is selected', () => {
    const columns = buildColumns({ selectedGroup: 'vip' }); // ratio 0.5
    const cell = renderCell(columnFor(columns, 'model_price'), perTokenModel());
    expect(cell.textContent).toContain('输入 $2.0000');
    expect(cell.textContent).toContain('输出 $6.0000');
  });

  // Currency conversion: the symbol AND the magnitude both have to move.
  // Asserting only the symbol would pass while the amount stayed in dollars.
  it('converts to CNY — symbol and amount move together', () => {
    const columns = buildColumns({
      currency: 'CNY',
      displayPrice: makeDisplayPrice({ currency: 'CNY', usdExchangeRate: 7 }),
    });
    const cell = renderCell(columnFor(columns, 'model_price'), perTokenModel());
    expect(cell.textContent).toContain('输入 ¥28.0000');
    expect(cell.textContent).toContain('输出 ¥84.0000');
    expect(cell.textContent).not.toContain('$');
  });

  it('renders a flat price (no /token suffix) for per-call models', () => {
    const columns = buildColumns();
    const cell = renderCell(columnFor(columns, 'model_price'), perCallModel());
    expect(cell.textContent).toContain('模型价格：$0.100');
    expect(cell.textContent).not.toContain('tokens');
  });

  it('scales the per-call price by the group ratio', () => {
    const columns = buildColumns({ selectedGroup: 'vip' });
    const cell = renderCell(
      columnFor(columns, 'model_price'),
      perCallModel({ enable_groups: ['default', 'vip'] }),
    );
    expect(cell.textContent).toContain('模型价格：$0.050');
  });

  // A free model must read as free, not as an em-dash or a blank cell.
  it('quotes a genuinely free (ratio 0) model as zero, not as missing', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'model_price'),
      perTokenModel({ model_ratio: 0 }),
    );
    expect(cell.textContent).toContain('输入 $0.0000');
    expect(cell.textContent).toContain('输出 $0.0000');
    expect(cell.textContent).not.toContain('-');
  });
});

describe('ratio column — the three ratios behind the price', () => {
  it('shows model, completion and group ratio for a per-token model', () => {
    const columns = buildColumns({ selectedGroup: 'vip' });
    const cell = renderCell(columnFor(columns, 'model_ratio'), perTokenModel());
    expect(cell.textContent).toContain('模型倍率：2');
    expect(cell.textContent).toContain('补全倍率：3');
    expect(cell.textContent).toContain('分组倍率：0.5');
  });

  it('rounds the completion ratio to 3 decimals', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'model_ratio'),
      perTokenModel({ completion_ratio: 1.23456789 }),
    );
    expect(cell.textContent).toContain('补全倍率：1.235');
  });

  it('suppresses model/completion ratio for per-call models (they do not apply)', () => {
    const columns = buildColumns();
    const cell = renderCell(columnFor(columns, 'model_ratio'), perCallModel());
    expect(cell.textContent).toContain('模型倍率：无');
    expect(cell.textContent).toContain('补全倍率：无');
    // The group ratio still applies to a per-call price, so it must stay.
    expect(cell.textContent).toContain('分组倍率：1');
  });

  // The group ratio displayed here is the one calculateModelPrice actually
  // used, not the one that was requested — that distinction is the whole
  // point of the field when the requested group has no ratio configured.
  it('displays the fallback group ratio that was actually applied', () => {
    const columns = buildColumns({
      selectedGroup: 'unconfigured',
      groupRatio: { default: 0.25, vip: 0.5 },
    });
    const cell = renderCell(columnFor(columns, 'model_ratio'), perTokenModel());
    expect(cell.textContent).toContain('分组倍率：0.25');
  });

  it('renders the ratio help tooltip copy and opens the explainer image', () => {
    const setModalImageUrl = vi.fn();
    const setIsModalOpenurl = vi.fn();
    const columns = buildColumns({ setModalImageUrl, setIsModalOpenurl });
    render(<div>{columnFor(columns, 'model_ratio').title()}</div>);

    expect(screen.getByTestId('tooltip')).toHaveAttribute(
      'data-content',
      '倍率是为了方便换算不同价格的模型',
    );
    fireEvent.click(screen.getByTestId('ratio-help-icon'));
    expect(setModalImageUrl).toHaveBeenCalledWith('/ratio.png');
    expect(setIsModalOpenurl).toHaveBeenCalledWith(true);
  });

  // ------------------------------------------------------------------
  // DEFECT: `record.completion_ratio.toFixed(3)` is called with no guard
  // (PricingTableColumns.jsx:208, and again in PricingCardView.jsx:343).
  // A model row whose completion_ratio the API omitted — new model, partial
  // record, per-call model saved without the field — throws inside render.
  // React unmounts the whole subtree on a render throw, so ONE bad row blanks
  // the ENTIRE pricing table: the customer sees no prices at all rather than
  // one missing cell.
  // ------------------------------------------------------------------
  it('CONTRACT: a row missing completion_ratio must still render the other ratios', () => {
    const columns = buildColumns();
    const record = perTokenModel({ completion_ratio: undefined });
    let view;
    expect(() => {
      view = render(
        <div data-testid='cell'>
          {columnFor(columns, 'model_ratio').render(2, record, 0)}
        </div>,
      );
    }).not.toThrow();
    // The two ratios the record does carry still reach the customer; only the
    // absent one reads as unknown. It must NOT read as 0 — a completion ratio
    // of 0 means free output, which is a price claim we cannot make here.
    const cell = view.container.querySelector('[data-testid="cell"]');
    expect(cell.textContent).toContain('模型倍率：2');
    expect(cell.textContent).toContain('分组倍率：1');
    expect(cell.textContent).toContain('补全倍率：-');
  });

  // NOTE: there is deliberately no live test asserting that a row without
  // completion_ratio *throws*. Such a test passes today and turns RED as soon
  // as the guard above is added, i.e. it would make the repair look like a
  // regression. The skipped CONTRACT lock records the defect; what stays live
  // is the invariant that holds before and after the fix — a ratio of 0 is a
  // real ratio and must render as 0, never as 无 or a dash.
  // All three ratios are exercised at 0 on purpose: `value || fallback` would
  // silently replace a legitimate zero with 无 / '-', and a free model or a
  // free group would then read as "not applicable" instead of "free".
  it('renders legitimate zero ratios as 0, not as missing', () => {
    const columns = buildColumns({ groupRatio: { default: 0 } });
    const cell = renderCell(
      columnFor(columns, 'model_ratio'),
      perTokenModel({ model_ratio: 0, completion_ratio: 0 }),
    );
    expect(cell.textContent).toContain('模型倍率：0');
    expect(cell.textContent).toContain('补全倍率：0');
    expect(cell.textContent).toContain('分组倍率：0');
    expect(cell.textContent).not.toContain('无');
    expect(cell.textContent).not.toContain('-');
  });
});

describe('quota type column', () => {
  it('labels per-call as 按次计费 and per-token as 按量计费, with distinct colours', () => {
    const columns = buildColumns();
    const column = columnFor(columns, 'quota_type');

    const perCall = renderCell(column, { quota_type: 1 });
    expect(perCall.textContent).toBe('按次计费');
    expect(perCall.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'teal',
    );

    const perToken = renderCell(column, { quota_type: 0 });
    expect(perToken.textContent).toBe('按量计费');
    expect(perToken.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'violet',
    );
  });

  it('parses a stringified quota type rather than falling through to 未知', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'quota_type'), { quota_type: '1' })
        .textContent,
    ).toBe('按次计费');
  });

  it('falls back to 未知 for an unrecognised billing type', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'quota_type'), { quota_type: 7 })
        .textContent,
    ).toBe('未知');
  });

  it('sorts per-token before per-call', () => {
    const { sorter } = columnFor(buildColumns(), 'quota_type');
    expect(sorter({ quota_type: 0 }, { quota_type: 1 })).toBeLessThan(0);
    expect(sorter({ quota_type: 1 }, { quota_type: 0 })).toBeGreaterThan(0);
    expect(sorter({ quota_type: 1 }, { quota_type: 1 })).toBe(0);
  });
});

describe('model name column', () => {
  it('copies the model name when the tag is clicked', () => {
    const copyText = vi.fn();
    const columns = buildColumns({ copyText });
    renderCell(columnFor(columns, 'model_name'), perTokenModel());
    fireEvent.click(screen.getByTestId('model-tag'));
    expect(copyText).toHaveBeenCalledWith('gpt-4o');
  });

  it('filters case-insensitively on a substring of the model name', () => {
    const { onFilter } = columnFor(buildColumns(), 'model_name');
    expect(onFilter('GPT', { model_name: 'gpt-4o' })).toBe(true);
    expect(onFilter('4O', { model_name: 'gpt-4o' })).toBe(true);
    expect(onFilter('claude', { model_name: 'gpt-4o' })).toBe(false);
  });
});

describe('vendor, tags, description and endpoint columns', () => {
  it('renders the vendor with its own icon', () => {
    const columns = buildColumns();
    const cell = renderCell(columnFor(columns, 'vendor_name'), perTokenModel());
    expect(cell.textContent).toContain('OpenAI');
    expect(cell.querySelector('[data-testid="vendor-icon"]')).toHaveAttribute(
      'data-icon',
      'OpenAI',
    );
  });

  it('falls back to the Layers icon when the vendor has none', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'vendor_name'),
      perTokenModel({ vendor_icon: '' }),
    );
    expect(cell.querySelector('[data-testid="vendor-icon"]')).toHaveAttribute(
      'data-icon',
      'Layers',
    );
  });

  it('renders a dash when there is no vendor at all', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'vendor_name'),
      perTokenModel({ vendor_name: '' }),
    );
    expect(cell.textContent).toBe('-');
    expect(cell.querySelector('[data-testid="tag"]')).toBeNull();
  });

  it('splits tags on commas, trims them, and caps the visible count at 3', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'tags'),
      perTokenModel({ tags: ' a , b ,c,d, ' }),
    );
    const list = cell.querySelector('[data-testid="limited-items"]');
    expect(list).toHaveAttribute('data-count', '4'); // trailing blank dropped
    expect(list).toHaveAttribute('data-max-display', '3');
    expect(
      [...list.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['a', 'b', 'c', 'd']);
  });

  it('renders a dash for a model with no tags', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'tags'), perTokenModel({ tags: '' }))
        .textContent,
    ).toBe('-');
  });

  it('clamps the description to 200px', () => {
    const columns = buildColumns();
    const cell = renderCell(columnFor(columns, 'description'), perTokenModel());
    expect(cell.querySelector('[data-testid="description"]')).toHaveAttribute(
      'data-max-width',
      '200',
    );
  });

  it('renders one tag per supported endpoint', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'supported_endpoint_types'),
      perTokenModel({
        supported_endpoint_types: ['/v1/chat/completions', '/v1/embeddings'],
      }),
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['/v1/chat/completions', '/v1/embeddings']);
  });

  it('renders nothing for a model with no endpoints (not an empty tag)', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'supported_endpoint_types'),
      perTokenModel({ supported_endpoint_types: [] }),
    );
    expect(cell.querySelector('[data-testid="tag"]')).toBeNull();
    expect(cell.textContent).toBe('');
  });
});
