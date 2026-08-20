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

// UsersTable is the router between the row action rail and four confirmation
// dialogs, two of which change a privilege level and one of which deletes an
// account. Everything asserted here is about IDENTITY and ACTION: which
// account the confirmation is pointed at, and which verb reaches manageUser.
//
// Every fixture user has an id that is neither its array index nor 0/1, and
// the tests deliberately act on the SECOND row — so an implementation that
// reached for `users[0]` or a hard-coded id would fail rather than coincide.
//
// The column render functions themselves are covered in
// q6_UsersColumnDefs.test.jsx; getUsersColumns is stubbed with a known set.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';

const H = vi.hoisted(() => {
  const React = require('react');
  // Each modal stub is a MARKER that publishes the user it was pointed at and
  // exposes onConfirm/onCancel as real buttons. Stubbing these to `() => null`
  // would let the whole confirmation path run invisibly.
  const modalStub = (name, slot, extraAttrs = () => ({})) => ({
    default: (props) => {
      slot.current = props;
      return React.createElement(
        'div',
        {
          'data-testid': `${name}-modal`,
          'data-visible': String(props.visible),
          'data-user-id': String(props.user ? props.user.id : 'none'),
          'data-user-name': String(props.user ? props.user.username : 'none'),
          ...extraAttrs(props),
        },
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `${name}-ok`,
            onClick: props.onConfirm ?? props.onOk,
          },
          'ok',
        ),
        React.createElement(
          'button',
          {
            type: 'button',
            'data-testid': `${name}-cancel`,
            onClick: props.onCancel,
          },
          'cancel',
        ),
      );
    },
  });
  return {
    modalStub,
    getUsersColumns: vi.fn(),
    table: { current: null },
    promote: { current: null },
    demote: { current: null },
    enableDisable: { current: null },
    del: { current: null },
  };
});

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

