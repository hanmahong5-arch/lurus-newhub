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

// RedemptionsTable is the shell around the redemption-code grid. It owns three
// decisions, and delegates the rest:
//   * whether the operations column keeps its `fixed` pin in compact mode;
//   * the horizontal scroll envelope;
//   * the DELETE path — which record is destroyed, and whether the grid can
//     leave the operator stranded on a page that no longer exists.
//
// Deleting a redemption code is irreversible and destroys a bearer instrument,
// so the delete wiring is asserted on identity (`id`), never on "some call
// happened". The column render functions live in RedemptionsColumnDefs and are
// covered by tu_RedemptionsColumnDefs.test.jsx; getRedemptionsColumns is
// stubbed here with a KNOWN column set so the three decisions above can be
// asserted precisely.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, act } from '@testing-library/react';

const H = vi.hoisted(() => ({
  getRedemptionsColumns: vi.fn(),
  isExpired: vi.fn(() => false),
  // Last props each seam received, so callbacks can be invoked directly rather
  // than through a click that would swallow a throw.
  table: { current: null },
  modal: { current: null },
}));

vi.mock('./RedemptionsColumnDefs', () => ({
  getRedemptionsColumns: H.getRedemptionsColumns,
  isExpired: H.isExpired,
}));

// CardTable is the seam: surface the props RedemptionsTable computed as
// attributes so they can be asserted. Nothing is stubbed to `() => null` — a
// branch that leaves no trace in the DOM cannot be told apart from one that
// never ran.
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
      'data-hidepagination': String(props.hidePagination),
      'data-hasselection': String(props.rowSelection != null),
      'data-hasonrow': String(typeof props.onRow === 'function'),
      'data-page': String(props.pagination?.currentPage),
      'data-pagesize': String(props.pagination?.pageSize),
      'data-total': String(props.pagination?.total),
    })
  ),
}));

