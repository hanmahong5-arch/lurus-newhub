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

const captured = vi.hoisted(() => ({ table: null, columnArgs: null }));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    Empty: ({ description }) => h('div', { role: 'status' }, description),
  };
});

vi.mock('@douyinfe/semi-illustrations', () => {
  const h = React.createElement;
  return {
    IllustrationNoResult: () => h('i', { 'data-testid': 'illus-no-result' }),
    IllustrationNoResultDark: () =>
      h('i', { 'data-testid': 'illus-no-result-dark' }),
  };
});

// CardTable belongs to another track; stub it to record what TokensTable
// hands down rather than re-testing the table itself.
vi.mock('../../common/ui/CardTable', () => {
  const h = React.createElement;
  return {
    default: (props) => {
      captured.table = props;
      return h(
        'div',
        { 'data-testid': 'card-table' },
        props.dataSource?.length
          ? `rows:${props.dataSource.length}`
          : props.empty,
      );
    },
  };
});

// Column building is covered exhaustively in tm_TokensColumnDefs.test.jsx;
// here we only care that TokensTable forwards the right dependencies and
// post-processes the result.
vi.mock('./TokensColumnDefs', () => ({
  getTokensColumns: (args) => {
    captured.columnArgs = args;
    return [
      { title: 'name', dataIndex: 'name' },
      { title: 'ops', dataIndex: 'operate', fixed: 'right' },
    ];
  },
}));

import TokensTable from './TokensTable';

const t = (k) => k;

const baseData = (over = {}) => ({
  tokens: [{ id: 1, name: 'a' }],
  loading: false,
  activePage: 2,
  pageSize: 20,
  tokenCount: 55,
  compactMode: false,
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  rowSelection: { selectedRowKeys: [1] },
  handleRow: vi.fn(),
  showKeys: { 1: true },
  setShowKeys: vi.fn(),
  copyText: vi.fn(),
  manageToken: vi.fn(),
  onOpenLink: vi.fn(),
  setEditingToken: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn(),
  t,
  ...over,
});

beforeEach(() => {
  captured.table = null;
  captured.columnArgs = null;
});

describe('TokensTable', () => {
  it('forwards every column dependency, including the reveal state', () => {
    const data = baseData();
    render(<TokensTable {...data} />);

    // showKeys/setShowKeys/copyText are what make the secret column work; a
    // dropped reference here would silently break reveal + copy.
    expect(captured.columnArgs.showKeys).toBe(data.showKeys);
    expect(captured.columnArgs.setShowKeys).toBe(data.setShowKeys);
    expect(captured.columnArgs.copyText).toBe(data.copyText);
    expect(captured.columnArgs.manageToken).toBe(data.manageToken);
    expect(captured.columnArgs.refresh).toBe(data.refresh);
    expect(captured.columnArgs.t).toBe(t);
  });

  it('passes the rows, selection and row handler straight through', () => {
    const data = baseData();
    render(<TokensTable {...data} />);

    expect(screen.getByTestId('card-table')).toHaveTextContent('rows:1');
    expect(captured.table.dataSource).toBe(data.tokens);
    expect(captured.table.rowSelection).toBe(data.rowSelection);
    expect(captured.table.onRow).toBe(data.handleRow);
    expect(captured.table.loading).toBe(false);
  });

  it('pins the operations column and scrolls horizontally in normal mode', () => {
    render(<TokensTable {...baseData({ compactMode: false })} />);

    expect(captured.table.scroll).toEqual({ x: 'max-content' });
    const ops = captured.table.columns.find((c) => c.dataIndex === 'operate');
    expect(ops.fixed).toBe('right');
  });

  it('unpins the operations column in compact mode so it does not overlap', () => {
    render(<TokensTable {...baseData({ compactMode: true })} />);

    expect(captured.table.scroll).toBeUndefined();
    const ops = captured.table.columns.find((c) => c.dataIndex === 'operate');
    expect(ops).toBeDefined();
    expect('fixed' in ops).toBe(false);
    // Non-operate columns must survive compact mode untouched.
    expect(captured.table.columns.find((c) => c.dataIndex === 'name')).toEqual({
      title: 'name',
      dataIndex: 'name',
    });
  });

  it('shows the search-specific empty state, not a generic one', () => {
    render(<TokensTable {...baseData({ tokens: [] })} />);
    expect(screen.getByRole('status')).toHaveTextContent('搜索无结果');
  });

  it('reflects the loading flag', () => {
    render(<TokensTable {...baseData({ loading: true })} />);
    expect(captured.table.loading).toBe(true);
  });

  // The pagination object is fully populated and then suppressed with
  // hidePagination — the page frame (CardPro) owns paging for this screen.
  // Both facts are asserted so a change to either is visible.
  it('builds pagination metadata but leaves rendering to the page frame', () => {
    render(<TokensTable {...baseData()} />);

    expect(captured.table.hidePagination).toBe(true);
    expect(captured.table.pagination).toMatchObject({
      currentPage: 2,
      pageSize: 20,
      total: 55,
    });
  });
});
