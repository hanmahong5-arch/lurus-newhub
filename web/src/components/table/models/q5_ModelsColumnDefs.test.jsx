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
 * getModelsColumns — the admin model catalogue.
 *
 * Two things on this table have consequences beyond cosmetics: the 定价 column,
 * which tells the operator whether a model is priced at all (an unpriced model
 * fails every call), and the operations column, which enables, disables and
 * deletes rows. Fixtures use id 7 and a three-row table so that a render which
 * quietly acts on "the first row" or on an index instead of an id cannot
 * produce the same calls as a correct one.
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

const semiModalConfirm = vi.hoisted(() => vi.fn());

vi.mock('@douyinfe/semi-ui', () => ({
  Button: ({ children, onClick, type, size }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': 'button',
        'data-type': type,
        'data-size': size,
        onClick,
      },
      children,
    ),
  Space: ({ children }) =>
    React.createElement('div', { 'data-testid': 'space' }, children),
  Tag: ({ children, color, size, shape, prefixIcon }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-size': size,
        'data-shape': shape,
      },
      prefixIcon,
      children,
    ),
  Typography: {
    Text: ({ children, copyable }) =>
      React.createElement(
        'span',
        {
          'data-testid': 'text',
          'data-copyable': copyable ? 'yes' : 'no',
        },
        children,
      ),
  },
  Modal: { confirm: semiModalConfirm },
  // content is surfaced: the tooltip is where "why is this model priced like
  // that" is explained, and dropping it would leave the branch untraceable.
  Tooltip: ({ content, children }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tooltip', 'data-content': String(content) },
      children,
    ),
}));

vi.mock('../../../helpers', () => ({
  timestamp2string: (ts) => `ts(${ts})`,
  getLobeHubIcon: (icon, size) =>
    React.createElement('i', {
      'data-testid': 'lobe-icon',
      'data-icon': String(icon),
      'data-size': String(size),
    }),
  stringToColor: (s) => `color-${s}`,
}));

vi.mock('../../common/ui/RenderUtils', () => ({
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

import { getModelsColumns } from './ModelsColumnDefs';

const t = (k) => k;

const vendorMap = {
  10: { name: 'OpenAI', icon: 'OpenAI' },
  11: { name: 'Anthropic' }, // no icon on purpose
};

const buildColumns = (over = {}) =>
  getModelsColumns({
    t,
    manageModel: vi.fn(),
    setEditingModel: vi.fn(),
    setShowEdit: vi.fn(),
    refresh: vi.fn(),
    vendorMap,
    pricingMap: {},
    ...over,
  });

const columnFor = (columns, dataIndex) =>
  columns.find((c) => c.dataIndex === dataIndex);

const renderCell = (column, record, index = 0) => {
  const view = render(
    <div data-testid='cell'>
      {column.render(record[column.dataIndex], record, index)}
    </div>,
  );
  return view.container.querySelector('[data-testid="cell"]');
};

const model = (over = {}) => ({
  id: 7,
  model_name: 'gpt-4o',
  status: 1,
  name_rule: 0,
  sync_official: 1,
  ...over,
});

beforeEach(() => {
  semiModalConfirm.mockReset();
});

describe('getModelsColumns — the column set', () => {
  it('emits every catalogue column, in order, with the actions pinned right', () => {
    const columns = buildColumns();
    expect(columns.map((c) => c.dataIndex)).toEqual([
      'icon',
      'model_name',
      'name_rule',
      'sync_official',
      'description',
      'vendor_id',
      'pricing',
      'tags',
      'endpoints',
      'bound_channels',
      'enable_groups',
      'quota_types',
      'created_time',
      'updated_time',
      'operate',
    ]);
    expect(columnFor(columns, 'operate').fixed).toBe('right');
  });
});

describe('icon and vendor columns', () => {
  it('prefers the model icon over the vendor icon', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'icon'),
      model({ icon: 'Claude', vendor_id: 10 }),
    );
    expect(cell.querySelector('[data-testid="lobe-icon"]')).toHaveAttribute(
      'data-icon',
      'Claude',
    );
  });

  it('falls back to the vendor icon when the model has none', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'icon'),
      model({ vendor_id: 10 }),
    );
    expect(cell.querySelector('[data-testid="lobe-icon"]')).toHaveAttribute(
      'data-icon',
      'OpenAI',
    );
  });

  it('renders a dash when neither the model nor its vendor has an icon', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'icon'),
      model({ vendor_id: 11 }),
    );
    expect(cell.textContent).toBe('-');
    expect(cell.querySelector('[data-testid="lobe-icon"]')).toBeNull();
  });

  it('names the vendor and gives it the Layers icon when it declares none', () => {
    const columns = buildColumns();
    const known = renderCell(
      columnFor(columns, 'vendor_id'),
      model({ vendor_id: 10 }),
    );
    expect(known.textContent).toBe('OpenAI');
    expect(known.querySelector('[data-testid="lobe-icon"]')).toHaveAttribute(
      'data-icon',
      'OpenAI',
    );

    const iconless = renderCell(
      columnFor(columns, 'vendor_id'),
      model({ vendor_id: 11 }),
    );
    expect(iconless.textContent).toBe('Anthropic');
    expect(iconless.querySelector('[data-testid="lobe-icon"]')).toHaveAttribute(
      'data-icon',
      'Layers',
    );
  });

  it('renders a dash for a vendor id that is not in the map', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'vendor_id'), model({ vendor_id: 999 }))
        .textContent,
    ).toBe('-');
    expect(
      renderCell(columnFor(columns, 'vendor_id'), model()).textContent,
    ).toBe('-');
  });

  it('offers the model name for copying', () => {
    const cell = renderCell(columnFor(buildColumns(), 'model_name'), model());
    expect(cell.textContent).toBe('gpt-4o');
    expect(cell.querySelector('[data-testid="text"]')).toHaveAttribute(
      'data-copyable',
      'yes',
    );
  });
});