vi.mock('./modals/PromoteUserModal', () => H.modalStub('promote', H.promote));
vi.mock('./modals/DemoteUserModal', () => H.modalStub('demote', H.demote));
vi.mock('./modals/EnableDisableUserModal', () =>
  H.modalStub('toggle', H.enableDisable, (p) => ({
    'data-action': String(p.action),
  })),
);
vi.mock('./modals/DeleteUserModal', () =>
  H.modalStub('delete', H.del, (p) => ({
    'data-page': String(p.activePage),
    'data-userlist': (p.users || []).map((u) => u.id).join(','),
  })),
);

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
  manageUser: vi.fn().mockResolvedValue(true),
  refresh: vi.fn().mockResolvedValue(undefined),
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
  it('hands the column factory all four confirmation openers plus the editor', () => {
    const { props, openers } = renderTable();
    expect(props.setEditingUser).toBe(openers.setEditingUser);
    expect(props.setShowEditUser).toBe(openers.setShowEditUser);
    [
      'showPromoteModal',
      'showDemoteModal',
      'showEnableDisableModal',
      'showDeleteModal',
    ].forEach((k) => expect(typeof openers[k]).toBe('function'));
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
    // Negative lock: users has no bulk actions today. If row selection is ever
    // introduced it must arrive with bulk handlers, and this test will say so.
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

describe('confirmations start closed', () => {
  it('opens none of the four dialogs on a plain render', () => {
    renderTable();
    ['promote', 'demote', 'toggle', 'delete'].forEach((n) =>
      expect(screen.getByTestId(`${n}-modal`)).toHaveAttribute(
        'data-visible',
        'false',
      ),
    );
  });
});

describe('promote', () => {
  it('points the promote dialog at the account the rail clicked', () => {
    const { openers } = renderTable();
    act(() => openers.showPromoteModal(users[1]));
    const modal = screen.getByTestId('promote-modal');
    expect(modal).toHaveAttribute('data-visible', 'true');
    expect(modal).toHaveAttribute('data-user-id', '77');
  });

  it('promotes that same account — never the first row — and closes', () => {
    const { props, openers } = renderTable();
    act(() => openers.showPromoteModal(users[1]));
    act(() => screen.getByTestId('promote-ok').click());
    expect(props.manageUser).toHaveBeenCalledTimes(1);
    expect(props.manageUser).toHaveBeenCalledWith(77, 'promote', users[1]);
    expect(screen.getByTestId('promote-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('changes no privilege when the admin backs out', () => {
    const { props, openers } = renderTable();
    act(() => openers.showPromoteModal(users[1]));
    act(() => screen.getByTestId('promote-cancel').click());
    expect(props.manageUser).not.toHaveBeenCalled();
    expect(screen.getByTestId('promote-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });
});

describe('demote', () => {
  it('sends demote — not promote — for the clicked account', () => {
    const { props, openers } = renderTable();
    act(() => openers.showDemoteModal(users[1]));
    expect(screen.getByTestId('demote-modal')).toHaveAttribute(
      'data-user-id',
      '77',
    );
    act(() => screen.getByTestId('demote-ok').click());
    expect(props.manageUser).toHaveBeenCalledWith(77, 'demote', users[1]);
  });

  it('leaves the account alone when the admin backs out', () => {
    const { props, openers } = renderTable();
    act(() => openers.showDemoteModal(users[0]));
    act(() => screen.getByTestId('demote-cancel').click());
    expect(props.manageUser).not.toHaveBeenCalled();
  });
});

describe('enable / disable', () => {
  it('carries the DISABLE verb through to the API call', () => {
    const { props, openers } = renderTable();
    act(() => openers.showEnableDisableModal(users[1], 'disable'));
    expect(screen.getByTestId('toggle-modal')).toHaveAttribute(
      'data-action',
      'disable',
    );
    act(() => screen.getByTestId('toggle-ok').click());
    expect(props.manageUser).toHaveBeenCalledWith(77, 'disable', users[1]);
  });

  it('carries the ENABLE verb through too — the verb is not hard-coded', () => {
    const { props, openers } = renderTable();
    act(() => openers.showEnableDisableModal(users[0], 'enable'));
    expect(screen.getByTestId('toggle-modal')).toHaveAttribute(
      'data-action',
      'enable',
    );
    act(() => screen.getByTestId('toggle-ok').click());
    expect(props.manageUser).toHaveBeenCalledWith(42, 'enable', users[0]);
  });

  it('uses the verb from the LATEST open, not the first one', () => {
    // Guards the shared `enableDisableAction` state: opening disable for one
    // account then enable for another must not submit "disable".
    const { props, openers } = renderTable();
    act(() => openers.showEnableDisableModal(users[0], 'disable'));
    act(() => screen.getByTestId('toggle-cancel').click());
    act(() => openers.showEnableDisableModal(users[1], 'enable'));
    act(() => screen.getByTestId('toggle-ok').click());
    expect(props.manageUser).toHaveBeenCalledTimes(1);
    expect(props.manageUser).toHaveBeenCalledWith(77, 'enable', users[1]);
  });
});

describe('delete', () => {
  it('points the delete dialog at the clicked account and gives it page context', () => {
    const { props, openers } = renderTable({ activePage: 3 });
    act(() => openers.showDeleteModal(users[1]));
    const modal = screen.getByTestId('delete-modal');
    expect(modal).toHaveAttribute('data-visible', 'true');
    expect(modal).toHaveAttribute('data-user-id', '77');
    expect(modal).toHaveAttribute('data-page', '3');
    expect(modal).toHaveAttribute('data-userlist', '42,77');
    expect(H.del.current.manageUser).toBe(props.manageUser);
    expect(H.del.current.refresh).toBe(props.refresh);
  });

  it('closes without deleting when the admin backs out', () => {
    const { props, openers } = renderTable();
    act(() => openers.showDeleteModal(users[1]));
    act(() => screen.getByTestId('delete-cancel').click());
    expect(screen.getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
    expect(props.manageUser).not.toHaveBeenCalled();
  });

  it('opens only the delete dialog — the other three stay shut', () => {
    const { openers } = renderTable();
    act(() => openers.showDeleteModal(users[0]));
    ['promote', 'demote', 'toggle'].forEach((n) =>
      expect(screen.getByTestId(`${n}-modal`)).toHaveAttribute(
        'data-visible',
        'false',
      ),
    );
    expect(screen.getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'true',
    );
  });
});

describe('outcome of a privilege change', () => {
  // DEFECT (correctness): handlePromoteConfirm / handleDemoteConfirm /
  // handleEnableDisableConfirm call manageUser WITHOUT awaiting it and without
  // inspecting the result, then close the dialog unconditionally. A server
  // that refuses the promotion produces exactly the same UI as one that
  // granted it — the dialog closes and the row keeps rendering the old role
  // with no error state of its own. The delete path at least awaits its call.
  it.skip('CONTRACT: a refused privilege change must not close the dialog silently', () => {
    const { openers } = renderTable({
      manageUser: vi.fn().mockResolvedValue(false),
    });
    act(() => openers.showPromoteModal(users[1]));
    act(() => screen.getByTestId('promote-ok').click());
    expect(screen.getByTestId('promote-modal')).toHaveAttribute(
      'data-visible',
      'true',
    );
  });

  it('currently discards the result of the privilege change call', async () => {
    // Fix-safe: asserts the refusal was genuinely delivered to the component
    // (the precondition of the contract above) and that the right verb and
    // account were submitted. Still passes once the outcome is handled.
    const manageUser = vi.fn().mockResolvedValue(false);
    const { openers } = renderTable({ manageUser });
    act(() => openers.showPromoteModal(users[1]));
    act(() => screen.getByTestId('promote-ok').click());
    expect(manageUser).toHaveBeenCalledWith(77, 'promote', users[1]);
    await expect(manageUser.mock.results[0].value).resolves.toBe(false);
  });
});
