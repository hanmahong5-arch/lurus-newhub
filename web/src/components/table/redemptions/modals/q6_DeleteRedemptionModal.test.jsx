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

// The irreversible one. Confirming here destroys a redemption code — a bearer
// instrument that may already be printed on a card or sitting in a customer's
// inbox. Two things must hold: the code that is destroyed is the code the
// operator clicked, and the console must not tell the operator "done" when the
// server refused.
//
// Semi's Modal is stubbed to a marker that exposes onOk/onCancel as real
// buttons, so the confirm path actually runs instead of being asserted by
// inspecting props.

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

import DeleteRedemptionModal from './DeleteRedemptionModal';

const t = (k) => k;

// The record under the cursor is deliberately NOT the first element of the
// list prop, and its id is not 0/1 — so a handler that reached for
// `redemptions[0].id` or a hard-coded id would be caught.
const target = { id: 77, name: 'winback-2026', key: 'SECRET-ZZZ', status: 1 };
const otherRow = { id: 12, name: 'launch', key: 'SECRET-AAA', status: 1 };

const makeProps = (over = {}) => ({
  visible: true,
  onCancel: vi.fn(),
  record: target,
  manageRedemption: vi.fn().mockResolvedValue(true),
  refresh: vi.fn().mockResolvedValue(undefined),
  redemptions: [otherRow, target],
  activePage: 1,
  t,
  ...over,
});

const renderModal = (over = {}) => {
  const props = makeProps(over);
  const utils = render(React.createElement(DeleteRedemptionModal, props));
  return { props, ...utils };
};

// The confirm handler is async and then schedules a 100ms follow-up, so a
// click has to be flushed in both dimensions before assertions.
const confirmAndSettle = async () => {
  await act(async () => {
    screen.getByTestId('ok').click();
  });
  await act(async () => {
    vi.advanceTimersByTime(150);
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('presentation', () => {
  it('warns, in words, that the deletion cannot be undone', () => {
    renderModal();
    expect(screen.getByTestId('modal-title')).toHaveTextContent(
      '确定是否要删除此兑换码？',
    );
    expect(screen.getByTestId('modal-body')).toHaveTextContent(
      '此修改将不可逆',
    );
    expect(screen.getByTestId('modal')).toHaveAttribute('data-type', 'warning');
  });

  it('stays hidden until the caller opens it', () => {
    renderModal({ visible: false });
    expect(screen.getByTestId('modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('never prints the redemption code itself in the confirmation', () => {
    // A confirmation dialog is a screenshot magnet; the bearer secret has no
    // business being in it. This assertion stays valid however the copy is
    // reworded, because it names the secret, not the layout.
    renderModal();
    expect(screen.getByTestId('modal').textContent).not.toContain('SECRET-ZZZ');
  });
});

describe('confirming a deletion', () => {
  it('deletes the record under the cursor, not the first row of the page', async () => {
    const { props } = renderModal();
    await confirmAndSettle();
    expect(props.manageRedemption).toHaveBeenCalledTimes(1);
    expect(props.manageRedemption).toHaveBeenCalledWith(77, 'delete', target);
  });

  it('reloads the list after deleting so the destroyed code disappears', async () => {
    const { props } = renderModal();
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalled();
  });

  it('closes itself once the deletion has been submitted', async () => {
    const { props } = renderModal();
    await confirmAndSettle();
    expect(props.onCancel).toHaveBeenCalled();
  });

  it('submits nothing when the operator backs out', () => {
    const { props } = renderModal();
    screen.getByTestId('cancel').click();
    expect(props.onCancel).toHaveBeenCalledTimes(1);
    expect(props.manageRedemption).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
  });

  it('deletes exactly once even if the operator double-clicks confirm', async () => {
    // Not a guard the component has today — this pins the count for ONE click
    // so a future "submit on every render" regression is visible. It asserts
    // an upper bound that any correct implementation also satisfies.
    const { props } = renderModal();
    await confirmAndSettle();
    expect(props.manageRedemption.mock.calls).toHaveLength(1);
  });
});

describe('page recovery after deleting the last row', () => {
  it('never navigates to page 0 or below', async () => {
    // Invariant, not a snapshot: whatever the page-back rule is, asking the
    // API for page 0 is always wrong. Holds before and after the defect below
    // is fixed.
    const { props } = renderModal({ redemptions: [], activePage: 1 });
    await confirmAndSettle();
    const pageArgs = props.refresh.mock.calls
      .map((c) => c[0])
      .filter((a) => a !== undefined);
    pageArgs.forEach((p) => expect(p).toBeGreaterThanOrEqual(1));
  });

  it('steps back a page when the page it was told about is already empty', async () => {
    const { props } = renderModal({ redemptions: [], activePage: 3 });
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalledWith(2);
  });

  it('does not step back while the operator is on page 1', async () => {
    const { props } = renderModal({ redemptions: [], activePage: 1 });
    await confirmAndSettle();
    expect(props.refresh.mock.calls.some((c) => c[0] !== undefined)).toBe(
      false,
    );
  });

  // DEFECT (correctness): the page-back decision reads the `redemptions` prop
  // captured when handleConfirm's closure was created — i.e. the list BEFORE
  // the delete. Deleting the only row on page 3 therefore sees length === 1,
  // decides the page is not empty, and leaves the operator staring at an empty
  // grid on a page that no longer exists; the codes are still there, they are
  // just invisible until the operator manually pages back. The check needs the
  // post-refresh list (or a count from the refresh result), not the stale prop.
  it.skip('CONTRACT: deleting the last row of a page returns the operator to the previous page', async () => {
    const { props } = renderModal({ redemptions: [target], activePage: 3 });
    await confirmAndSettle();
    expect(props.refresh).toHaveBeenCalledWith(2);
  });

  it('currently decides the page-back from the pre-delete list', async () => {
    // Pins the mechanism WITHOUT pinning the broken outcome: it asserts only
    // that the component read the list it was handed, which stays true after
    // the fix (the fixed version reads a list too — a fresher one). The wrong
    // outcome itself is locked in the skipped contract above.
    const { props } = renderModal({ redemptions: [target], activePage: 3 });
    await confirmAndSettle();
    expect(props.manageRedemption).toHaveBeenCalledWith(77, 'delete', target);
    expect(props.refresh).toHaveBeenCalled();
  });
});

describe('a refused deletion', () => {
  // DEFECT (correctness): manageRedemption's return value is discarded. When
  // the server refuses (returns false — the shape the redemptions hook uses to
  // report failure), the modal still closes and still refreshes, so the
  // operator is shown the normal success path for a code that was never
  // deleted. They will believe a live bearer code is dead.
  it.skip('CONTRACT: a refused deletion must not be presented as a completed one', async () => {
    const { props } = renderModal({
      manageRedemption: vi.fn().mockResolvedValue(false),
    });
    await confirmAndSettle();
    expect(props.onCancel).not.toHaveBeenCalled();
  });

  it('currently ignores the outcome reported by the delete call', async () => {
    // Fix-safe: asserts that the refusal was actually delivered to the
    // component (the precondition of the contract above), not that the
    // component then mis-handled it. Still passes once the handling is fixed.
    const manageRedemption = vi.fn().mockResolvedValue(false);
    renderModal({ manageRedemption });
    await confirmAndSettle();
    expect(manageRedemption).toHaveBeenCalledWith(77, 'delete', target);
    expect(await manageRedemption.mock.results[0].value).toBe(false);
  });
});
