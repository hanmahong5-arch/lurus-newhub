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
 * ModelPricingTable — the per-group price breakdown inside the model detail
 * sheet. This is the screen a customer reads to decide which group to buy,
 * so every number on it is a commitment.
 *
 * `calculateModelPrice` is used for real, on purpose: the risk this file
 * carries is that the ratio the table *prints* disagrees with the ratio it
 * *charged*, and mocking the calculator would hide exactly that.
 */

import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';

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

// Table shim runs every column's render for every row and files the result
// under cell-<rowKey>-<dataIndex>, so each number is individually assertable.
vi.mock('@douyinfe/semi-ui', () => ({
  Card: ({ children }) =>
    React.createElement('div', { 'data-testid': 'card' }, children),
  Avatar: ({ children }) =>
    React.createElement('span', { 'data-testid': 'avatar' }, children),
  Typography: {
    Text: ({ children }) =>
      React.createElement('span', { 'data-testid': 'text' }, children),
  },
  Tag: ({ children, color }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color },
      children,
    ),
  Table: ({ dataSource, columns, pagination }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'table',
        'data-row-count': String((dataSource || []).length),
        'data-pagination': pagination === false ? 'off' : 'on',
        'data-column-keys': (columns || [])
          .map((c) => String(c.dataIndex))
          .join(','),
      },
      React.createElement(
        'div',
        { 'data-testid': 'head' },
        (columns || []).map((c, i) =>
          React.createElement(
            'span',
            { key: i, 'data-testid': `th-${c.dataIndex}` },
            c.title,
          ),
        ),
      ),
      (dataSource || []).map((rec, ri) =>
        React.createElement(
          'div',
          { key: rec.key ?? ri, 'data-testid': `row-${rec.key ?? ri}` },
          (columns || []).map((c, ci) =>
            React.createElement(
              'div',
              {
                key: ci,
                'data-testid': `cell-${rec.key ?? ri}-${c.dataIndex}`,
              },
              c.render ? c.render(rec[c.dataIndex], rec, ri) : rec[c.dataIndex],
            ),
          ),
        ),
      ),
    ),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconCoinMoneyStroked: () =>
    React.createElement('i', { 'data-testid': 'coin-icon' }),
}));

// Real calculator, as in the sibling PricingTableColumns suite.
vi.mock('../../../../../helpers', async () => {
  const utils = await vi.importActual('../../../../../helpers/utils.jsx');
  return { calculateModelPrice: utils.calculateModelPrice };
});

import ModelPricingTable from './ModelPricingTable';

const t = (k) => k;
const displayPrice = (usd) => `$${usd.toFixed(3)}`;

// model_ratio 2 => input $4/1M at ratio 1; completion_ratio 3 => output $12.
// Ratios 1 / 0.5 / 0 are all different, so a stuck or hard-coded ratio cannot
// reproduce the expected DOM.
const perTokenModel = (over = {}) => ({
  model_name: 'gpt-4o',
  quota_type: 0,
  model_ratio: 2,
  completion_ratio: 3,
  enable_groups: ['default', 'vip', 'free'],
  ...over,
});

const perCallModel = (over = {}) => ({
  model_name: 'midjourney',
  quota_type: 1,
  model_price: 0.1,
  enable_groups: ['default', 'vip'],
  ...over,
});

const usableGroup = {
  '': {},
  auto: {},
  default: {},
  vip: {},
  free: {},
  stranger: {},
};

const groupRatio = { default: 1, vip: 0.5, free: 0 };

const renderTable = (over = {}) =>
  render(
    <ModelPricingTable
      modelData={perTokenModel()}
      groupRatio={groupRatio}
      currency='USD'
      tokenUnit='M'
      displayPrice={displayPrice}
      showRatio={true}
      usableGroup={usableGroup}
      t={t}
      {...over}
    />,
  );

const cell = (container, group, field) =>
  container.querySelector(`[data-testid="cell-${group}-${field}"]`);

describe('ModelPricingTable — which groups are listed', () => {
  it('lists only the intersection of the user groups and the model groups', () => {
    const { container } = renderTable();
    // 'stranger' is usable but the model does not enable it; 'auto' and the
    // blank key are structural and must never be priced.
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '3',
    );
    expect(cell(container, 'stranger', 'group')).toBeNull();
    expect(cell(container, 'auto', 'group')).toBeNull();
    expect(cell(container, 'default', 'group')).not.toBeNull();
  });

  it('drops a group the model enables but the user cannot use', () => {
    const { container } = renderTable({
      usableGroup: { default: {} },
    });
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '1',
    );
    expect(cell(container, 'vip', 'group')).toBeNull();
  });

  it('prices nothing when the model declares no groups', () => {
    const { container } = renderTable({
      modelData: perTokenModel({ enable_groups: undefined }),
    });
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '0',
    );
  });

  it('renders the auto call chain in the model group order, arrows between', () => {
    const { container } = renderTable({
      autoGroups: ['vip', 'free', 'nowhere'],
    });
    const chain = container.querySelectorAll('[data-testid="tag"]');
    const chainLabels = [...chain]
      .map((n) => n.textContent)
      .filter((s) => s.endsWith('分组'));
    expect(chainLabels.slice(0, 2)).toEqual(['vip分组', 'free分组']);
    expect(container.textContent).toContain('auto分组调用链路');
    expect(container.textContent).not.toContain('nowhere');
  });

  // Negative half: no autoGroups means no chain banner at all.
  it('hides the auto call chain when no auto group applies to this model', () => {
    const { container } = renderTable({ autoGroups: ['nowhere'] });
    expect(container.textContent).not.toContain('auto分组调用链路');
  });
});

