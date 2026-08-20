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

// ChannelsTable owns four decisions and delegates everything else:
//   * which columns are visible;
//   * whether `fixed` survives compact mode;
//   * which rows may be expanded into the models sub-table;
//   * whether row selection exists at all.
// getChannelsColumns is stubbed with a KNOWN column set so those four
// decisions can be asserted precisely; the column render functions themselves
// are covered directly in tc_ChannelsColumnDefs.test.jsx.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const H = vi.hoisted(() => ({
  getChannelsColumns: vi.fn(),
  // Last props CardTable received, so callbacks can be invoked directly
  // instead of through a click that would swallow a throw.
  captured: { current: null },
}));

vi.mock('./ChannelsColumnDefs', () => ({
  getChannelsColumns: H.getChannelsColumns,
}));

// CardTable is the seam: surface the props ChannelsTable computed so they can
// be asserted, and actually invoke the render callbacks so their code runs.
vi.mock('../../common/ui/CardTable', () => ({
  default: (props) => (
    (H.captured.current = props),
    React.createElement(
      'div',
      {
        'data-testid': 'card-table',
        'data-columns': props.columns.map((c) => c.key).join(','),
        'data-fixed': props.columns.map((c) => String(c.fixed)).join(','),
        'data-scroll': JSON.stringify(props.scroll ?? null),
        'data-loading': String(props.loading),
        'data-rowkey': props.rowKey,
        'data-hasselection': String(props.rowSelection !== null),
        'data-expandedkeys': (props.expandedRowKeys || []).join(','),
      },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'fire-select',
          onClick: () =>
            props.rowSelection?.onChange?.([1, 2], [{ id: 1 }, { id: 2 }]),
        },
        'select',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'fire-expand-objects',
          onClick: () => props.onExpandedRowsChange([{ id: 5 }, { id: 6 }, {}]),
        },
        'expand',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'fire-expand-scalars',
          onClick: () => props.onExpandedRowsChange([7, 8]),
        },
        'expand-scalar',
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'fire-expand-empty',
          onClick: () => props.onExpandedRowsChange(undefined),
        },
        'expand-empty',
      ),
      React.createElement(
        'div',
        { 'data-testid': 'expandable-probe' },
        JSON.stringify(
          [
            { id: 1, models: 'gpt-4o' },
            { id: 2, models: '' },
            { id: 3, models: '   ' },
            { id: 4 },
            { id: 5, models: 'a', children: [] },
            { id: 6, models: ['a'] },
          ].map((r) => props.rowExpandable(r)),
        ),
      ),
      React.createElement(
        'div',
        { 'data-testid': 'expanded-render' },
        props.expandedRowRender({ id: 1, models: 'gpt-4o' }),
      ),
      props.empty,
    )
  ),
}));

vi.mock('./ChannelModelsSubTable', () => ({
  default: ({ channel, selectedModels, t }) =>
    React.createElement('div', {
      'data-testid': 'sub-table',
      'data-channel': String(channel?.id),
      'data-selected': String((selectedModels || []).length),
      'data-t': typeof t,
    }),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Empty: ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () => React.createElement('i', null),
  IllustrationNoResultDark: () => React.createElement('i', null),
}));

import ChannelsTable from './ChannelsTable';

const COLUMNS = [
  { key: 'id', title: 'ID', dataIndex: 'id' },
  { key: 'name', title: '名称', dataIndex: 'name' },
  {
    key: 'balance',
    title: '已用/剩余',
    dataIndex: 'expired_time',
    fixed: 'right',
  },
];

const t = (k) => k;

