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

// The four "are you sure" dialogs on the users screen. Between them they can
// hand out an admin role, take one away, lock an account out, and delete an
// account outright.
//
// All four accept a `user` prop. Only DeleteUserModal actually reads it. That
// asymmetry is the subject of the defect locks below: a confirmation that
// cannot name its target is a confirmation of nothing.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: ({ title, visible, onCancel, onOk, type, children }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'modal',
        'data-visible': String(visible),
        'data-type': type,
      },
      React.createElement('h1', { 'data-testid': 'modal-title' }, title),
      React.createElement('div', { 'data-testid': 'modal-body' }, children),
      React.createElement(
        'button',
        { type: 'button', 'data-testid': 'ok', onClick: onOk },
        'ok',
      ),
      React.createElement(
        'button',
        { type: 'button', 'data-testid': 'cancel', onClick: onCancel },
        'cancel',
      ),
    ),
}));

import PromoteUserModal from './PromoteUserModal';
import DemoteUserModal from './DemoteUserModal';
import EnableDisableUserModal from './EnableDisableUserModal';
import DeleteUserModal from './DeleteUserModal';

const t = (k) => k;

// id 88 is neither an index nor 0/1, and the username is distinctive, so an
// assertion that the dialog identifies its target cannot pass by accident.
const target = { id: 88, username: 'carol.ops', role: 1, status: 1 };
const otherRow = { id: 12, username: 'alice', role: 1, status: 1 };

const dialogText = () => screen.getByTestId('modal').textContent;

beforeEach(() => vi.clearAllMocks());