describe('定价 column — is this model priced at all', () => {
  const pricingColumn = (pricingMap) =>
    columnFor(buildColumns({ pricingMap }), 'pricing');

  it('shows the explicitly configured ratio to three decimals', () => {
    const cell = renderCell(
      pricingColumn({ 'gpt-4o': { source: 'explicit', ratio: 2.5 } }),
      model(),
    );
    expect(cell.textContent).toContain('2.500');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'green',
    );
    expect(cell.querySelector('[data-testid="tooltip"]')).toHaveAttribute(
      'data-content',
      '显式配置倍率',
    );
  });

  // A configured ratio of 0 means "free", which is a decision someone made.
  // `ratio || '-'` would erase it and make a deliberately free model look
  // unconfigured.
  it('shows an explicit ratio of zero as 0.000, not as unpriced', () => {
    const cell = renderCell(
      pricingColumn({ 'gpt-4o': { source: 'explicit', ratio: 0 } }),
      model(),
    );
    expect(cell.textContent).toContain('0.000');
    expect(cell.textContent).not.toContain('未定价');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'green',
    );
  });

  it('marks a family-inferred ratio as approximate and names the family it came from', () => {
    const cell = renderCell(
      pricingColumn({
        'gpt-4o': {
          source: 'family_fallback',
          ratio: 1.25,
          family: 'gpt-4',
          markup: 1.1,
        },
      }),
      model(),
    );
    expect(cell.textContent).toContain('≈');
    expect(cell.textContent).toContain('1.250');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'blue',
    );
    expect(cell.querySelector('[data-testid="tooltip"]')).toHaveAttribute(
      'data-content',
      '自动定价: gpt-4 × 1.1',
    );
  });

  // The loudest case: an unpriced model rejects every request, so it must not
  // look like any of the priced states.
  it('warns in red that an unrecognised pricing source will fail calls', () => {
    const cell = renderCell(
      pricingColumn({ 'gpt-4o': { source: 'nothing_known' } }),
      model(),
    );
    expect(cell.textContent).toContain('未定价');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'red',
    );
    expect(cell.querySelector('[data-testid="tooltip"]')).toHaveAttribute(
      'data-content',
      '未配置倍率，调用将失败',
    );
  });

  it('shows a neutral placeholder for a model absent from the pricing map', () => {
    const cell = renderCell(pricingColumn({ other: {} }), model());
    expect(cell.textContent).toBe('-');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'grey',
    );
    expect(cell.querySelector('[data-testid="tooltip"]')).toBeNull();
  });

  it('does not throw when no pricing map was loaded yet', () => {
    const cell = renderCell(
      columnFor(buildColumns({ pricingMap: undefined }), 'pricing'),
      model(),
    );
    expect(cell.textContent).toBe('-');
  });

  // ------------------------------------------------------------------
  // DEFECT (money, silent): ModelsColumnDefs.jsx:193 and :203 render the ratio
  // with `info.ratio?.toFixed(3)`. When the pricing service reports a source
  // of 'explicit' but no ratio — a half-written pricing row, a rename that
  // orphaned the ratio — the optional chain yields undefined and the tag
  // renders EMPTY. The operator sees a green "priced" badge with no number in
  // it, i.e. the most alarming state in the system is displayed as the calmest.
  // Fix direction: fall through to the 未定价 branch when ratio is not a finite
  // number, so a missing ratio reads as missing.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: an explicit source with no ratio must not read as priced', () => {
    const cell = renderCell(
      pricingColumn({ 'gpt-4o': { source: 'explicit' } }),
      model(),
    );
    expect(cell.textContent.trim()).not.toBe('');
    expect(cell.querySelector('[data-testid="tag"]')).not.toHaveAttribute(
      'data-color',
      'green',
    );
  });

  // No live test pinning "the tag is empty" — that would go red on repair.
  // The live invariant is the one that must hold under either behaviour: a
  // ratio that IS present must appear in the cell, so the cell is never a
  // decoration detached from the number.
  it('always prints the ratio it was given when there is one', () => {
    const cell = renderCell(
      pricingColumn({ 'gpt-4o': { source: 'explicit', ratio: 3.14159 } }),
      model(),
    );
    expect(cell.textContent).toContain('3.142');
  });
});