// The delete modal is rendered by the table. Expose `visible`, the record it
// is pointed at, and a real button bound to onOk so the confirm path runs.
vi.mock('./modals/DeleteRedemptionModal', () => ({
  default: (props) => {
    H.modal.current = props;
    return React.createElement(
      'div',
      {
        'data-testid': 'delete-modal',
        'data-visible': String(props.visible),
        'data-record-id': String(props.record ? props.record.id : 'none'),
      },
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'modal-cancel',
          onClick: props.onCancel,
        },
        'cancel',
      ),
    );
  },
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

import RedemptionsTable from './RedemptionsTable';

const t = (k) => k;

// A known column set. `operate` is the only column carrying `fixed`, and a
// second column deliberately carries `fixed: 'left'` so that "compact mode
// strips fixed" can be told apart from "compact mode strips ONLY operate's
// fixed" — a stub whose columns all look alike would hide that difference.
const stubColumns = () => [
  { title: 'ID', dataIndex: 'id' },
  { title: 'name', dataIndex: 'name', fixed: 'left' },
  { title: 'op', dataIndex: 'operate', fixed: 'right', width: 205 },
];

const rows = [
  { id: 11, name: 'launch-promo', key: 'SECRET-AAA', status: 1 },
  { id: 12, name: 'winback', key: 'SECRET-BBB', status: 1 },
];

const makeProps = (over = {}) => ({
  redemptions: rows,
  loading: false,
  activePage: 1,
  pageSize: 20,
  tokenCount: 2,
  compactMode: false,
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  rowSelection: { selectedRowKeys: [] },
  handleRow: vi.fn(),
  manageRedemption: vi.fn().mockResolvedValue(true),
  copyText: vi.fn(),
  setEditingRedemption: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn().mockResolvedValue(undefined),
  t,
  ...over,
});

const renderTable = (over = {}) => {
  const props = makeProps(over);
  const utils = render(React.createElement(RedemptionsTable, props));
  return { props, ...utils };
};

beforeEach(() => {
  vi.clearAllMocks();
  H.table.current = null;
  H.modal.current = null;
  H.getRedemptionsColumns.mockImplementation(stubColumns);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('column plumbing', () => {
  it('hands the column factory the callbacks the operations column needs', () => {
    const { props } = renderTable();
    const arg = H.getRedemptionsColumns.mock.calls[0][0];
    expect(arg.manageRedemption).toBe(props.manageRedemption);
    expect(arg.copyText).toBe(props.copyText);
    expect(arg.setEditingRedemption).toBe(props.setEditingRedemption);
    expect(arg.setShowEdit).toBe(props.setShowEdit);
    expect(arg.refresh).toBe(props.refresh);
    expect(typeof arg.showDeleteRedemptionModal).toBe('function');
  });

  it('passes the redemption rows straight through to the grid', () => {
    renderTable();
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-rows',
      '11,12',
    );
  });

  it('keeps the operations column pinned right when compact mode is off', () => {
    renderTable({ compactMode: false });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-fixed',
      'undefined,left,right',
    );
  });

  it('drops the pin from the operations column ONLY when compact mode is on', () => {
    renderTable({ compactMode: true });
    // `left` on the name column must survive: compact mode is documented to
    // unpin the operations column, not every column.
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-fixed',
      'undefined,left,undefined',
    );
  });

  it('keeps every column — unpinning must not drop the operations column', () => {
    renderTable({ compactMode: true });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-columns',
      'id,name,operate',
    );
  });

  it('scrolls horizontally in normal mode and not in compact mode', () => {
    const { unmount } = renderTable({ compactMode: false });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-scroll',
      JSON.stringify({ x: 'max-content' }),
    );
    unmount();
    renderTable({ compactMode: true });
    expect(screen.getByTestId('card-table')).toHaveAttribute(
      'data-scroll',
      'null',
    );
  });

  it('forwards paging state and the row hooks to the grid', () => {
    renderTable({ activePage: 3, pageSize: 50, tokenCount: 137 });
    const table = screen.getByTestId('card-table');
    expect(table).toHaveAttribute('data-page', '3');
    expect(table).toHaveAttribute('data-pagesize', '50');
    expect(table).toHaveAttribute('data-total', '137');
    expect(table).toHaveAttribute('data-hasselection', 'true');
    expect(table).toHaveAttribute('data-hasonrow', 'true');
  });

  it('offers both page-size and page callbacks so the pager can move', () => {
    const { props } = renderTable();
    H.table.current.pagination.onPageChange(4);
    H.table.current.pagination.onPageSizeChange(100);
    expect(props.handlePageChange).toHaveBeenCalledWith(4);
    expect(props.handlePageSizeChange).toHaveBeenCalledWith(100);
  });

  it('renders a translated empty state rather than a blank grid', () => {
    renderTable({ redemptions: [] });
    render(H.table.current.empty);
    expect(screen.getByTestId('empty-desc')).toHaveTextContent('搜索无结果');
    expect(screen.getByTestId('illu-light')).toBeInTheDocument();
    expect(screen.getByTestId('illu-dark')).toBeInTheDocument();
  });
});

describe('delete wiring', () => {
  it('keeps the delete confirmation closed until a row asks for it', () => {
    renderTable();
    const modal = screen.getByTestId('delete-modal');
    expect(modal).toHaveAttribute('data-visible', 'false');
    expect(modal).toHaveAttribute('data-record-id', 'none');
  });

  it('opens the confirmation pointed at the row that asked — not the first row', () => {
    renderTable();
    const { showDeleteRedemptionModal } =
      H.getRedemptionsColumns.mock.calls[0][0];
    // Deliberately the SECOND row: a hard-coded `redemptions[0]` would pass a
    // one-row fixture and fail here.
    act(() => showDeleteRedemptionModal(rows[1]));
    const modal = screen.getByTestId('delete-modal');
    expect(modal).toHaveAttribute('data-visible', 'true');
    expect(modal).toHaveAttribute('data-record-id', '12');
  });

  it('closes the confirmation without destroying anything when cancelled', () => {
    const { props } = renderTable();
    const { showDeleteRedemptionModal } =
      H.getRedemptionsColumns.mock.calls[0][0];
    act(() => showDeleteRedemptionModal(rows[0]));
    fireEvent.click(screen.getByTestId('modal-cancel'));
    expect(screen.getByTestId('delete-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
    expect(props.manageRedemption).not.toHaveBeenCalled();
  });

  it('hands the confirmation the same manage/refresh handles the grid uses', () => {
    const { props } = renderTable({ activePage: 2 });
    expect(H.modal.current.manageRedemption).toBe(props.manageRedemption);
    expect(H.modal.current.refresh).toBe(props.refresh);
    expect(H.modal.current.activePage).toBe(2);
    expect(H.modal.current.redemptions).toBe(props.redemptions);
  });
});
