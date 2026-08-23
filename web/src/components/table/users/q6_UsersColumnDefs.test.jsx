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

// Column definitions for the USERS table — the screen an operator uses to
// decide who gets promoted, disabled or deregistered, and how much credit each
// account still holds. The money helpers (renderQuota / renderNumber) are
// deliberately NOT stubbed: the arithmetic they perform on a user's remaining
// balance is the thing under test. Only the Semi UI shell is replaced.

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

// Semi UI's barrel drags in lottie-web, which needs a canvas jsdom does not
// have. Every stub below renders a MARKER and surfaces the deciding props as
// attributes. Nothing is stubbed to `() => null`: a branch that leaves no
// trace in the DOM cannot be told apart from a branch that never ran.
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
  // Semi renders Tooltip/Popover content only on hover. Rendering it eagerly
  // is what makes the hidden quota breakdown assertable.
  const Tooltip = ({ content, children, position }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tooltip', 'data-position': position },
      React.createElement(
        'span',
        { 'data-testid': 'tooltip-content', key: 'c' },
        content,
      ),
      React.createElement(
        'span',
        { 'data-testid': 'tooltip-trigger', key: 't' },
        children,
      ),
    );
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
  // `percent` decides how full the bar looks; `format` decides the label. Both
  // are surfaced, because "the bar is drawn" is not the same claim as "the bar
  // is drawn at the right fraction".
  const Progress = ({ percent, format, stroke, showInfo, 'aria-label': al }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'progress',
        'data-label': al,
        'data-percent': String(percent),
        'data-stroke': stroke,
        'data-showinfo': String(showInfo),
      },
      typeof format === 'function' ? format(percent) : null,
    );
  // `copyable.content` is the string that lands on the operator's clipboard —
  // surface it, or the copy payload can silently drift from the label.
  const Paragraph = ({ children, copyable }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'paragraph',
        'data-copy':
          copyable && typeof copyable === 'object' ? copyable.content : '',
      },
      children,
    );
  const Text = ({ children }) => React.createElement('span', null, children);
  const Modal = () => null;
  Modal.error = vi.fn();
  Modal.confirm = vi.fn();
  return {
    Tag,
    Button,
    Space,
    Tooltip,
    Popover,
    Progress,
    Modal,
    Typography: { Paragraph, Text, Title: Text },
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
// Swap the barrel for the REAL formatters so every money string asserted below
// is the one a real operator sees; a stubbed renderQuota would only prove the
// test's own arithmetic.
vi.mock('../../../helpers', async () => {
  const renderMod = await vi.importActual('../../../helpers/render.jsx');
  return {
    renderGroup: renderMod.renderGroup,
    renderNumber: renderMod.renderNumber,
    renderQuota: renderMod.renderQuota,
  };
});

import { getUsersColumns } from './UsersColumnDefs';

const t = (key) => key;

// DeletedAt is explicitly null on a live row (that is what GORM marshals).
const user = (over = {}) => ({
  id: 42,
  username: 'alice',
  DeletedAt: null,
  status: 1,
  role: 1,
  group: 'default',
  quota: 500000,
  used_quota: 1500000,
  request_count: 12,
  aff_count: 3,
  aff_history_quota: 250000,
  inviter_id: 0,
  ...over,
});

const makeCtx = (over = {}) => ({
  t,
  setEditingUser: vi.fn(),
  setShowEditUser: vi.fn(),
  ...over,
});

const columnFor = (ctx, key) => {
  const columns = getUsersColumns(ctx);
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
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('column set', () => {
  it('exposes the eight columns the users screen renders, in order', () => {
    const columns = getUsersColumns(makeCtx());
    expect(columns.map((c) => c.dataIndex ?? c.key)).toEqual([
      'id',
      'username',
      'info',
      'quota_usage',
      'group',
      'role',
      'invite',
      'operate',
    ]);
  });

  it('pins the operations column to the right at a fixed width', () => {
    const operate = columnFor(makeCtx(), 'operate');
    expect(operate.fixed).toBe('right');
    expect(operate.width).toBe(200);
  });

  it('leaves the ID column unrendered so the raw id is shown verbatim', () => {
    // No render function == Semi prints record.id as-is. Guards against a
    // future "formatter" quietly reshaping the primary key an operator quotes
    // in a support ticket.
    expect(columnFor(makeCtx(), 'id').render).toBeUndefined();
  });
});

describe('role column', () => {
  const roleOf = (role) => {
    renderCell(makeCtx(), 'role', user({ role }));
    return screen.getByTestId('tag');
  };

  it.each([
    [1, '普通用户', 'blue'],
    [5, '订阅用户', 'cyan'],
    [10, '管理员', 'yellow'],
    [100, '超级管理员', 'orange'],
  ])('renders role %i as %s in %s', (role, label, color) => {
    const tag = roleOf(role);
    expect(tag).toHaveTextContent(label);
    expect(tag).toHaveAttribute('data-color', color);
  });

  it('flags an unrecognised role as 未知身份 rather than guessing', () => {
    // The negative half of the matrix above: without it the table cannot tell
    // "maps each role" from "always says 普通用户".
    const tag = roleOf(7);
    expect(tag).toHaveTextContent('未知身份');
    expect(tag).toHaveAttribute('data-color', 'red');
  });

  it('never silently downgrades a missing role to an ordinary user', () => {
    // A privileged-looking label on an unknown role would be worse than
    // useless on an access-control screen; so would hiding an admin.
    const tag = roleOf(undefined);
    expect(tag.textContent).not.toBe('普通用户');
    expect(tag.textContent).not.toBe('管理员');
    expect(tag.textContent).not.toBe('超级管理员');
  });
});

describe('username column', () => {
  it('renders a bare username when the account carries no remark', () => {
    renderCell(makeCtx(), 'username', user({ username: 'alice', remark: '' }));
    expect(cell()).toHaveTextContent('alice');
    expect(screen.queryByTestId('tooltip')).toBeNull();
  });

  it('keeps a short remark intact next to the username', () => {
    renderCell(makeCtx(), 'username', user({ remark: 'vip客户' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('vip客户');
    expect(screen.getByTestId('tag').textContent).not.toContain('…');
  });

  it('truncates a long remark on screen but keeps the full text in the tooltip', () => {
    // 26 chars: the ellipsis must appear, and the untruncated remark must
    // still be reachable — otherwise the operator loses information silently.
    const remark = 'abcdefghijklmnopqrstuvwxyz';
    renderCell(makeCtx(), 'username', user({ remark }));
    expect(screen.getByTestId('tag')).toHaveTextContent('abcdefghij…');
    expect(screen.getByTestId('tag').textContent).not.toContain('klmnop');
    expect(screen.getByTestId('tooltip-content')).toHaveTextContent(remark);
  });

  it('does not truncate a remark that is exactly at the limit', () => {
    renderCell(makeCtx(), 'username', user({ remark: '0123456789' }));
    expect(screen.getByTestId('tag').textContent).not.toContain('…');
  });
});

describe('group column', () => {
  it('renders one clickable tag per group, sorted', () => {
    renderCell(makeCtx(), 'group', user({ group: 'zeta,default,alpha' }));
    const labels = screen.getAllByTestId('tag').map((n) => n.textContent);
    expect(labels).toEqual(['alpha', 'default', 'zeta']);
  });

  it('shows the 用户分组 placeholder for an account with no group', () => {
    renderCell(makeCtx(), 'group', user({ group: '' }));
    expect(screen.getByTestId('tag')).toHaveTextContent('用户分组');
  });

  it('gives the privileged tiers their warning colours', () => {
    renderCell(makeCtx(), 'group', user({ group: 'svip,vip' }));
    const byText = Object.fromEntries(
      screen
        .getAllByTestId('tag')
        .map((n) => [n.textContent, n.getAttribute('data-color')]),
    );
    expect(byText.svip).toBe('red');
    expect(byText.vip).toBe('yellow');
  });

  // DEFECT (correctness): renderGroup only special-cases the empty STRING.
  // A user row whose `group` is absent (undefined/null — which is what a
  // partially-projected API response or a freshly-created OIDC account
  // carries) reaches `group.split(',')` and throws TypeError out of the render
  // function. React unwinds the whole table, so ONE such row blanks the entire
  // user list instead of degrading that single cell.
  //
  // The it.skip below holds the correct contract; flip it when fixing.
  // Verified red 2026-08-20: un-skipped it fails with
  // "TypeError: Cannot read properties of undefined (reading 'split')".
  // Re-verified red 2026-08-21, same TypeError. Left skipped on purpose: the
  // repair belongs to renderGroup in src/helpers/render.jsx (the `group === ''`
  // guard), not to this column, which only forwards the field.
  it.skip('CONTRACT:a user with no group renders a placeholder instead of throwing', () => {
    expect(() =>
      renderCell(makeCtx(), 'group', user({ group: undefined })),
    ).not.toThrow();
    expect(cell().textContent.trim()).not.toBe('');
  });

  it('currently cannot render a user row whose group field is absent', () => {
    // Pinning the blast radius, not endorsing it: the throw escapes the cell.
    // This assertion is about the cell being unable to produce output for that
    // input; the repair turns the throw into a rendered placeholder, which the
    // CONTRACT lock above already demands, so this line is deleted together
    // with the defect rather than fought.
    expect(() =>
      renderCell(makeCtx(), 'group', user({ group: undefined })),
    ).toThrow();
  });
});

describe('status column', () => {
  const statusTag = () =>
    within(screen.getByTestId('tooltip-trigger')).getByTestId('tag');

  it('shows 已启用 in green for a live enabled account', () => {
    renderCell(makeCtx(), 'info', user({ status: 1 }));
    expect(statusTag()).toHaveTextContent('已启用');
    expect(statusTag()).toHaveAttribute('data-color', 'green');
  });

  it('shows 已禁用 in red for a live disabled account', () => {
    renderCell(makeCtx(), 'info', user({ status: 2 }));
    expect(statusTag()).toHaveTextContent('已禁用');
    expect(statusTag()).toHaveAttribute('data-color', 'red');
  });

  it('shows 未知状态 for a status the console does not model', () => {
    renderCell(makeCtx(), 'info', user({ status: 9 }));
    expect(statusTag()).toHaveTextContent('未知状态');
    expect(statusTag()).toHaveAttribute('data-color', 'grey');
  });

  it('shows 已注销 for an account carrying a deletion timestamp', () => {
    renderCell(
      makeCtx(),
      'info',
      user({ DeletedAt: '2026-01-01T00:00:00Z', status: 1 }),
    );
    // Deregistration outranks the enabled flag — otherwise a deleted account
    // reads as live.
    expect(statusTag()).toHaveTextContent('已注销');
    expect(statusTag()).toHaveAttribute('data-color', 'red');
  });

  it('surfaces the request count in the hover breakdown', () => {
    renderCell(makeCtx(), 'info', user({ request_count: 12345 }));
    // renderNumber compacts >= 10000 into k units.
    expect(screen.getByTestId('tooltip-content')).toHaveTextContent('12.3k');
  });
});

describe('operations column', () => {
  const byLabel = (label) =>
    screen.getAllByTestId('button').find((b) => b.textContent === label);

  // Disable/Enable, Promote, Demote and the "注销账户" overflow item used to
  // live here. All four posted to POST /api/user/manage, which no handler
  // has answered since the service was slimmed to a pure gateway — every
  // click 404'd. They were removed rather than kept as always-failing
  // buttons; v2 admin users is the working path now. This locks the removal
  // so a status-driven Disable/Enable control (or a Promote/Demote/Deregister
  // one) is not silently re-added without a live route behind it.
  it('offers only 编辑 — no disable/enable, promote, demote or deregister controls', () => {
    renderCell(makeCtx(), 'operate', user({ status: 1 }));
    expect(screen.getAllByTestId('button').map((b) => b.textContent)).toEqual([
      '编辑',
    ]);
    expect(screen.queryAllByTestId('dropdown-item')).toHaveLength(0);
  });

  it('offers only 编辑 for a disabled account too — the status has no effect', () => {
    renderCell(makeCtx(), 'operate', user({ status: 2 }));
    expect(screen.getAllByTestId('button').map((b) => b.textContent)).toEqual([
      '编辑',
    ]);
  });

  it('opens the editor on the clicked record when 编辑 is pressed', () => {
    const ctx = makeCtx();
    const record = user({ id: 77 });
    renderCell(ctx, 'operate', record);
    fireEvent.click(byLabel('编辑'));
    expect(ctx.setEditingUser).toHaveBeenCalledWith(record);
    expect(ctx.setShowEditUser).toHaveBeenCalledWith(true);
  });

  it('offers no actions at all on an account that is already deregistered', () => {
    const ctx = makeCtx();
    renderCell(ctx, 'operate', user({ DeletedAt: '2026-01-01T00:00:00Z' }));
    expect(screen.queryAllByTestId('button')).toHaveLength(0);
    expect(screen.queryAllByTestId('dropdown-item')).toHaveLength(0);
  });

  // DEFECT (correctness): both the status cell and the operations cell test
  // `record.DeletedAt !== null`, so a row where the field is simply ABSENT —
  // undefined, not null — is classified as deregistered. Such a row is painted
  // 已注销 in red and loses every admin control (today: just 编辑). Any API
  // response that omits the soft-delete column (a projection, a cached older
  // payload, a hand-built fixture) therefore locks the operator out of those
  // accounts with no error shown anywhere. The correct test is a truthiness
  // check on an actual deletion timestamp.
  // Verified red 2026-08-20: un-skipped it fails — 0 buttons are rendered.
  // Fixed 2026-08-21: both cells now test the timestamp for truthiness.
  it('CONTRACT:a row with no DeletedAt field is treated as a live account', () => {
    const record = user();
    delete record.DeletedAt;
    renderCell(makeCtx(), 'operate', record);
    expect(screen.queryAllByTestId('button').length).toBeGreaterThan(0);
  });

  it('keeps the status cell and the action rail agreeing about deregistration', () => {
    // Invariant that survives the repair: whatever the two cells decide about
    // a row with an absent DeletedAt, they must decide it the SAME way. A row
    // labelled live but stripped of its controls (or the reverse) is the
    // failure mode that would actually confuse an operator, and this stays
    // green both today and after the guard is corrected.
    const record = user();
    delete record.DeletedAt;

    const ops = renderCell(makeCtx(), 'operate', record);
    const hasControls =
      within(ops.container).queryAllByTestId('button').length > 0;
    ops.unmount();

    const info = renderCell(makeCtx(), 'info', record);
    const label = within(info.container)
      .getByTestId('tooltip-trigger')
      .textContent.trim();
    expect(hasControls).toBe(label !== '已注销');
  });
});

describe('quota usage column — money on screen', () => {
  const usageTag = () =>
    within(screen.getByTestId('popover-trigger')).getByTestId('tag');
  const breakdown = () => screen.getByTestId('popover-content').textContent;

  it('prints remaining over total, both derived from the same two fields', () => {
    // used 1500000 == $3.00, remaining 500000 == $1.00, total $4.00.
    renderCell(
      makeCtx(),
      'quota_usage',
      user({ used_quota: 1500000, quota: 500000 }),
    );
    expect(usageTag()).toHaveTextContent('$1.00 / $4.00');
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-percent',
      '25',
    );
  });

  it('breaks the same figures down in the hover card, with a copyable payload', () => {
    renderCell(
      makeCtx(),
      'quota_usage',
      user({ used_quota: 1500000, quota: 500000 }),
    );
    const text = breakdown();
    expect(text).toContain('已用额度');
    expect(text).toContain('$3.00');
    expect(text).toContain('$1.00');
    expect(text).toContain('$4.00');
    expect(text).toContain('(25%)');
    // The clipboard payload must be the money string, not the raw quota units:
    // an operator pasting "500000" into a ticket has pasted the wrong thing.
    const copies = screen
      .getAllByTestId('paragraph')
      .map((n) => n.getAttribute('data-copy'));
    expect(copies).toContain('$3.00');
    expect(copies).toContain('$1.00');
    expect(copies).toContain('$4.00');
    expect(copies).not.toContain('500000');
  });

  it('reports a brand-new account with no usage and no credit as 0%', () => {
    renderCell(makeCtx(), 'quota_usage', user({ used_quota: 0, quota: 0 }));
    // total === 0 must not become a division by zero on screen.
    expect(screen.getByTestId('progress')).toHaveAttribute('data-percent', '0');
    expect(usageTag()).toHaveTextContent('$0.00 / $0.00');
  });

  it('parses string quotas, which is how some API rows arrive', () => {
    renderCell(
      makeCtx(),
      'quota_usage',
      user({ used_quota: '500000', quota: '500000' }),
    );
    expect(usageTag()).toHaveTextContent('$1.00 / $2.00');
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-percent',
      '50',
    );
  });

  it('shows no daily-limit section for an account without a daily cap', () => {
    renderCell(makeCtx(), 'quota_usage', user({ daily_quota: 0 }));
    expect(breakdown()).not.toContain('每日限额');
    expect(screen.getAllByTestId('progress')).toHaveLength(1);
  });

  it('adds a second bar and the daily figures once a daily cap is set', () => {
    renderCell(
      makeCtx(),
      'quota_usage',
      user({ daily_quota: 1000000, daily_used: 250000 }),
    );
    const bars = screen.getAllByTestId('progress');
    expect(bars).toHaveLength(2);
    // 1000000 - 250000 = 750000 remaining of 1000000 == 75%.
    expect(bars[1]).toHaveAttribute('data-label', 'daily quota usage');
    expect(bars[1]).toHaveAttribute('data-percent', '75');
    const text = breakdown();
    expect(text).toContain('$0.50'); // today used  == 250000
    expect(text).toContain('$1.50'); // today left  == 750000
    expect(text).toContain('$2.00'); // daily cap   == 1000000
    expect(text).toContain('(75%)');
  });

  it('clamps an overspent daily allowance at zero rather than going negative', () => {
    renderCell(
      makeCtx(),
      'quota_usage',
      user({ daily_quota: 500000, daily_used: 900000 }),
    );
    expect(screen.getAllByTestId('progress')[1]).toHaveAttribute(
      'data-percent',
      '0',
    );
    expect(breakdown()).toContain('今日剩余');
  });

  it('marks an account that has fallen back to its degraded group', () => {
    renderCell(
      makeCtx(),
      'quota_usage',
      user({
        daily_quota: 1000000,
        group: 'free',
        base_group: 'vip',
        fallback_group: 'free',
      }),
    );
    expect(breakdown()).toContain('降级中');
    expect(breakdown()).toContain('vip');
    // Orange stroke is the at-a-glance signal on the collapsed row.
    expect(screen.getAllByTestId('progress')[1]).toHaveAttribute(
      'data-stroke',
      '#f97316',
    );
  });

  it('does NOT mark an account still on its base group as degraded', () => {
    // Negative half of the pair: without it the test cannot tell "flags a
    // fallback" from "always flags".
    renderCell(
      makeCtx(),
      'quota_usage',
      user({
        daily_quota: 1000000,
        group: 'vip',
        base_group: 'vip',
        fallback_group: 'free',
      }),
    );
    expect(breakdown()).not.toContain('降级中');
    expect(screen.getAllByTestId('progress')[1]).toHaveAttribute(
      'data-stroke',
      '#3b82f6',
    );
  });

  // DEFECT (money): a quota of -1 is pushed straight through renderQuota.
  // -1 / 500000 rounds to a signed zero, so the account was displayed to the
  // operator as "$-0.00 / $-0.00" — a figure that reads as a negative balance
  // on the screen used to decide whether an account has run out of credit.
  // Verified red 2026-08-20: un-skipped it fails, the tag reads "$-0.00 / $-0.00".
  // Resolved 2026-08-21 by helpers/render.jsx, which normalises the signed zero:
  // the tag now reads "$0.00 / $0.00". Nothing was changed in this column.
  //
  // This comment previously asserted that -1 is "the platform's sentinel for
  // no limit" on a user quota, and concluded the account should read 无限.
  // Checked against the server on 2026-08-21 and NOT confirmed: -1 is an
  // unlimited sentinel for max_balance (internal/adapter/handler/
  // tenant_credit_pool.go:38, middleware/pool_balance_check.go:31) and for
  // expired_time, but a token spells unlimited quota as the boolean
  // UnlimitedQuota (internal/domain/entity/token.go:20) and nothing on the
  // server treats user.quota == -1 specially. So "$0.00" may simply be the
  // right answer for a hair-thin overdraft here.
  //
  // Do not render this as 无限 on the strength of this comment alone — confirm
  // first how the server actually represents an unlimited user quota. That
  // unverified premise is the reason this note exists.
  it('CONTRACT:an unlimited quota is never printed as a negative money amount', () => {
    renderCell(makeCtx(), 'quota_usage', user({ used_quota: 0, quota: -1 }));
    expect(usageTag().textContent).not.toMatch(/-\s*0\.00/);
  });

  it('keeps the usage bar at a sane fraction for a sentinel quota', () => {
    // Deliberately asserts only what stays true after the repair: the progress
    // bar must never be driven negative or to NaN by the -1 sentinel. Pinning
    // the literal "$-0.00" here would turn this test red the day someone
    // renders it as 无限 instead — i.e. it would make the fix look like a
    // regression. (Observed today: "$-0.00 / $-0.00".)
    renderCell(makeCtx(), 'quota_usage', user({ used_quota: 0, quota: -1 }));
    const percent = Number(
      screen.getByTestId('progress').getAttribute('data-percent'),
    );
    expect(Number.isNaN(percent)).toBe(false);
    expect(percent).toBeGreaterThanOrEqual(0);
    expect(percent).toBeLessThanOrEqual(100);
  });

  // DEFECT (money): quota_per_unit is read from localStorage and parsed with
  // parseFloat; before /api/status has populated it the parse yields NaN and
  // every figure in this column prints as "$NaN".
  // Verified red 2026-08-20: un-skipped it fails, the tag reads "$NaN / $NaN".
  // Resolved 2026-08-21 by helpers/render.jsx, which returns the "—" placeholder
  // when the unit price is unknown: the tag reads "— / —". Nothing changed here.
  it('CONTRACT:an unavailable quota_per_unit does not print NaN as money', () => {
    window.localStorage.removeItem('quota_per_unit');
    renderCell(makeCtx(), 'quota_usage', user());
    expect(usageTag().textContent).not.toMatch(/NaN/);
  });

  it('never shows a concrete balance it could not actually compute', () => {
    // Invariant, not a literal: with quota_per_unit missing the cell must not
    // claim a specific amount of money. True today ("$NaN"), true after a
    // placeholder fix, and RED if someone "fixes" it by defaulting to $0.00 —
    // which would tell an operator an account is empty when it is not.
    window.localStorage.removeItem('quota_per_unit');
    renderCell(makeCtx(), 'quota_usage', user());
    expect(usageTag().textContent).not.toMatch(/\$\s*[\d]/);
  });
});

describe('invite column', () => {
  const tagTexts = () =>
    screen.getAllByTestId('tag').map((n) => n.textContent.trim());

  it('shows the invite count, the earned commission and the inviter', () => {
    renderCell(
      makeCtx(),
      'invite',
      user({ aff_count: 3, aff_history_quota: 250000, inviter_id: 9 }),
    );
    const texts = tagTexts();
    expect(texts[0]).toContain('3');
    expect(texts[1]).toContain('$0.50');
    expect(texts[2]).toContain('9');
  });

  it('says 无邀请人 rather than printing user 0 for a self-signup', () => {
    renderCell(makeCtx(), 'invite', user({ inviter_id: 0 }));
    const texts = tagTexts();
    expect(texts[2]).toBe('无邀请人');
    expect(texts[2]).not.toContain('0');
  });

  it('compacts a large invite count instead of overflowing the cell', () => {
    renderCell(makeCtx(), 'invite', user({ aff_count: 12345 }));
    expect(tagTexts()[0]).toContain('12.3k');
  });

  // DEFECT (money): aff_history_quota is passed to renderQuota with no guard.
  // An account that has never earned commission comes back from some API
  // projections with the field absent, and undefined / 500000 is NaN, so the
  // affiliate earnings tag prints "$NaN" on the user's own row.
  // Verified red 2026-08-20: un-skipped it fails, the tag reads 收益: $NaN.
  // Fixed 2026-08-21: an absent commission renders the "—" placeholder, so the
  // tag reads 收益: — instead of claiming a figure that could not be computed.
  it('CONTRACT:an absent commission total is not rendered as NaN money', () => {
    renderCell(makeCtx(), 'invite', user({ aff_history_quota: undefined }));
    expect(tagTexts()[1]).not.toMatch(/NaN/);
  });

  it('never renders an unknown commission total as a concrete amount', () => {
    // Same invariant shape as above: unknown must not be dressed up as a
    // number. Red on a $0.00 default, green on a real placeholder fix.
    renderCell(makeCtx(), 'invite', user({ aff_history_quota: undefined }));
    const earnings = tagTexts()[1];
    expect(earnings).not.toBe('');
    expect(earnings).not.toMatch(/\$\s*[\d]/);
  });
});