describe('匹配类型 column', () => {
  it('labels an exact rule green and hangs no tooltip on it', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'name_rule'),
      model({ name_rule: 0, matched_models: ['a', 'b'] }),
    );
    expect(cell.textContent).toBe('精确');
    expect(cell.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'green',
    );
    expect(cell.querySelector('[data-testid="tooltip"]')).toBeNull();
  });

  it('reports how many models a fuzzy rule matched, and lists them on hover', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'name_rule'),
      model({
        name_rule: 1,
        matched_count: 3,
        matched_models: ['gpt-4o', 'gpt-4o-mini', 'gpt-4o-audio'],
      }),
    );
    expect(cell.textContent).toContain('前缀 3个模型');
    expect(cell.querySelector('[data-testid="tooltip"]')).toHaveAttribute(
      'data-content',
      'gpt-4o, gpt-4o-mini, gpt-4o-audio',
    );
  });

  it('gives each rule kind its own colour and label', () => {
    const columns = buildColumns();
    const seen = [1, 2, 3].map((rule) => {
      const cell = renderCell(
        columnFor(columns, 'name_rule'),
        model({ name_rule: rule }),
      );
      return [
        cell.textContent,
        cell.querySelector('[data-testid="tag"]').getAttribute('data-color'),
      ];
    });
    expect(seen).toEqual([
      ['前缀', 'blue'],
      ['包含', 'orange'],
      ['后缀', 'purple'],
    ]);
  });

  it('renders a dash for an unknown rule code', () => {
    expect(
      renderCell(
        columnFor(buildColumns(), 'name_rule'),
        model({ name_rule: 9 }),
      ).textContent,
    ).toBe('-');
  });

  // ------------------------------------------------------------------
  // DEFECT (falsy zero, operational): ModelsColumnDefs.jsx:292 gates the count
  // suffix on `record.matched_count` being truthy. A fuzzy rule that currently
  // matches NOTHING reports matched_count 0, which is falsy, so the badge falls
  // back to the bare "前缀" — exactly what a healthy rule looks like. The one
  // row an operator most needs to spot (a pattern that resolves to no models,
  // so the catalogue entry is dead) is the one rendered as normal.
  // Fix direction: test `typeof record.matched_count === 'number'`.
  // ------------------------------------------------------------------
  it.skip('CONTRACT: a fuzzy rule matching zero models must say so', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'name_rule'),
      model({ name_rule: 1, matched_count: 0, matched_models: [] }),
    );
    expect(cell.textContent).toContain('0个模型');
  });

  // Live counterpart, true either way: a rule that matches models must show
  // the number, so the suffix is not simply absent everywhere.
  it('shows the count whenever the rule matched at least one model', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'name_rule'),
      model({ name_rule: 2, matched_count: 1, matched_models: ['x'] }),
    );
    expect(cell.textContent).toContain('包含 1个模型');
  });
});