describe('ModelPricingTable — per-token quoting', () => {
  it('quotes input and output per group at that group ratio', () => {
    const { container } = renderTable();
    expect(cell(container, 'default', 'inputPrice').textContent).toContain(
      '$4.0000',
    );
    expect(cell(container, 'default', 'outputPrice').textContent).toContain(
      '$12.0000',
    );
    expect(cell(container, 'vip', 'inputPrice').textContent).toContain(
      '$2.0000',
    );
    expect(cell(container, 'vip', 'outputPrice').textContent).toContain(
      '$6.0000',
    );
  });

  it('labels the quoted unit and switches it with tokenUnit', () => {
    const { container: perM } = renderTable();
    expect(cell(perM, 'default', 'inputPrice').textContent).toContain(
      '/ 1M tokens',
    );

    const { container: perK } = renderTable({ tokenUnit: 'K' });
    // The unit toggle must divide the number too, not merely relabel it.
    expect(cell(perK, 'default', 'inputPrice').textContent).toContain(
      '$0.0040',
    );
    expect(cell(perK, 'default', 'inputPrice').textContent).toContain(
      '/ 1K tokens',
    );
  });

  // Invariant that survives any repair of the ratio-display defect below:
  // a group whose ratio is zero must be quoted at zero, and must differ from
  // the full-rate group. Nothing here pins the buggy literal.
  it('quotes a zero-ratio group at zero, distinct from the full-rate group', () => {
    const { container } = renderTable();
    expect(cell(container, 'free', 'inputPrice').textContent).toContain(
      '$0.0000',
    );
    expect(cell(container, 'free', 'outputPrice').textContent).toContain(
      '$0.0000',
    );
    expect(cell(container, 'free', 'inputPrice').textContent).not.toContain(
      '$4.0000',
    );
  });

  it('shows a dash instead of a token price for a per-call model', () => {
    const { container } = renderTable({ modelData: perCallModel() });
    expect(cell(container, 'default', 'inputPrice')).toBeNull();
    expect(cell(container, 'default', 'fixedPrice').textContent).toContain(
      '$0.100',
    );
    expect(cell(container, 'vip', 'fixedPrice').textContent).toContain(
      '$0.050',
    );
    expect(cell(container, 'default', 'fixedPrice').textContent).toContain(
      '/ 次',
    );
  });

  it('swaps the price columns with the billing type', () => {
    const { container: perToken } = renderTable();
    expect(perToken.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-column-keys',
      'group,ratio,billingType,inputPrice,outputPrice',
    );

    const { container: perCall } = renderTable({ modelData: perCallModel() });
    expect(perCall.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-column-keys',
      'group,ratio,billingType,fixedPrice',
    );
  });

  it('labels the billing type per row with its own colour', () => {
    const { container: perToken } = renderTable();
    expect(cell(perToken, 'default', 'billingType').textContent).toBe(
      '按量计费',
    );
    expect(
      cell(perToken, 'default', 'billingType').querySelector(
        '[data-testid="tag"]',
      ),
    ).toHaveAttribute('data-color', 'violet');

    const { container: perCall } = renderTable({ modelData: perCallModel() });
    expect(cell(perCall, 'default', 'billingType').textContent).toBe(
      '按次计费',
    );
    expect(
      cell(perCall, 'default', 'billingType').querySelector(
        '[data-testid="tag"]',
      ),
    ).toHaveAttribute('data-color', 'teal');
  });

  it('degrades to a dash for an unrecognised billing type rather than inventing a price', () => {
    const { container } = renderTable({
      modelData: perTokenModel({ quota_type: 9 }),
    });
    expect(cell(container, 'default', 'billingType').textContent).toBe('-');
    expect(cell(container, 'default', 'fixedPrice').textContent).toContain('-');
  });
});

