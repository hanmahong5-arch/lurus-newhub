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

// Column visibility picker. Small, but it decides whether the BALANCE column
// is on screen at all, so "the checkbox state matches the stored visibility
// map" is worth pinning. Also covers the modal's three footer buttons, two of
// which currently do the same thing — see the defect note at the bottom.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

const H = vi.hoisted(() => ({ getChannelsColumns: vi.fn() }));

vi.mock('../ChannelsColumnDefs', () => ({
  getChannelsColumns: H.getChannelsColumns,
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: ({ visible, title, footer, children, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal', role: 'dialog' },
          React.createElement('span', { 'data-testid': 'modal-title' }, title),
          children,
          footer,
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-dismiss',
              onClick: onCancel,
            },
            'dismiss',
          ),
        )
      : null,
  Button: ({ children, onClick }) =>
    React.createElement('button', { type: 'button', onClick }, children),
  Checkbox: ({ checked, indeterminate, onChange, children }) =>
    React.createElement(
      'label',
      {
        'data-testid': 'checkbox',
        'data-indeterminate': String(!!indeterminate),
      },
      React.createElement('input', {
        type: 'checkbox',
        checked: !!checked,
        onChange: (e) => onChange({ target: { checked: e.target.checked } }),
        'aria-label': typeof children === 'string' ? children : undefined,
      }),
      children,
    ),
}));

import ColumnSelectorModal from './ColumnSelectorModal';

const COLUMNS = [
  { key: 'id', title: 'ID' },
  { key: 'name', title: '名称' },
  { key: 'balance', title: '已用/剩余' },
  // A column with no title must not render a nameless checkbox.
  { key: 'ghost', title: undefined },
];

const t = (k) => k;

const makeProps = (over = {}) => ({
  t,
  showColumnSelector: true,
  setShowColumnSelector: vi.fn(),
  visibleColumns: { id: true, name: true, balance: true },
  handleColumnVisibilityChange: vi.fn(),
  handleSelectAll: vi.fn(),
  initDefaultColumns: vi.fn(),
  COLUMN_KEYS: { ID: 'id', NAME: 'name', BALANCE: 'balance' },
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
  activePage: 1,
  channels: [],
  ...over,
});

const renderModal = (over = {}) => {
  const props = makeProps(over);
  render(<ColumnSelectorModal {...props} />);
  return props;
};

// The first checkbox is "全选"; the rest are one per titled column.
const boxes = () => screen.getAllByTestId('checkbox');
const boxFor = (label) =>
  boxes()
    .find((n) => n.textContent === label)
    .querySelector('input');

beforeEach(() => {
  vi.clearAllMocks();
  H.getChannelsColumns.mockReturnValue(COLUMNS);
});

describe('rendering', () => {
  it('renders nothing while closed', () => {
    renderModal({ showColumnSelector: false });
    expect(screen.queryByTestId('modal')).toBeNull();
  });

  it('offers one checkbox per titled column, plus select-all', () => {
    renderModal();
    // 3 titled columns + the 全选 box. The untitled 'ghost' column must not
    // produce a checkbox nobody can identify.
    expect(boxes()).toHaveLength(4);
    expect(screen.queryByText('undefined')).toBeNull();
  });

  it('checks exactly the columns the visibility map says are on', () => {
    renderModal({ visibleColumns: { id: true, name: false, balance: true } });
    expect(boxFor('ID')).toBeChecked();
    expect(boxFor('名称')).not.toBeChecked();
    expect(boxFor('已用/剩余')).toBeChecked();
  });

  it('treats a column absent from the map as unchecked, not as checked', () => {
    renderModal({ visibleColumns: { id: true } });
    expect(boxFor('名称')).not.toBeChecked();
    expect(boxFor('已用/剩余')).not.toBeChecked();
  });
});