describe('tags, endpoints, groups, channels and billing columns', () => {
  it('splits tags on commas and drops the empty ones', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'tags'),
      model({ tags: 'vision,,tools,audio' }),
    );
    expect(cell.querySelector('[data-testid="limited-items"]')).toHaveAttribute(
      'data-count',
      '3',
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['vision', 'tools', 'audio']);
  });

  it('renders a dash for a model with no tags', () => {
    expect(
      renderCell(columnFor(buildColumns(), 'tags'), model({ tags: '' }))
        .textContent,
    ).toBe('-');
  });

  it('lists the keys of an endpoint map', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'endpoints'),
      model({
        endpoints: JSON.stringify({
          openai: { path: '/v1/chat/completions' },
          anthropic: { path: '/v1/messages' },
        }),
      }),
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['openai', 'anthropic']);
    expect(cell.querySelector('[data-testid="limited-items"]')).toHaveAttribute(
      'data-max-display',
      '3',
    );
  });

  it('still understands the legacy array form', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'endpoints'),
      model({ endpoints: '["/v1/chat/completions","/v1/embeddings"]' }),
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['/v1/chat/completions', '/v1/embeddings']);
  });

  it('renders a dash for an empty endpoint map or an empty legacy array', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'endpoints'), model({ endpoints: '{}' }))
        .textContent,
    ).toBe('-');
    expect(
      renderCell(columnFor(columns, 'endpoints'), model({ endpoints: '[]' }))
        .textContent,
    ).toBe('-');
  });

  // Malformed JSON must be shown verbatim rather than swallowed: the operator
  // needs to see what is actually stored so they can repair it.
  it('shows unparseable endpoint text verbatim instead of hiding it', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'endpoints'),
      model({ endpoints: '{not json' }),
    );
    expect(cell.textContent).toBe('{not json');
  });

  it('renders a dash when there are no endpoints at all', () => {
    expect(
      renderCell(columnFor(buildColumns(), 'endpoints'), model()).textContent,
    ).toBe('-');
  });

  it('lists enabled groups and dashes when there are none', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'enable_groups'),
      model({ enable_groups: ['default', 'vip'] }),
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['default', 'vip']);
    expect(
      renderCell(
        columnFor(columns, 'enable_groups'),
        model({ enable_groups: [] }),
      ).textContent,
    ).toBe('-');
  });

  it('names each bound channel with its type', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'bound_channels'),
      model({
        bound_channels: [
          { name: 'azure-eu', type: 3 },
          { name: 'openai-main', type: 1 },
        ],
      }),
    );
    expect(
      [...cell.querySelectorAll('[data-testid="tag"]')].map(
        (n) => n.textContent,
      ),
    ).toEqual(['azure-eu(3)', 'openai-main(1)']);
  });

  it('renders a dash when a model is bound to no channel', () => {
    expect(
      renderCell(columnFor(buildColumns(), 'bound_channels'), model())
        .textContent,
    ).toBe('-');
  });

  it('labels each billing type with its own colour and passes anything else through', () => {
    const cell = renderCell(
      columnFor(buildColumns(), 'quota_types'),
      model({ quota_types: [0, 1, 5] }),
    );
    const tags = [...cell.querySelectorAll('[data-testid="tag"]')];
    expect(tags.map((n) => n.textContent)).toEqual([
      '按量计费',
      '按次计费',
      '5',
    ]);
    expect(tags.map((n) => n.getAttribute('data-color'))).toEqual([
      'violet',
      'teal',
      'white',
    ]);
  });

  it('renders a dash for a model with no billing types', () => {
    const columns = buildColumns();
    expect(
      renderCell(columnFor(columns, 'quota_types'), model({ quota_types: [] }))
        .textContent,
    ).toBe('-');
    expect(
      renderCell(columnFor(columns, 'quota_types'), model()).textContent,
    ).toBe('-');
  });

  it('shows whether the model takes part in official sync, both ways', () => {
    const columns = buildColumns();
    const on = renderCell(
      columnFor(columns, 'sync_official'),
      model({ sync_official: 1 }),
    );
    expect(on.textContent).toBe('是');
    expect(on.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'green',
    );

    const off = renderCell(
      columnFor(columns, 'sync_official'),
      model({ sync_official: 0 }),
    );
    expect(off.textContent).toBe('否');
    expect(off.querySelector('[data-testid="tag"]')).toHaveAttribute(
      'data-color',
      'orange',
    );
  });

  it('clamps the description to 200px and dashes an empty one', () => {
    const columns = buildColumns();
    const cell = renderCell(
      columnFor(columns, 'description'),
      model({ description: 'fast and cheap' }),
    );
    expect(cell.querySelector('[data-testid="description"]')).toHaveAttribute(
      'data-max-width',
      '200',
    );
    expect(cell.textContent).toBe('fast and cheap');
    expect(
      renderCell(columnFor(columns, 'description'), model()).textContent,
    ).toBe('-');
  });

  it('formats both timestamps through the shared formatter', () => {
    const columns = buildColumns();
    expect(
      renderCell(
        columnFor(columns, 'created_time'),
        model({ created_time: 1710000000 }),
      ).textContent,
    ).toBe('ts(1710000000)');
    expect(
      renderCell(
        columnFor(columns, 'updated_time'),
        model({ updated_time: 1720000000 }),
      ).textContent,
    ).toBe('ts(1720000000)');
  });
});