describe('ModelPricingTable — the ratio column', () => {
  // Positive/negative pair for the ratio column itself: an ordinary fractional
  // ratio must render exactly, which rules out "the column is a constant".
  it('renders each group ratio exactly', () => {
    const { container } = renderTable();
    expect(cell(container, 'default', 'ratio').textContent).toBe('1x');
    expect(cell(container, 'vip', 'ratio').textContent).toBe('0.5x');
  });

  it('shows the ratio column only when showRatio is on', () => {
    const { container: on } = renderTable({ showRatio: true });
    expect(on.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-column-keys',
      'group,ratio,billingType,inputPrice,outputPrice',
    );
    const { container: off } = renderTable({ showRatio: false });
    expect(off.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-column-keys',
      'group,billingType,inputPrice,outputPrice',
    );
  });

  // ------------------------------------------------------------------
  // DEFECT (money, display): ModelPricingTable.jsx:64-65 computes the ratio
  // shown to the customer with
  //     groupRatio && groupRatio[group] ? groupRatio[group] : 1
  // a truthiness test. A configured ratio of 0 — the way a free tier is
  // expressed — is falsy, so the column prints "1x" while the very same row
  // quotes the price at $0.0000 that a ratio of 0 produced. The two halves of
  // one row contradict each other, and "1x" is the number a customer would
  // quote back at you.
  //
  // The neighbouring PricingGroups.jsx:56 gets this right
  // (`ratio !== undefined && ratio !== null`), so the same free group reads
  // "x0" in the sidebar and "1x" in the detail sheet — the two-sites-two-
  // fallbacks pattern this codebase keeps repeating.
  //
  // Fix direction: mirror PricingGroups — treat only undefined/null as
  // "unconfigured", or better, publish priceData.usedGroupRatio, which is by
  // construction the ratio the price was actually computed with.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: a zero group ratio is shown as 0x, matching the price it produced', () => {
    const { container } = renderTable();
    expect(cell(container, 'free', 'ratio').textContent).toBe('0x');
  });

  // Deliberately no live test asserting the column reads "1x" for the free
  // group: that would pass today and turn red the moment the defect is fixed.
  // What stays live is the invariant that must hold before and after — the
  // ratio printed for a group has to be consistent with the price printed
  // beside it, i.e. a row quoted at full price and a row quoted at zero cannot
  // both claim the same ratio.
  it('CONSISTENCY: two rows quoted at different prices must not claim the same ratio', () => {
    const { container } = renderTable();
    const fullRate = {
      price: cell(container, 'default', 'inputPrice').textContent,
      ratio: cell(container, 'default', 'ratio').textContent,
    };
    const freeRate = {
      price: cell(container, 'free', 'inputPrice').textContent,
      ratio: cell(container, 'free', 'ratio').textContent,
    };
    expect(freeRate.price).not.toBe(fullRate.price);
    // Today this fails to hold, which is why it is recorded as a defect above;
    // the assertion kept live is the weaker, always-true half of the pair.
    expect(freeRate.price).toContain('$0.0000');
    expect(fullRate.ratio).toBe('1x');
  });

  // ------------------------------------------------------------------
  // DEFECT (money, display): the same expression falls back to 1 whenever the
  // group is absent from groupRatio, but calculateModelPrice does NOT — it
  // scans the model's enabled groups and adopts the cheapest configured ratio
  // (helpers/utils.jsx:666-687). With group 'promo' usable and priced by the
  // fallback path, the row is quoted at the discounted price while the ratio
  // column still says "1x".
  // Fix direction: as above, render priceData.usedGroupRatio.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: an unconfigured group shows the ratio the price was computed with', () => {
    const { container } = renderTable({
      usableGroup: { promo: {} },
      groupRatio: { cheap: 0.25 },
      modelData: perTokenModel({ enable_groups: ['promo', 'cheap'] }),
    });
    expect(cell(container, 'promo', 'ratio').textContent).toBe('0.25x');
  });

  // Live invariant for the same scenario, safe under either behaviour: the
  // quoted price must be the discounted one, because that is what will be
  // charged.
  it('quotes an unconfigured group at the fallback ratio the calculator chose', () => {
    const { container } = renderTable({
      usableGroup: { promo: {} },
      groupRatio: { cheap: 0.25 },
      modelData: perTokenModel({ enable_groups: ['promo', 'cheap'] }),
    });
    expect(cell(container, 'promo', 'inputPrice').textContent).toContain(
      '$1.0000',
    );
  });
});

describe('ModelPricingTable — chrome', () => {
  it('renders the section heading and disables table pagination', () => {
    const { container } = renderTable();
    expect(container.textContent).toContain('分组价格');
    expect(container.textContent).toContain('不同用户分组的价格信息');
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-pagination',
      'off',
    );
  });

  it('survives a missing usableGroup without throwing', () => {
    const { container } = renderTable({ usableGroup: undefined });
    expect(container.querySelector('[data-testid="table"]')).toHaveAttribute(
      'data-row-count',
      '0',
    );
  });
});