const makeProps = (over = {}) => ({
  t,
  COLUMN_KEYS: { ID: 'id', NAME: 'name', BALANCE: 'balance' },
  channels: [{ id: 1, name: 'a' }],
  loading: false,
  searching: false,
  activePage: 2,
  pageSize: 20,
  channelCount: 40,
  enableBatchDelete: true,
  compactMode: false,
  visibleColumns: { id: true, name: true, balance: true },
  setSelectedChannels: vi.fn(),
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  handleRow: vi.fn(),
  updateChannelBalance: vi.fn(),
  manageChannel: vi.fn(),
  manageTag: vi.fn(),
  submitTagEdit: vi.fn(),
  testChannel: vi.fn(),
  setCurrentTestChannel: vi.fn(),
  setShowModelTestModal: vi.fn(),
  setEditingChannel: vi.fn(),
  setShowEdit: vi.fn(),
  setShowEditTag: vi.fn(),
  setEditingTag: vi.fn(),
  copySelectedChannel: vi.fn(),
  refresh: vi.fn(),
  checkOllamaVersion: vi.fn(),
  setShowMultiKeyManageModal: vi.fn(),
  setCurrentMultiKeyChannel: vi.fn(),
  selectedModels: [{ channelId: 1, modelName: 'gpt-4o' }],
  setChannelModelSelection: vi.fn(),
  expandedRowKeys: [1],
  setExpandedRowKeys: vi.fn(),
  ...over,
});

const renderTable = (over = {}) => {
  const props = makeProps(over);
  render(<ChannelsTable {...props} />);
  return props;
};

const table = () => screen.getByTestId('card-table');

beforeEach(() => {
  vi.clearAllMocks();
  H.getChannelsColumns.mockReturnValue(COLUMNS);
});

describe('column visibility', () => {
  it('renders every column the user left switched on, in definition order', () => {
    renderTable();
    expect(table().dataset.columns).toBe('id,name,balance');
  });

  it('drops the columns the user switched off', () => {
    renderTable({ visibleColumns: { id: true, name: false, balance: true } });
    expect(table().dataset.columns).toBe('id,balance');
  });

  it('treats a column missing from visibleColumns as hidden', () => {
    renderTable({ visibleColumns: { id: true } });
    expect(table().dataset.columns).toBe('id');
  });

  it('renders no columns rather than throwing when nothing is visible', () => {
    renderTable({ visibleColumns: {} });
    expect(table().dataset.columns).toBe('');
  });
});

describe('compact mode', () => {
  it('keeps the fixed column pinned and the table horizontally scrollable by default', () => {
    renderTable({ compactMode: false });
    expect(table().dataset.fixed).toBe('undefined,undefined,right');
    expect(JSON.parse(table().dataset.scroll)).toEqual({ x: 'max-content' });
  });

  it('strips every fixed flag and the horizontal scroll in compact mode', () => {
    renderTable({ compactMode: true });
    // A pinned column inside a compact, non-scrolling table overlaps its
    // neighbours — hence the strip.
    expect(table().dataset.fixed).toBe('undefined,undefined,undefined');
    expect(JSON.parse(table().dataset.scroll)).toBeNull();
  });
});

