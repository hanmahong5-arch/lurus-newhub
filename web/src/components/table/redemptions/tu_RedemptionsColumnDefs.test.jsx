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
import { render, screen, fireEvent, within } from '@testing-library/react';

// helpers/utils.jsx touches window.matchMedia at module scope with no guard,
// so the transitive import throws under jsdom unless a stub exists first.
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

// Semi UI shims. Every stub RENDERS A MARKER and surfaces the props the
// assertions below care about (Tag colour, Popover content, Dropdown menu
// items) as attributes / real DOM nodes. Nothing is stubbed to `() => null`:
// a stub that renders nothing would let a whole branch disappear from the
// product while the test stayed green.
vi.mock('@douyinfe/semi-ui', () => {
  const Tag = ({ children, color, shape, size, className, onClick }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-shape': shape,
        'data-size': size,
        className,
        onClick,
      },
      children,
    );
  const Button = ({ children, onClick, disabled, type, size, icon }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': 'button',
        'data-variant': type,
        'data-size': size,
        disabled: Boolean(disabled),
        onClick,
      },
      children === undefined ? (icon ?? null) : children,
    );
  const Space = ({ children }) =>
    React.createElement('span', { 'data-testid': 'space' }, children);
  // Semi renders Popover content only on hover; rendering it eagerly here is
  // what makes "the redemption code is exposed by the 查看 popover" assertable.
  const Popover = ({ content, children, position }) =>
    React.createElement(
      'span',
      { 'data-testid': 'popover', 'data-position': position },
      React.createElement(
        'span',
        { 'data-testid': 'popover-content', key: 'c' },
        content,
      ),
      React.createElement(
        'span',
        { 'data-testid': 'popover-trigger', key: 't' },
        children,
      ),
    );
  const Dropdown = ({ menu = [], children, trigger, position }) =>
    React.createElement(
      'span',
      { 'data-testid': 'dropdown', 'data-trigger': trigger },
      React.createElement(
        'span',
        { 'data-testid': 'dropdown-menu', key: 'm' },
        menu.map((item, i) =>
          React.createElement(
            'button',
            {
              key: i,
              type: 'button',
              'data-testid': 'dropdown-item',
              'data-variant': item.type,
              disabled: Boolean(item.disabled),
              onClick: item.onClick,
            },
            item.name,
          ),
        ),
      ),
      React.createElement(
        'span',
        { 'data-testid': 'dropdown-trigger', key: 't' },
        children,
      ),
    );
  const Modal = () => null;
  Modal.error = vi.fn();
  Modal.confirm = vi.fn();
  const Text = ({ children }) => React.createElement('span', null, children);
  return {
    Tag,
    Button,
    Space,
    Popover,
    Dropdown,
    Modal,
    Typography: { Text },
    Avatar: ({ children }) => React.createElement('span', null, children),
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconMore: () => React.createElement('i', { 'data-testid': 'icon-more' }),
}));

// render.jsx pulls i18next in directly; without init t() returns undefined.
vi.mock('i18next', () => {
  const stub = {
    language: 'zh',
    t: (key) => key,
    use() {
      return stub;
    },
    init: () => Promise.resolve(),
    changeLanguage: () => Promise.resolve(),
    on: () => {},
    off: () => {},
  };
  return { default: stub };
});

// The component imports the helpers BARREL, which drags axios/history in.
// Swap the barrel for the REAL money + timestamp formatters, so the money
// strings asserted below are the ones a user actually sees — a stubbed
// renderQuota would only prove the test's own arithmetic.
vi.mock('../../../helpers', async () => {
  const renderMod = await vi.importActual('../../../helpers/render.jsx');
  const utilsMod = await vi.importActual('../../../helpers/utils.jsx');
  return {
    renderQuota: renderMod.renderQuota,
    timestamp2string: utilsMod.timestamp2string,
  };
});

import { getRedemptionsColumns, isExpired } from './RedemptionsColumnDefs';
import { REDEMPTION_STATUS } from '../../../constants/redemption.constants';

const t = (key) => key;

const NOW = 1_800_000_000; // seconds
const HOUR = 3600;

const code = (over = {}) => ({
  id: 7,
  name: 'launch-promo',
  key: 'SECRET-BEARER-CODE-0001',
  status: REDEMPTION_STATUS.UNUSED,
  quota: 500000,
  created_time: NOW - HOUR,
  expired_time: 0,
  used_user_id: 0,
  ...over,
});

const makeCtx = () => ({
  t,
  manageRedemption: vi.fn(),
  copyText: vi.fn().mockResolvedValue(undefined),
  setEditingRedemption: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn(),
  redemptions: [],
  activePage: 1,
  showDeleteRedemptionModal: vi.fn(),
});

const columnFor = (ctx, key) => {
  const columns = getRedemptionsColumns(ctx);
  const found = columns.find((c) => c.dataIndex === key || c.key === key);
  if (!found) throw new Error(`no column for ${key}`);
  return found;
};

const renderCell = (ctx, key, record) => {
  const column = columnFor(ctx, key);
  return render(
    <div data-testid='cell'>
      {column.render(record[column.dataIndex], record, 0)}
    </div>,
  );
};