describe('select-all state', () => {
  it('is checked and not indeterminate when every column is on', () => {
    renderModal({ visibleColumns: { id: true, name: true, balance: true } });
    expect(boxFor('全选')).toBeChecked();
    expect(boxes()[0].dataset.indeterminate).toBe('false');
  });

  it('is indeterminate when only some columns are on', () => {
    renderModal({ visibleColumns: { id: true, name: false, balance: false } });
    expect(boxFor('全选')).not.toBeChecked();
    expect(boxes()[0].dataset.indeterminate).toBe('true');
  });

  it('is neither checked nor indeterminate when every column is off', () => {
    renderModal({ visibleColumns: { id: false, name: false, balance: false } });
    expect(boxFor('全选')).not.toBeChecked();
    expect(boxes()[0].dataset.indeterminate).toBe('false');
  });

  it('asks to switch everything ON when nothing is on', async () => {
    const user = userEvent.setup();
    const props = renderModal({
      visibleColumns: { id: false, name: false, balance: false },
    });
    await user.click(boxFor('全选'));
    expect(props.handleSelectAll).toHaveBeenCalledWith(true);
  });

  it('asks to switch everything OFF when everything is on', async () => {
    const user = userEvent.setup();
    const props = renderModal({
      visibleColumns: { id: true, name: true, balance: true },
    });
    await user.click(boxFor('全选'));
    expect(props.handleSelectAll).toHaveBeenCalledWith(false);
  });
});

describe('individual toggles', () => {
  it('reports the column key and the new checked state', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(boxFor('已用/剩余'));
    // Hiding the money column is a real choice; it must be recorded against
    // the right key or the wrong column disappears.
    expect(props.handleColumnVisibilityChange).toHaveBeenCalledWith(
      'balance',
      false,
    );
  });

  it('reports re-enabling a hidden column', async () => {
    const user = userEvent.setup();
    const props = renderModal({
      visibleColumns: { id: true, name: true, balance: false },
    });
    await user.click(boxFor('已用/剩余'));
    expect(props.handleColumnVisibilityChange).toHaveBeenCalledWith(
      'balance',
      true,
    );
  });
});

describe('footer', () => {
  it('resets to the default column set without closing the modal', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByText('重置'));
    expect(props.initDefaultColumns).toHaveBeenCalledTimes(1);
    expect(props.setShowColumnSelector).not.toHaveBeenCalled();
  });

  it('closes on the modal dismiss affordance', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByTestId('modal-dismiss'));
    expect(props.setShowColumnSelector).toHaveBeenCalledWith(false);
  });

  // DEFECT (cosmetic): 取消 and 确定 are literally the same handler —
  // `() => setShowColumnSelector(false)`. Every checkbox change has already
  // been pushed to the parent by handleColumnVisibilityChange, which mutates
  // visibleColumns immediately, so there is nothing left for 确定 to commit
  // and nothing for 取消 to roll back. An operator who hides the balance
  // column, thinks better of it and presses 取消 keeps the change anyway.
  //
  // The lock asserts the fix-agnostic invariant: the two buttons must not be
  // the same operation. Whether the fix adds a snapshot-and-restore to 取消
  // or defers the writes until 确定, they end up doing different things.
  //
  // Verified red 2026-08-20: un-skipped it fails, both produce exactly
  // [['setShowColumnSelector', false]].
  it.skip('CONTRACT:取消 and 确定 are not the same operation', async () => {
    const user = userEvent.setup();

    // Each button gets its OWN mounted tree — rendering both into the same
    // body would let screen.getByText hit the first modal's button and make
    // the two look different for a reason that has nothing to do with the
    // defect.
    const record = async (label) => {
      const props = renderModal();
      await user.click(screen.getByText(label));
      const calls = {
        close: props.setShowColumnSelector.mock.calls.length,
        visibility: props.handleColumnVisibilityChange.mock.calls.length,
        selectAll: props.handleSelectAll.mock.calls.length,
        reset: props.initDefaultColumns.mock.calls.length,
      };
      cleanup();
      return calls;
    };

    expect(await record('取消')).not.toEqual(await record('确定'));
  });

  it('currently closes on 取消 without restoring anything', async () => {
    // Pinning the defect, not endorsing it — see the comment above. The
    // assertion is only that 取消 closes; it does not forbid a future 取消
    // from ALSO restoring, so it will not block the fix.
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByText('取消'));
    expect(props.setShowColumnSelector).toHaveBeenCalledWith(false);
    expect(props.handleColumnVisibilityChange).not.toHaveBeenCalled();
  });

  it('closes on 确定', async () => {
    const user = userEvent.setup();
    const props = renderModal();
    await user.click(screen.getByText('确定'));
    expect(props.setShowColumnSelector).toHaveBeenCalledWith(false);
  });
});
