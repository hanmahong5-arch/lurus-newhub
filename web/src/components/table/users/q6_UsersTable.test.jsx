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

// UsersTable is now a thin wrapper around CardTable: it builds the column set
// and forwards paging/compact-mode state. It used to also own four
// confirmation dialogs (promote / demote / enable-disable / delete), all of
// which posted to POST /api/user/manage — a handler that has not existed
// since the service was slimmed to a pure gateway, so every one of those
// controls 404'd. They were removed (see UsersColumnDefs and
// frontend_route_contract_test.go's knownUnrouted) rather than kept as
// always-failing buttons; v2 admin users is the working path now. What
// remains here is the grid plumbing those dialogs used to sit alongside.
//
// The column render functions themselves are covered in
// q6_UsersColumnDefs.test.jsx; getUsersColumns is stubbed with a known set.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

const H = vi.hoisted(() => ({
  getUsersColumns: vi.fn(),
  table: { current: null },
}));

vi.mock('./UsersColumnDefs', () => ({
  getUsersColumns: H.getUsersColumns,
}));

vi.mock('../../common/ui/CardTable', () => ({
  default: (props) => (
    (H.table.current = props),
    React.createElement('div', {
      'data-testid': 'card-table',
      'data-columns': props.columns.map((c) => c.dataIndex).join(','),
      'data-fixed': props.columns.map((c) => String(c.fixed)).join(','),
      'data-scroll': JSON.stringify(props.scroll ?? null),
      'data-loading': String(props.loading),
      'data-rows': (props.dataSource || []).map((r) => r.id).join(','),
      'data-page': String(props.pagination?.currentPage),
      'data-pagesize': String(props.pagination?.pageSize),
      'data-total': String(props.pagination?.total),
      'data-hasonrow': String(typeof props.onRow === 'function'),
      'data-hasselection': String(props.rowSelection != null),
    })
  ),
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Empty: ({ description, image, darkModeImage }) =>
    React.createElement(
      'div',
      { 'data-testid': 'empty' },
      React.createElement('span', { 'data-testid': 'empty-desc' }, description),
      image,
      darkModeImage,
    ),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () =>
    React.createElement('i', { 'data-testid': 'illu-light' }),
  IllustrationNoResultDark: () =>
    React.createElement('i', { 'data-testid': 'illu-dark' }),
}));

import UsersTable from './UsersTable';

const t = (k) => k;

const stubColumns = () => [
  { title: 'ID', dataIndex: 'id' },
  { title: 'user', dataIndex: 'username', fixed: 'left' },
  { title: 'op', dataIndex: 'operate', fixed: 'right', width: 300 },
];

const users = [
  { id: 42, username: 'alice', role: 1, status: 1 },
  { id: 77, username: 'bob', role: 1, status: 1 },
];

const makeProps = (over = {}) => ({
  users,
  loading: false,
  activePage: 1,
  pageSize: 20,
  userCount: 2,
  compactMode: false,
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  handleRow: vi.fn(),
  setEditingUser: vi.fn(),
  setShowEditUser: vi.fn(),
  t,
  ...over,
});

const renderTable = (over = {}) => {
  const props = makeProps(over);
  render(React.createElement(UsersTable, props));
  return { props, openers: H.getUsersColumns.mock.calls[0][0] };
};

beforeEach(() => {
  vi.clearAllMocks();
  H.table.current = null;
  H.getUsersColumns.mockImplementation(stubColumns);
});

describe('grid plumbing', () => {
  it('hands the column factory the editor openers and nothing else', () => {
    const { props, openers } = renderTable();
    expect(props.setEditingUser).toBe(openers.setEditingUser);
    expect(props.setShowEditUser).toBe(openers.setShowEditUser);
    // Locks the removal: no promote/demote/enable-disable/delete opener is
    // threaded through any more. Re-adding one here without a live route
    // behind it is exactly the regression this file guards against.
    expect(Object.keys(openers).sort()).toEqual([
      'setEditingUser',
      'setShowEditUser',
      't',
    ]);
  });

  it('renders the account rows it was given', () => {
    renderTable();
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-rows',
      '42,77',
    );
  });

  it('keeps the operations column pinned right outside compact mode', () => {
    renderTable({ compactMode: false });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-fixed',
      'undefined,left,right',
    );
  });

  it('unpins ONLY the operations column in compact mode', () => {
    renderTable({ compactMode: true });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-fixed',
      'undefined,left,undefined',
    );
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-columns',
      'id,username,operate',
    );
  });

  it('switches the horizontal scroll envelope with compact mode', () => {
    const { unmount } = render(
      React.createElement(UsersTable, makeProps({ compactMode: false })),
    );
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-scroll',
      JSON.stringify({ x: 'max-content' }),
    );
    unmount();
    render(React.createElement(UsersTable, makeProps({ compactMode: true })));
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-scroll',
      'null',
    );
  });

  it('forwards paging state and moves the pager through the callbacks', () => {
    const { props } = renderTable({
      activePage: 4,
      pageSize: 50,
      userCount: 913,
    });
    const table = screen.getByTestId('card-table');
    expect(table).toHaveAttribute('data-page', '4');
    expect(table).toHaveAttribute('data-pagesize', '50');
    expect(table).toHaveAttribute('data-total', '913');
    H.table.current.pagination.onPageChange(5);
    H.table.current.pagination.onPageSizeChange(100);
    expect(props.handlePageChange).toHaveBeenCalledWith(5);
    expect(props.handlePageSizeChange).toHaveBeenCalledWith(100);
  });

  it('renders a translated empty state', () => {
    renderTable({ users: [] });
    render(H.table.current.empty);
    expect(screen.getByTestId('empty-desc')).toHaveTextContent('搜索无结果');
  });

  it('offers no bulk row selection on the users screen', () => {
    renderTable();
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-hasselection',
      'false',
    );
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-hasonrow',
      'true',
    );
  });
});