const cell = () => screen.getByTestId('cell');

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  // 500000 quota units == $1.00, the product default.
  window.localStorage.setItem('quota_per_unit', '500000');
  vi.spyOn(Date, 'now').mockReturnValue(NOW * 1000);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('isExpired', () => {
  it('is true only for an unused code whose expiry is in the past', () => {
    expect(isExpired(code({ expired_time: NOW - HOUR }))).toBe(true);
  });

  it('is false for an unused code whose expiry is still ahead', () => {
    expect(isExpired(code({ expired_time: NOW + HOUR }))).toBe(false);
  });

  it('treats expired_time 0 as "never expires", not as 1970', () => {
    // 0 < now is arithmetically true, so the explicit !== 0 guard is the only
    // thing standing between "never expires" and "every code is expired".
    expect(isExpired(code({ expired_time: 0 }))).toBe(false);
  });

  it('does not call an already-used or disabled code expired', () => {
    const past = { expired_time: NOW - HOUR };
    expect(isExpired(code({ ...past, status: REDEMPTION_STATUS.USED }))).toBe(
      false,
    );
    expect(
      isExpired(code({ ...past, status: REDEMPTION_STATUS.DISABLED })),
    ).toBe(false);
  });
});

describe('status column', () => {
  const statusText = () => cell().textContent;
  const statusColor = () =>
    screen.getByTestId('tag').getAttribute('data-color');

  it('shows 已过期 in orange for an unused code past its expiry', () => {
    renderCell(makeCtx(), 'status', code({ expired_time: NOW - HOUR }));
    expect(statusText()).toBe('已过期');
    expect(statusColor()).toBe('orange');
  });

  it('shows 未使用 in green for a live unused code', () => {
    renderCell(makeCtx(), 'status', code({ expired_time: NOW + HOUR }));
    expect(statusText()).toBe('未使用');
    expect(statusColor()).toBe('green');
  });

  it('shows 已禁用 in red for a disabled code', () => {
    renderCell(
      makeCtx(),
      'status',
      code({ status: REDEMPTION_STATUS.DISABLED }),
    );
    expect(statusText()).toBe('已禁用');
    expect(statusColor()).toBe('red');
  });

  it('shows 已使用 in grey for a redeemed code', () => {
    renderCell(makeCtx(), 'status', code({ status: REDEMPTION_STATUS.USED }));
    expect(statusText()).toBe('已使用');
    expect(statusColor()).toBe('grey');
  });

  it('falls back to 未知状态 for a status the console does not know', () => {
    renderCell(makeCtx(), 'status', code({ status: 42 }));
    expect(statusText()).toBe('未知状态');
    expect(statusColor()).toBe('black');
  });
});

describe('quota column', () => {
  it('formats the face value as money', () => {
    renderCell(makeCtx(), 'quota', code({ quota: 500000 }));
    expect(cell()).toHaveTextContent('$1.00');
  });

  it('formats a string quota (the API sends numbers as strings on some rows)', () => {
    renderCell(makeCtx(), 'quota', code({ quota: '2500000' }));
    expect(cell()).toHaveTextContent('$5.00');
  });

  // The contract a redemption row owes its reader: the face value cell is
  // either a number of money or an explicit placeholder. The cell used to
  // read "$NaN"; it now says 未知.
  it('CONTRACT: a missing quota must never render as money-shaped nonsense', () => {
    renderCell(makeCtx(), 'quota', code({ quota: undefined }));
    expect(cell().textContent).not.toMatch(/NaN/);
  });

  // DEFECT: renderQuota(parseInt(undefined)) === renderQuota(NaN) === "$NaN".
  // A redemption code is a bearer instrument; a row that claims its face value
  // is "$NaN" gives an operator no way to tell a zero-value code from a
  // malformed one, and the string is what gets read out to a customer.
  //
  // Asserting the literal "$NaN" here would pin the defect and turn red on the
  // repair. What is asserted instead is the invariant that outlives it: a code
  // whose face value is unknown must never be shown as a concrete amount of
  // money. That is true today ("$NaN"), true after a placeholder fix, and red
  // if someone "fixes" it by silently defaulting the face value to $0.00.
  it('never renders a missing face value as a concrete money amount', () => {
    renderCell(makeCtx(), 'quota', code({ quota: undefined }));
    const printed = cell().textContent;
    expect(printed.trim()).not.toBe('');
    expect(printed).not.toMatch(/\$\s*[\d.]+/);
  });
});

describe('time columns', () => {
  it('renders the creation timestamp as a local date-time string', () => {
    renderCell(makeCtx(), 'created_time', code({ created_time: NOW }));
    // Formatting is timezone-dependent, so assert the shape, not the digits.
    expect(cell().textContent).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  });

  it('renders 永不过期 rather than the epoch when expired_time is 0', () => {
    renderCell(makeCtx(), 'expired_time', code({ expired_time: 0 }));
    expect(cell().textContent).toBe('永不过期');
  });

  it('renders a real expiry as a date-time string', () => {
    renderCell(makeCtx(), 'expired_time', code({ expired_time: NOW + HOUR }));
    expect(cell().textContent).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  });
});

