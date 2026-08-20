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

// The expanded-row sub-table. It turns two loosely-typed channel fields
// (`models`, a comma string; `model_mapping`, a JSON string) into rows, and
// its checkboxes feed the page-level action rail — which can DELETE models.
// So the two things worth guarding are: the parse never throws on the
// malformed input the API can legitimately return, and the selection it
// reports back is scoped to this channel only.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

vi.mock('@douyinfe/semi-ui', () => {
  const Table = ({ columns, dataSource, rowSelection, pagination }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'table',
        'data-pagination': String(pagination),
        'data-selected': (rowSelection?.selectedRowKeys || []).join('|'),
      },
      (dataSource || []).map((record, rowIdx) =>
        React.createElement(
          'div',
          {
            key: record.key,
            'data-testid': 'model-row',
            'data-key': record.key,
          },
          columns.map((col, i) =>
            React.createElement(
              'span',
              { key: i, 'data-col': col.dataIndex },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
          // Stand-in for the row checkbox: selecting a row hands the whole
          // row object back, exactly as Semi's rowSelection.onChange does.
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `select-${record.modelName}`,
              onClick: () => rowSelection.onChange([record.key], [record]),
            },
            'select',
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `select-all-${rowIdx}`,
              onClick: () =>
                rowSelection.onChange(
                  dataSource.map((r) => r.key),
                  dataSource,
                ),
            },
            'select all',
          ),
        ),
      ),
    );
  return {
    Table,
    Tag: ({ children, color }) =>
      React.createElement(
        'span',
        { 'data-testid': 'tag', 'data-color': color },
        children,
      ),
    Empty: ({ description }) =>
      React.createElement('div', { 'data-testid': 'empty' }, description),
    Typography: {
      Text: ({ children, type }) =>
        React.createElement(
          'span',
          { 'data-testid': 'text', 'data-type': type },
          children,
        ),
    },
  };
});

import ChannelModelsSubTable from './ChannelModelsSubTable';

const t = (k) => k;

const renderSub = (channel, over = {}) => {
  const props = {
    channel,
    selectedModels: [],
    setChannelModelSelection: vi.fn(),
    t,
    ...over,
  };
  render(<ChannelModelsSubTable {...props} />);
  return props;
};

const rowNames = () =>
  screen.getAllByTestId('model-row').map((n) => n.dataset.key);

beforeEach(() => vi.clearAllMocks());

describe('model list parsing', () => {
  it('lists one row per model, keyed by channel id and model name', () => {
    renderSub({ id: 7, models: 'gpt-4o,gpt-4o-mini' });
    expect(rowNames()).toEqual(['7::gpt-4o', '7::gpt-4o-mini']);
    expect(screen.getAllByTestId('tag').map((n) => n.textContent)).toEqual([
      'gpt-4o',
      'gpt-4o-mini',
    ]);
  });

  it('trims whitespace and drops the empty segments a trailing comma leaves', () => {
    renderSub({ id: 7, models: ' gpt-4o , , claude-3 ,' });
    expect(rowNames()).toEqual(['7::gpt-4o', '7::claude-3']);
  });

  it('shows the empty state, not a blank table, for a channel with no models', () => {
    renderSub({ id: 7, models: '' });
    expect(screen.getByTestId('empty')).toHaveTextContent(
      '该渠道未配置任何模型',
    );
    expect(screen.queryAllByTestId('model-row')).toHaveLength(0);
  });

  it('shows the empty state when models is absent entirely', () => {
    renderSub({ id: 7 });
    expect(screen.getByTestId('empty')).toBeInTheDocument();
  });
});

describe('model redirect mapping', () => {
  it('shows the redirect target for a mapped model', () => {
    renderSub({
      id: 7,
      models: 'gpt-4o',
      model_mapping: JSON.stringify({ 'gpt-4o': 'gpt-4o-2024-11-20' }),
    });
    const row = screen.getByTestId('model-row');
    expect(row.textContent).toContain('gpt-4o-2024-11-20');
    expect(row.textContent).not.toContain('（无映射）');
  });

  it('says "no mapping" for an unmapped model — the negative half of the pair', () => {
    renderSub({
      id: 7,
      models: 'gpt-4o,claude-3',
      model_mapping: JSON.stringify({ 'gpt-4o': 'gpt-4o-2024-11-20' }),
    });
    const rows = screen.getAllByTestId('model-row');
    expect(rows[0].textContent).toContain('gpt-4o-2024-11-20');
    expect(rows[1].textContent).toContain('（无映射）');
  });

  it.each([
    ['malformed JSON', '{ not json'],
    ['a JSON array', '[]'],
    ['a JSON scalar', '"gpt-4o"'],
    ['null', 'null'],
    ['an empty string', ''],
  ])(
    'degrades to "no mapping" rather than throwing when model_mapping is %s',
    (_label, model_mapping) => {
      expect(() =>
        renderSub({ id: 7, models: 'gpt-4o', model_mapping }),
      ).not.toThrow();
      expect(screen.getByTestId('model-row').textContent).toContain(
        '（无映射）',
      );
    },
  );

  it('treats an empty-string mapping value as no mapping at all', () => {
    renderSub({
      id: 7,
      models: 'gpt-4o',
      model_mapping: JSON.stringify({ 'gpt-4o': '' }),
    });
    expect(screen.getByTestId('model-row').textContent).toContain('（无映射）');
  });
});

describe('selection is scoped to this channel', () => {
  it('reports the selection back as channel id plus bare model names', async () => {
    const user = userEvent.setup();
    const props = renderSub({ id: 7, models: 'gpt-4o,claude-3' });
    await user.click(screen.getByTestId('select-all-0'));
    expect(props.setChannelModelSelection).toHaveBeenCalledWith(7, [
      'gpt-4o',
      'claude-3',
    ]);
  });

  it('pre-checks only the models belonging to THIS channel', () => {
    renderSub(
      { id: 7, models: 'gpt-4o,claude-3' },
      {
        selectedModels: [
          { channelId: 7, modelName: 'gpt-4o' },
          // A model selected on a different channel's sub-table must not
          // light up a checkbox here.
          { channelId: 9, modelName: 'claude-3' },
        ],
      },
    );
    expect(screen.getByTestId('table').dataset.selected).toBe('7::gpt-4o');
  });

  it('pre-checks nothing when the selection belongs entirely to other channels', () => {
    renderSub(
      { id: 7, models: 'gpt-4o' },
      { selectedModels: [{ channelId: 9, modelName: 'gpt-4o' }] },
    );
    expect(screen.getByTestId('table').dataset.selected).toBe('');
  });
});