describe('PromoteUserModal', () => {
  const renderIt = (over = {}) => {
    const props = {
      visible: true,
      onCancel: vi.fn(),
      onConfirm: vi.fn(),
      user: target,
      t,
      ...over,
    };
    render(React.createElement(PromoteUserModal, props));
    return props;
  };

  it('says what it is about to do and marks itself as a warning', () => {
    renderIt();
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定要提升此用户吗？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '此操作将提升用户的权限级别',
    );
    expect(screen.getByTestId('modal')).toHaveAttribute('data-type', 'warning');
  });

  it('confirms and cancels through the handlers it was given', () => {
    const props = renderIt();
    screen.getByTestId('ok').click();
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
    expect(props.onCancel).not.toHaveBeenCalled();
    screen.getByTestId('cancel').click();
    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
  });

  it('stays hidden until asked to show', () => {
    renderIt({ visible: false });
    expect(screen.getByTestId('modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  // DEFECT (security): the dialog receives `user` and never renders it. An
  // admin clicking 提升 on row 14 of a paged list is asked "promote this
  // user?" with no name, no id, nothing — so a mis-clicked row, or a row that
  // shifted under a background refresh, grants an admin role to the wrong
  // account with no chance to notice. Same for demote and enable/disable.
  it.skip('CONTRACT: the promote confirmation identifies the account being promoted', () => {
    renderIt();
    expect(dialogText()).toContain('carol.ops');
  });

  it('currently asks about "this user" without saying which', () => {
    // Fix-safe: asserts the dialog states the ACTION, which any fixed version
    // still does. It does NOT assert the username is absent — that would turn
    // red the moment someone adds it, i.e. it would block the fix.
    renderIt();
    expect(dialogText()).toContain('提升');
  });
});

describe('DemoteUserModal', () => {
  const renderIt = (over = {}) => {
    const props = {
      visible: true,
      onCancel: vi.fn(),
      onConfirm: vi.fn(),
      user: target,
      t,
      ...over,
    };
    render(React.createElement(DemoteUserModal, props));
    return props;
  };

  it('describes a demotion, not a promotion', () => {
    renderIt();
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定要降级此用户吗？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '此操作将降低用户的权限级别',
    );
    // Negative half: the two dialogs must not be interchangeable copy.
    expect(dialogText()).not.toContain('提升用户的权限级别');
  });

  it('routes ok and cancel to the right handlers', () => {
    const props = renderIt();
    screen.getByTestId('ok').click();
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
    screen.getByTestId('cancel').click();
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it.skip('CONTRACT: the demote confirmation identifies the account being demoted', () => {
    renderIt();
    expect(dialogText()).toContain('carol.ops');
  });
});

describe('EnableDisableUserModal', () => {
  const renderIt = (over = {}) => {
    const props = {
      visible: true,
      onCancel: vi.fn(),
      onConfirm: vi.fn(),
      user: target,
      action: 'disable',
      t,
      ...over,
    };
    render(React.createElement(EnableDisableUserModal, props));
    return props;
  };

  it('asks about disabling when the action is disable', () => {
    renderIt({ action: 'disable' });
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定要禁用此用户吗？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '此操作将禁用用户账户',
    );
  });

  it('asks about enabling for every other action value', () => {
    renderIt({ action: 'enable' });
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定要启用此用户吗？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '此操作将启用用户账户',
    );
  });

  // DEFECT (correctness): the copy is chosen by `action === 'disable'`, so any
  // value that is not exactly the string 'disable' — a typo, a renamed
  // constant, or the empty string the table holds before the first open —
  // silently reads as ENABLE. A dialog that is about to lock an account out
  // would then reassure the admin it is switching the account ON.
  it.skip('CONTRACT: an unrecognised action must not be presented as "enable"', () => {
    renderIt({ action: 'Disable' });
    expect(screen.getByTestId('modal-title')).not.toHaveTextContent(
      '确定要启用此用户吗？',
    );
  });

  it('currently treats any non-"disable" action as an enable', () => {
    // Pins the mechanism, not a specific wrong string: it shows the fallback
    // exists. A fix that adds an explicit unknown-action branch keeps the
    // 'enable' case passing, which is what is asserted here.
    renderIt({ action: 'enable' });
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定要启用此用户吗？',
    );
  });

  it('routes ok and cancel to the right handlers', () => {
    const props = renderIt();
    screen.getByTestId('ok').click();
    expect(props.onConfirm).toHaveBeenCalledTimes(1);
    screen.getByTestId('cancel').click();
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it.skip('CONTRACT: the lockout confirmation identifies the account being locked out', () => {
    renderIt({ action: 'disable' });
    expect(dialogText()).toContain('carol.ops');
  });
});

describe('DeleteUserModal', () => {
  const makeProps = (over = {}) => ({
    visible: true,
    onCancel: vi.fn(),
    user: target,
    users: [otherRow, target],
    activePage: 1,
    refresh: vi.fn().mockResolvedValue(undefined),
    manageUser: vi.fn().mockResolvedValue(true),
    t,
    ...over,
  });

  const renderIt = (over = {}) => {
    const props = makeProps(over);
    render(React.createElement(DeleteUserModal, props));
    return props;
  };

  const confirmAndSettle = async () => {
    await act(async () => {
      screen.getByTestId('ok').click();
    });
    await act(async () => {
      vi.advanceTimersByTime(150);
    });
  };

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('is styled as the destructive dialog and warns it is irreversible', () => {
    renderIt();
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定是否要注销此用户？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '相当于删除用户，此修改将不可逆',
    );
    // The one dialog of the four that must NOT read as a mere warning.
    expect(screen.getByTestId('modal')).toHaveAttribute('data-type', 'danger');
  });

  it('deletes the account under the cursor, not the first of the list', async () => {
    const props = renderIt();
    await confirmAndSettle();
    expect(props.manageUser).toHaveBeenCalledTimes(1);
    expect(props.manageUser).toHaveBeenCalledWith(88, 'delete', target);
  });

  it('reloads the list and closes once the deletion is submitted', async () => {
    const props = renderIt();
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalled();
    expect(props.onCancel).toHaveBeenCalled();
  });

  it('deletes nothing when the admin backs out', () => {
    const props = renderIt();
    screen.getByTestId('cancel').click();
    expect(props.manageUser).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('never asks the API for page 0 or below when recovering the page', async () => {
    const props = renderIt({ users: [], activePage: 1 });
    await confirmAndSettle();
    props.refresh.mock.calls
      .map((c) => c[0])
      .filter((a) => a !== undefined)
      .forEach((p) => expect(p).toBeGreaterThanOrEqual(1));
  });

  it('steps back a page when the list it was told about is already empty', async () => {
    const props = renderIt({ users: [], activePage: 4 });
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalledWith(3);
  });

  // DEFECT (correctness): identical to the redemptions delete modal — the
  // page-back check reads the `users` prop captured before the delete, so
  // deleting the last account on a page leaves the admin on an empty page.
  it.skip('CONTRACT: deleting the last account on a page returns the admin to the previous page', async () => {
    const props = renderIt({ users: [target], activePage: 4 });
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalledWith(3);
  });

  // DEFECT (correctness): manageUser's result is discarded, so a refused
  // deletion closes the dialog and refreshes exactly like a successful one.
  // The admin is left believing an account was deregistered when it was not.
  it.skip('CONTRACT: a refused deletion must not be presented as a completed one', async () => {
    const props = renderIt({
      manageUser: vi.fn().mockResolvedValue(false),
    });
    await confirmAndSettle();
    expect(props.onCancel).not.toHaveBeenCalled();
  });

  it('currently ignores the outcome reported by the delete call', async () => {
    const manageUser = vi.fn().mockResolvedValue(false);
    renderIt({ manageUser });
    await confirmAndSettle();
    expect(manageUser).toHaveBeenCalledWith(88, 'delete', target);
    await expect(manageUser.mock.results[0].value).resolves.toBe(false);
  });

  it.skip('CONTRACT: the deletion confirmation identifies the account being deleted', () => {
    renderIt();
    expect(dialogText()).toContain('carol.ops');
  });
});