describe('redeemer column', () => {
  it('renders 无 when nobody has redeemed the code', () => {
    renderCell(makeCtx(), 'used_user_id', code({ used_user_id: 0 }));
    expect(cell().textContent).toBe('无');
  });

  it('renders the redeeming user id once the code is spent', () => {
    renderCell(makeCtx(), 'used_user_id', code({ used_user_id: 314 }));
    expect(cell().textContent).toBe('314');
  });
});

describe('operations column', () => {
  const byLabel = (label) =>
    screen.getAllByTestId('button').find((b) => b.textContent === label) ??
    screen.getAllByTestId('dropdown-item').find((b) => b.textContent === label);

  it('exposes the bearer code itself through the 查看 popover', () => {
    const ctx = makeCtx();
    renderCell(ctx, 'operate', code({ key: 'SECRET-BEARER-CODE-0001' }));
    expect(screen.getByTestId('popover-content')).toHaveTextContent(
      'SECRET-BEARER-CODE-0001',
    );
    expect(
      within(screen.getByTestId('popover-trigger')).getByTestId('button'),
    ).toHaveTextContent('查看');
  });

  it('copies the code — not the id or the name — when 复制 is pressed', async () => {
    const ctx = makeCtx();
    renderCell(ctx, 'operate', code({ key: 'SECRET-BEARER-CODE-0001' }));
    fireEvent.click(byLabel('复制'));
    expect(ctx.copyText).toHaveBeenCalledTimes(1);
    expect(ctx.copyText).toHaveBeenCalledWith('SECRET-BEARER-CODE-0001');
  });

  it('opens the editor on the clicked record when 编辑 is pressed', () => {
    const ctx = makeCtx();
    const record = code();
    renderCell(ctx, 'operate', record);
    fireEvent.click(byLabel('编辑'));
    expect(ctx.setEditingRedemption).toHaveBeenCalledWith(record);
    expect(ctx.setShowEdit).toHaveBeenCalledWith(true);
  });

  it('disables 编辑 for a code that has already been redeemed', () => {
    renderCell(makeCtx(), 'operate', code({ status: REDEMPTION_STATUS.USED }));
    expect(byLabel('编辑')).toBeDisabled();
  });

  it('keeps 编辑 enabled for an unused code', () => {
    renderCell(
      makeCtx(),
      'operate',
      code({ status: REDEMPTION_STATUS.UNUSED }),
    );
    expect(byLabel('编辑')).not.toBeDisabled();
  });

  it('offers 禁用 (and not 启用) for a live unused code', () => {
    const ctx = makeCtx();
    const record = code({ expired_time: NOW + HOUR });
    renderCell(ctx, 'operate', record);
    expect(byLabel('禁用')).toBeTruthy();
    expect(byLabel('启用')).toBeUndefined();

    fireEvent.click(byLabel('禁用'));
    expect(ctx.manageRedemption).toHaveBeenCalledWith(7, 'disable', record);
  });

  it('offers 启用 for a disabled code and wires it to the enable action', () => {
    const ctx = makeCtx();
    const record = code({ status: REDEMPTION_STATUS.DISABLED });
    renderCell(ctx, 'operate', record);
    expect(byLabel('禁用')).toBeUndefined();

    fireEvent.click(byLabel('启用'));
    expect(ctx.manageRedemption).toHaveBeenCalledWith(7, 'enable', record);
  });

  it('shows 启用 greyed out for a redeemed code — a spent code cannot be revived', () => {
    renderCell(makeCtx(), 'operate', code({ status: REDEMPTION_STATUS.USED }));
    expect(byLabel('启用')).toBeDisabled();
  });

  it('offers only 删除 for an expired code — no enable/disable toggle', () => {
    renderCell(makeCtx(), 'operate', code({ expired_time: NOW - HOUR }));
    const items = screen
      .getAllByTestId('dropdown-item')
      .map((b) => b.textContent);
    expect(items).toEqual(['删除']);
  });

  it('routes 删除 through the confirmation modal, never straight to the API', () => {
    const ctx = makeCtx();
    const record = code();
    renderCell(ctx, 'operate', record);
    fireEvent.click(byLabel('删除'));
    expect(ctx.showDeleteRedemptionModal).toHaveBeenCalledWith(record);
    expect(ctx.manageRedemption).not.toHaveBeenCalled();
  });

  it('marks 删除 as the danger-styled entry', () => {
    renderCell(makeCtx(), 'operate', code());
    expect(byLabel('删除').getAttribute('data-variant')).toBe('danger');
  });
});

describe('column set', () => {
  it('pins the operations column to the right at a fixed width', () => {
    const operate = columnFor(makeCtx(), 'operate');
    expect(operate.fixed).toBe('right');
    expect(operate.width).toBe(205);
  });

  it('exposes exactly the eight columns the table renders', () => {
    const columns = getRedemptionsColumns(makeCtx());
    expect(columns.map((c) => c.dataIndex)).toEqual([
      'id',
      'name',
      'status',
      'quota',
      'created_time',
      'expired_time',
      'used_user_id',
      'operate',
    ]);
  });
});