describe('operations column', () => {
  const operate = (over = {}) => {
    const handlers = {
      manageModel: vi.fn().mockResolvedValue(undefined),
      setEditingModel: vi.fn(),
      setShowEdit: vi.fn(),
      refresh: vi.fn().mockResolvedValue(undefined),
      ...over,
    };
    return { handlers, column: columnFor(buildColumns(handlers), 'operate') };
  };

  const buttons = (cell) => [
    ...cell.querySelectorAll('[data-testid="button"]'),
  ];

  it('offers 禁用 for a live model and 启用 for a disabled one', () => {
    expect(
      buttons(renderCell(operate().column, model({ status: 1 })))[0]
        .textContent,
    ).toBe('禁用');
    expect(
      buttons(renderCell(operate().column, model({ status: 0 })))[0]
        .textContent,
    ).toBe('启用');
  });

  // The row's own id must travel with the action. Using id 7 on a row rendered
  // at index 0 means an implementation that passed the index would produce a
  // different call.
  it('disables the row it was rendered for, by id', () => {
    const { handlers, column } = operate();
    const record = model({ id: 7, status: 1 });
    fireEvent.click(buttons(renderCell(column, record, 0))[0]);
    expect(handlers.manageModel).toHaveBeenCalledWith(7, 'disable', record);
  });

  it('enables a disabled row by id', () => {
    const { handlers, column } = operate();
    const record = model({ id: 7, status: 0 });
    fireEvent.click(buttons(renderCell(column, record, 0))[0]);
    expect(handlers.manageModel).toHaveBeenCalledWith(7, 'enable', record);
  });

  it('opens the editor on the row that was clicked', () => {
    const { handlers, column } = operate();
    const record = model({ id: 7 });
    fireEvent.click(buttons(renderCell(column, record))[1]);
    expect(handlers.setEditingModel).toHaveBeenCalledWith(record);
    expect(handlers.setShowEdit).toHaveBeenCalledWith(true);
    expect(handlers.manageModel).not.toHaveBeenCalled();
  });

  // Deletion is irreversible, so the click alone must not delete: it must only
  // raise the confirmation. This is the negative half that matters.
  it('asks for confirmation instead of deleting on the first click', () => {
    const { handlers, column } = operate();
    fireEvent.click(buttons(renderCell(column, model()))[2]);
    expect(handlers.manageModel).not.toHaveBeenCalled();
    expect(semiModalConfirm).toHaveBeenCalledTimes(1);
    expect(semiModalConfirm.mock.calls[0][0].title).toBe(
      '确定是否要删除此模型？',
    );
    expect(semiModalConfirm.mock.calls[0][0].content).toBe('此修改将不可逆');
  });

  it('deletes the confirmed row and then refreshes the table', async () => {
    const { handlers, column } = operate();
    const record = model({ id: 7 });
    fireEvent.click(buttons(renderCell(column, record))[2]);
    await semiModalConfirm.mock.calls[0][0].onOk();
    // Awaiting onOk is not enough on its own — the handler fires an async IIFE.
    await Promise.resolve();
    await Promise.resolve();
    expect(handlers.manageModel).toHaveBeenCalledWith(7, 'delete', record);
    expect(handlers.refresh).toHaveBeenCalled();
  });
});