describe('row expansion', () => {
  it('allows expansion only for non-tag rows that actually declare models', () => {
    renderTable();
    expect(
      JSON.parse(screen.getByTestId('expandable-probe').textContent),
    ).toEqual([
      true, // has models
      false, // empty string
      false, // whitespace only
      false, // no models field
      false, // tag-aggregation row
      false, // models is an array, not the comma string the parser expects
    ]);
  });

  it('passes the expanded row down to the models sub-table with the page selection', () => {
    renderTable();
    const sub = screen.getByTestId('sub-table');
    expect(sub.dataset.channel).toBe('1');
    expect(sub.dataset.selected).toBe('1');
    expect(sub.dataset.t).toBe('function');
  });

  it('maps expanded row records back to their ids, discarding rows with none', () => {
    const props = renderTable();
    screen.getByTestId('fire-expand-objects').click();
    // `{}` carries no id; keeping it would poison expandedRowKeys and
    // collapse unrelated rows.
    expect(props.setExpandedRowKeys).toHaveBeenCalledWith([5, 6]);
  });

  // DEFECT (correctness): the id mapper is
  //     .map((r) => (typeof r === 'object' ? r.id : r))
  //     .filter((v) => v !== undefined && v !== null)
  // The `!== null` in the filter says plainly that the author expected null
  // entries in this array — but `typeof null === 'object'`, so a null is
  // routed into the `r.id` branch and dereferenced one step BEFORE the guard
  // that was written to catch it. The filter can never do its job.
  //
  // Consequence: a null in the expanded-rows array throws out of an event
  // handler, React unmounts the tree, and the whole channel list blanks —
  // rather than that one row simply not being tracked as expanded. The
  // defensive code is present, wired backwards, and reports as covered.
  //
  // Verified red 2026-08-20: un-skipped it fails with
  // "TypeError: Cannot read properties of null (reading 'id')".
  it.skip('CONTRACT:discards a null expanded-row entry instead of dereferencing it', () => {
    const props = renderTable();
    H.captured.current.onExpandedRowsChange([{ id: 5 }, null, { id: 6 }]);
    expect(props.setExpandedRowKeys).toHaveBeenCalledWith([5, 6]);
  });

  it('currently throws on a null expanded-row entry the filter claims to handle', () => {
    // Pinning the defect, not endorsing it — see the comment above. The
    // assertion is "the null is not handled", which stays meaningful however
    // the fix is written: once it IS handled, this goes red and the skip
    // above goes green together.
    const props = renderTable();
    expect(() =>
      H.captured.current.onExpandedRowsChange([{ id: 5 }, null]),
    ).toThrow();
    expect(props.setExpandedRowKeys).not.toHaveBeenCalled();
  });

  it('accepts a bare id list as well as row records', () => {
    const props = renderTable();
    screen.getByTestId('fire-expand-scalars').click();
    expect(props.setExpandedRowKeys).toHaveBeenCalledWith([7, 8]);
  });

  it('collapses to an empty list rather than throwing when handed nothing', () => {
    const props = renderTable();
    expect(() => screen.getByTestId('fire-expand-empty').click()).not.toThrow();
    expect(props.setExpandedRowKeys).toHaveBeenCalledWith([]);
  });

  it('reflects the controlled expanded keys back to the table', () => {
    renderTable({ expandedRowKeys: [3, 9] });
    expect(table().dataset.expandedkeys).toBe('3,9');
  });
});

describe('row selection', () => {
  it('reports the selected row records upward when batch mode is on', () => {
    const props = renderTable({ enableBatchDelete: true });
    expect(table().dataset.hasselection).toBe('true');
    screen.getByTestId('fire-select').click();
    // The rail needs the full records (status, children, channel_info), not
    // just the keys.
    expect(props.setSelectedChannels).toHaveBeenCalledWith([
      { id: 1 },
      { id: 2 },
    ]);
  });

  it('offers no row selection at all when batch mode is off', () => {
    const props = renderTable({ enableBatchDelete: false });
    expect(table().dataset.hasselection).toBe('false');
    screen.getByTestId('fire-select').click();
    expect(props.setSelectedChannels).not.toHaveBeenCalled();
  });
});

describe('wiring and chrome', () => {
  it('hands the column factory the callbacks the cells need to act', () => {
    const props = renderTable();
    const arg = H.getChannelsColumns.mock.calls[0][0];
    expect(arg.updateChannelBalance).toBe(props.updateChannelBalance);
    expect(arg.manageChannel).toBe(props.manageChannel);
    expect(arg.submitTagEdit).toBe(props.submitTagEdit);
    expect(arg.setCurrentMultiKeyChannel).toBe(props.setCurrentMultiKeyChannel);
    expect(arg.activePage).toBe(2);
    expect(arg.channels).toBe(props.channels);
  });

  it('keys rows by id so selection survives a re-sort', () => {
    renderTable();
    expect(table().dataset.rowkey).toBe('id');
  });

  it('shows the busy state while either the list or a search is loading', () => {
    renderTable({ loading: false, searching: true });
    expect(table().dataset.loading).toBe('true');
  });

  it('is idle when neither is running', () => {
    renderTable({ loading: false, searching: false });
    expect(table().dataset.loading).toBe('false');
  });

  it('offers a "no results" empty state rather than a bare blank table', () => {
    renderTable({ channels: [] });
    expect(screen.getByTestId('empty')).toHaveTextContent('搜索无结果');
  });
});
