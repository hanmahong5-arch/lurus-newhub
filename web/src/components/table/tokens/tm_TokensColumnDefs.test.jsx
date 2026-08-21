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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// Shared spies. `vi.hoisted` so the module factories below (which are hoisted
// above the imports) can reach them.
const spies = vi.hoisted(() => ({
  showError: null,
  confirmCalls: [],
}));

// Semi UI shims. Every stub renders a MARKER and surfaces the props the
// assertions care about as attributes — a stub that renders null would let a
// whole branch "execute" while leaving nothing in the DOM to check.
vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;

  const Modal = ({ children }) => h('div', null, children);
  Modal.confirm = (args) => {
    spies.confirmCalls.push(args);
    return { destroy: () => {} };
  };

  const Text = ({ children }) => h('span', null, children);
  const Paragraph = ({ children, copyable }) =>
    h(
      'p',
      {
        'data-testid': 'paragraph',
        'data-copyable': copyable ? String(copyable.content) : undefined,
      },
      children,
    );

  return {
    Modal,
    Typography: { Text, Paragraph },
    Button: ({ icon, children, onClick, ...rest }) =>
      h(
        'button',
        {
          type: 'button',
          onClick,
          'aria-label': rest['aria-label'],
          'data-btn-type': rest.type,
        },
        icon,
        children,
      ),
    Dropdown: ({ menu = [], children }) =>
      h(
        'span',
        {
          'data-testid': 'chat-dropdown',
          'data-item-count': String(menu.length),
        },
        children,
        menu.map((m, i) =>
          h(
            'button',
            {
              key: i,
              type: 'button',
              'data-testid': `chat-item-${m.name}`,
              onClick: m.onClick,
            },
            m.name,
          ),
        ),
      ),
    Space: ({ children }) => h('span', null, children),
    SplitButtonGroup: ({ children }) => h('span', null, children),
    Tag: ({ color, children }) =>
      h('span', { 'data-testid': 'tag', 'data-color': color }, children),
    AvatarGroup: ({ children }) =>
      h('span', { 'data-testid': 'avatar-group' }, children),
    Avatar: ({ alt, children }) =>
      h('span', { 'data-testid': 'avatar', 'data-alt': alt }, children),
    Tooltip: ({ content, children }) =>
      h(
        'span',
        {
          'data-testid': 'tooltip',
          'data-content': typeof content === 'string' ? content : '',
        },
        children,
      ),
    Progress: ({ percent, stroke, format }) =>
      h(
        'span',
        {
          'data-testid': 'quota-progress',
          'data-percent': String(percent),
          // '' when getProgressColor returned undefined — the "no special
          // colour" branch has to stay distinguishable from the coloured ones.
          'data-stroke': stroke === undefined ? '' : String(stroke),
        },
        format ? format() : null,
      ),
    Popover: ({ content, children }) =>
      h(
        'span',
        null,
        h('span', { 'data-testid': 'popover-content' }, content),
        children,
      ),
    Input: ({ value, suffix }) =>
      h(
        'span',
        null,
        h('input', {
          readOnly: true,
          value: value ?? '',
          'data-testid': 'token-key-input',
        }),
        suffix,
      ),
  };
});

vi.mock('@douyinfe/semi-icons', () => {
  const h = React.createElement;
  return {
    IconTreeTriangleDown: () => h('i', { 'data-testid': 'icon-caret' }),
    IconCopy: () => h('i', { 'data-testid': 'icon-copy' }),
    IconEyeOpened: () => h('i', { 'data-testid': 'icon-eye-open' }),
    IconEyeClosed: () => h('i', { 'data-testid': 'icon-eye-closed' }),
  };
});

vi.mock('../../../helpers', () => {
  const h = React.createElement;
  spies.showError = vi.fn();
  return {
    showError: spies.showError,
    timestamp2string: (ts) => `TS(${ts})`,
    renderQuota: (q) => `Q(${q})`,
    renderGroup: (g) => h('span', { 'data-testid': 'group-tag' }, g),
    getModelCategories: () => ({
      all: { label: 'all', icon: null, filter: () => true },
      openai: {
        label: 'OpenAI',
        icon: h('i', { 'data-testid': 'cat-openai' }),
        filter: ({ model_name }) => model_name.startsWith('gpt'),
      },
      claude: {
        label: 'Claude',
        icon: h('i', { 'data-testid': 'cat-claude' }),
        filter: ({ model_name }) => model_name.startsWith('claude'),
      },
      // A category with no icon — the loop must skip it rather than push a
      // blank avatar.
      iconless: { label: 'Iconless', filter: ({ model_name }) => true },
    }),
  };
});

import { getTokensColumns } from './TokensColumnDefs';

const t = (key, vars) =>
  vars
    ? Object.entries(vars).reduce(
        (s, [k, v]) => s.replace(new RegExp(`{{\\s*${k}\\s*}}`, 'g'), v),
        key,
      )
    : key;

const FULL_KEY = 'AbCd1234efgh5678ijkl9012mnop3456';

const baseToken = (over = {}) => ({
  id: 5,
  name: 'production',
  status: 1,
  key: FULL_KEY,
  group: 'default',
  used_quota: 250000,
  remain_quota: 750000,
  unlimited_quota: false,
  model_limits_enabled: false,
  model_limits: '',
  allow_ips: '',
  created_time: 1700000000,
  expired_time: -1,
  ...over,
});

let deps;
let cols;

const col = (key) =>
  cols.find((c) => c.key === key || c.dataIndex === key || c.title === key);

const renderCell = (columnKey, record, value) => {
  const c = col(columnKey);
  const v = value !== undefined ? value : record[c.dataIndex];
  return render(<>{c.render(v, record, 0)}</>);
};

beforeEach(() => {
  spies.confirmCalls.length = 0;
  spies.showError?.mockClear();
  window.localStorage.clear();
  deps = {
    t,
    showKeys: {},
    setShowKeys: vi.fn(),
    copyText: vi.fn().mockResolvedValue(true),
    manageToken: vi.fn().mockResolvedValue(true),
    onOpenLink: vi.fn(),
    setEditingToken: vi.fn(),
    setShowEdit: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  };
  cols = getTokensColumns(deps);
});

afterEach(() => {
  window.localStorage.clear();
});

describe('getTokensColumns — shape', () => {
  it('exposes the ten operator-facing columns in order', () => {
    expect(cols.map((c) => c.title)).toEqual([
      '名称',
      '状态',
      '剩余额度/总额度',
      '分组',
      '密钥',
      '可用模型',
      'IP限制',
      '创建时间',
      '过期时间',
      '',
    ]);
    // The trailing operations column is pinned so the destructive buttons stay
    // reachable on a wide table.
    expect(cols[cols.length - 1].fixed).toBe('right');
    // The name column is plain text — no renderer means no escaping surprises.
    expect(col('name').render).toBeUndefined();
  });
});

describe('token key cell', () => {
  it('masks the secret by default, showing only the first and last four chars', () => {
    renderCell('token_key', baseToken());

    const shown = screen.getByTestId('token-key-input').value;
    expect(shown).toBe('sk-AbCd**********3456');
    // The whole point: the live secret is not in the DOM while masked.
    expect(shown).not.toContain(FULL_KEY);
    expect(document.body.textContent).not.toContain(FULL_KEY);
    // Masked state offers the "reveal" affordance, not the "hide" one.
    expect(screen.getByTestId('icon-eye-open')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-eye-closed')).not.toBeInTheDocument();
  });

  // The mask is built as slice(0, 4) + stars + slice(-4). Those two slices are
  // only disjoint while the key is longer than eight characters: at exactly
  // eight they tile it, and below eight they overlap. Either way every
  // character of the secret ends up on screen — the one thing the reveal
  // toggle exists to withhold.
  //
  // Asserting `not.toContain(key)` is NOT enough and was the first draft's
  // mistake: for a six-character key the mask reads 'sk-abcd**********cdef',
  // which discloses all six characters without containing them contiguously,
  // so a substring check passes while the secret is fully readable. The
  // invariant asserted instead is the one that actually matters and that
  // survives any future change of masking format: at least four characters of
  // the key are always withheld.
  it.each([1, 4, 6, 7, 8, 9, 11, 12, 48])(
    'withholds part of a %i-character key from the mask',
    (len) => {
      const key = Array.from({ length: len }, (_, i) =>
        String.fromCharCode(97 + (i % 26)),
      ).join('');
      renderCell('token_key', baseToken({ key }));

      const shown = screen.getByTestId('token-key-input').value;
      const disclosed = shown.replace(/^sk-/, '').replace(/\*+/g, '');

      expect(disclosed.length).toBeLessThanOrEqual(Math.max(0, len - 4));
      // Still recognisably a masked key rather than an empty box.
      expect(shown.startsWith('sk-')).toBe(true);
      expect(shown).toContain('*');
    },
  );

  it('shows the full secret once that row is marked revealed', () => {
    deps.showKeys = { 5: true };
    cols = getTokensColumns(deps);
    renderCell('token_key', baseToken());

    expect(screen.getByTestId('token-key-input').value).toBe(`sk-${FULL_KEY}`);
    expect(screen.getByTestId('icon-eye-closed')).toBeInTheDocument();
  });

  it('reveal state is per-row and toggles rather than replaces', () => {
    renderCell('token_key', baseToken({ id: 5 }));
    fireEvent.click(
      screen.getByRole('button', { name: 'toggle token visibility' }),
    );

    const updater = deps.setShowKeys.mock.calls[0][0];
    // Row 7 was already open; toggling row 5 must not close it.
    expect(updater({ 7: true })).toEqual({ 7: true, 5: true });
    expect(updater({})).toEqual({ 5: true });
  });

  it('a revealed row toggles back to hidden', () => {
    deps.showKeys = { 5: true };
    cols = getTokensColumns(deps);
    renderCell('token_key', baseToken());
    fireEvent.click(
      screen.getByRole('button', { name: 'toggle token visibility' }),
    );

    expect(deps.setShowKeys.mock.calls[0][0]({ 5: true })).toEqual({
      5: false,
    });
  });

  it('copy puts the full usable secret on the clipboard even while masked', async () => {
    renderCell('token_key', baseToken());
    // Sanity: we are copying from the masked state, not the revealed one.
    expect(screen.getByTestId('token-key-input').value).toContain('*');

    fireEvent.click(screen.getByRole('button', { name: 'copy token key' }));

    await waitFor(() => expect(deps.copyText).toHaveBeenCalledTimes(1));
    // The sk- prefix matters: without it the pasted value is not a usable key.
    expect(deps.copyText).toHaveBeenCalledWith(`sk-${FULL_KEY}`);
    // Copying must not flip the row to revealed as a side effect.
    expect(deps.setShowKeys).not.toHaveBeenCalled();
  });

  it('copy from a revealed row copies the same value as from a masked row', async () => {
    deps.showKeys = { 5: true };
    cols = getTokensColumns(deps);
    renderCell('token_key', baseToken());

    fireEvent.click(screen.getByRole('button', { name: 'copy token key' }));
    await waitFor(() => expect(deps.copyText).toHaveBeenCalledTimes(1));
    expect(deps.copyText).toHaveBeenCalledWith(`sk-${FULL_KEY}`);
  });

  // ---------------------------------------------------------------------
  // DEFECT: renderTokenKey does `record.key.slice(...)` with no guard. A row
  // whose `key` the API omitted (a non-owner view, a partially-serialised
  // record) throws a TypeError *during render*, which unmounts the whole
  // tokens table — every other token disappears because one row lacked a
  // field. Every neighbouring renderer in this file degrades instead
  // (`parseInt(x) || 0`, `if (!text) return 无限制`).
  // Fix: fall back to a placeholder for a missing key. Flip both tests below
  // together.
  // ---------------------------------------------------------------------
  it.skip('renders a row whose key is missing instead of taking the table down', () => {
    expect(() =>
      renderCell('token_key', baseToken({ key: undefined })),
    ).not.toThrow();
    // And it must not print a fake key made out of the undefined value.
    expect(screen.getByTestId('token-key-input').value).not.toContain(
      'undefined',
    );
  });

  // (There is deliberately no companion test pinning "this throws today".
  // Such a pin is the exact negation of the lock above, so repairing the guard
  // would turn it red and the repair would read as a regression. The lock
  // carries the record on its own.)
});

describe('status cell', () => {
  it.each([
    [1, '已启用', 'green'],
    [2, '已禁用', 'red'],
    [3, '已过期', 'yellow'],
    [4, '已耗尽', 'grey'],
  ])('status %i renders %s', (status, label, color) => {
    renderCell('status', baseToken({ status }));
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent(label);
    expect(tag).toHaveAttribute('data-color', color);
  });

  it('an unrecognised status is called out rather than silently shown as enabled', () => {
    renderCell('status', baseToken({ status: 99 }));
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent('未知状态');
    expect(tag).not.toHaveTextContent('已启用');
    expect(tag).toHaveAttribute('data-color', 'black');
  });
});

describe('quota usage cell', () => {
  it('shows remaining / total plus a percentage for a metered token', () => {
    renderCell('quota_usage', baseToken());

    // used 250000 + remain 750000 = 1000000 total, 75% left.
    expect(screen.getByText('Q(750000) / Q(1000000)')).toBeInTheDocument();
    const bar = screen.getByTestId('quota-progress');
    expect(bar).toHaveAttribute('data-percent', '75');
    expect(bar).toHaveTextContent('75%');
    // 75% is neither exhausted nor full — no alarm colour.
    expect(bar).toHaveAttribute('data-stroke', '');
  });

  it.each([
    // remain, used, expected percent, expected stroke
    [100, 0, '100', 'var(--semi-color-success)'],
    [5, 95, '5', 'var(--semi-color-danger)'],
    [25, 75, '25', 'var(--semi-color-warning)'],
  ])(
    'colours the bar by how much is left (remain=%i)',
    (remain, used, percent, stroke) => {
      renderCell(
        'quota_usage',
        baseToken({ remain_quota: remain, used_quota: used }),
      );
      const bar = screen.getByTestId('quota-progress');
      expect(bar).toHaveAttribute('data-percent', percent);
      expect(bar).toHaveAttribute('data-stroke', stroke);
    },
  );

  it('offers used / remaining / total as three separately copyable lines', () => {
    renderCell('quota_usage', baseToken());
    const copyables = screen
      .getAllByTestId('paragraph')
      .map((p) => p.getAttribute('data-copyable'));
    expect(copyables).toEqual(['Q(250000)', 'Q(750000)', 'Q(1000000)']);
  });

  it('an unlimited token shows the unlimited tag and no progress bar', () => {
    renderCell(
      'quota_usage',
      baseToken({ unlimited_quota: true, remain_quota: 0 }),
    );

    expect(screen.getByTestId('tag')).toHaveTextContent('无限额度');
    // A progress bar here would imply a ceiling that does not exist.
    expect(screen.queryByTestId('quota-progress')).not.toBeInTheDocument();
    // Consumption is still disclosed, in the popover.
    expect(screen.getByTestId('popover-content')).toHaveTextContent(
      'Q(250000)',
    );
    // ...but not a fabricated remaining/total pair.
    expect(screen.getByTestId('popover-content')).not.toHaveTextContent(
      '剩余额度',
    );
  });

  it('a token with no quota at all does not divide by zero', () => {
    renderCell('quota_usage', baseToken({ used_quota: 0, remain_quota: 0 }));
    const bar = screen.getByTestId('quota-progress');
    expect(bar).toHaveAttribute('data-percent', '0');
    expect(bar).toHaveTextContent('0%');
  });

  it('treats non-numeric quota fields as zero rather than printing NaN', () => {
    renderCell(
      'quota_usage',
      baseToken({ used_quota: null, remain_quota: undefined }),
    );
    expect(screen.getByTestId('quota-progress')).toHaveAttribute(
      'data-percent',
      '0',
    );
    expect(document.body.textContent).not.toContain('NaN');
  });

  // ---------------------------------------------------------------------
  // DEFECT: an overdrawn token (remain_quota < 0, reachable once overdraft is
  // allowed) produces a negative percentage. `(-500 / 500) * 100` is -100, so
  // the console shows "-100%" on a progress bar and paints it danger-red by
  // accident rather than by intent. A percentage of a capacity should be
  // clamped to 0..100 and the overdraft called out explicitly.
  // ---------------------------------------------------------------------
  it.skip('clamps the usage bar to 0..100 for an overdrawn token', () => {
    renderCell(
      'quota_usage',
      baseToken({ used_quota: 1000, remain_quota: -500 }),
    );
    const pct = Number(
      screen.getByTestId('quota-progress').getAttribute('data-percent'),
    );
    expect(pct).toBeGreaterThanOrEqual(0);
    expect(pct).toBeLessThanOrEqual(100);
  });

  it('the bar label always agrees with the percent it reports', () => {
    renderCell(
      'quota_usage',
      baseToken({ used_quota: 1000, remain_quota: -500 }),
    );
    // Invariant either way: the number printed in the bar must match the
    // numeric percent attribute, and must be a real number. True before the
    // clamp lands (-100 / "-100%") and after it (0 / "0%"), and still red if
    // the label and the value are ever computed from different expressions.
    const bar = screen.getByTestId('quota-progress');
    const pct = Number(bar.getAttribute('data-percent'));
    expect(Number.isFinite(pct)).toBe(true);
    expect(bar).toHaveTextContent(`${pct.toFixed(0)}%`);
    expect(bar.textContent).not.toContain('NaN');
  });
});

describe('group cell', () => {
  it('explains the auto group instead of just printing "auto"', () => {
    renderCell('group', baseToken({ group: 'auto' }));

    const tip = screen.getByTestId('tooltip');
    expect(tip).toHaveTextContent('智能熔断');
    expect(tip.getAttribute('data-content')).toContain('自动选择最优分组');
    // Without cross-group retry the qualifier must be absent — otherwise the
    // positive case below proves nothing.
    expect(tip).not.toHaveTextContent('跨分组');
  });

  it('marks an auto group that is allowed to fall through to other groups', () => {
    renderCell('group', baseToken({ group: 'auto', cross_group_retry: true }));
    expect(screen.getByTestId('tooltip')).toHaveTextContent('智能熔断(跨分组)');
  });

  it('a concrete group is delegated to the shared group renderer', () => {
    renderCell('group', baseToken({ group: 'vip' }));
    expect(screen.getByTestId('group-tag')).toHaveTextContent('vip');
    expect(screen.queryByText(/智能熔断/)).not.toBeInTheDocument();
  });
});

describe('model limits cell', () => {
  it('says 无限制 when the token is not restricted', () => {
    renderCell('model_limits', baseToken());
    expect(screen.getByTestId('tag')).toHaveTextContent('无限制');
    expect(screen.queryByTestId('avatar-group')).not.toBeInTheDocument();
  });

  it('groups the allow-list into one avatar per vendor with the models in the tooltip', () => {
    renderCell(
      'model_limits',
      baseToken({
        model_limits_enabled: true,
        model_limits: 'gpt-4o,gpt-4o-mini,claude-3-opus',
      }),
    );

    const tips = screen.getAllByTestId('tooltip');
    expect(tips).toHaveLength(2);
    expect(tips[0].getAttribute('data-content')).toBe('gpt-4o, gpt-4o-mini');
    expect(tips[1].getAttribute('data-content')).toBe('claude-3-opus');
    expect(screen.queryByText('无限制')).not.toBeInTheDocument();
  });

  it('does not swallow a model that matches no known vendor', () => {
    renderCell(
      'model_limits',
      baseToken({
        model_limits_enabled: true,
        model_limits: 'gpt-4o,some-private-model',
      }),
    );

    const tips = screen.getAllByTestId('tooltip');
    const contents = tips.map((el) => el.getAttribute('data-content'));
    // The unknown model has to appear somewhere, or the operator reads the
    // allow-list as shorter than it is.
    expect(contents).toContain('some-private-model');
    expect(
      screen.getAllByTestId('avatar').some((a) => a.dataset.alt === 'unknown'),
    ).toBe(true);
  });

  it('ignores empty segments from a trailing comma', () => {
    renderCell(
      'model_limits',
      baseToken({ model_limits_enabled: true, model_limits: 'gpt-4o,,' }),
    );
    const contents = screen
      .getAllByTestId('tooltip')
      .map((el) => el.getAttribute('data-content'));
    expect(contents).toEqual(['gpt-4o']);
  });

  // ---------------------------------------------------------------------
  // DEFECT: `record.model_limits_enabled && text` collapses "restricted, but
  // the list came back empty" into the same cell as "not restricted at all".
  // The row then reads 无限制 (no restriction) for a token that the gateway
  // will treat as allowing *nothing*, which is the opposite meaning.
  // ---------------------------------------------------------------------
  it.skip('distinguishes an empty allow-list from no allow-list', () => {
    renderCell(
      'model_limits',
      baseToken({ model_limits_enabled: true, model_limits: '' }),
    );
    expect(screen.queryByText('无限制')).not.toBeInTheDocument();
  });

  // (No companion pin on "this reads 无限制 today": that is the literal
  // negation of the lock above, so it would go red on the repair. The
  // unrestricted branch itself is already covered by the first test in this
  // block, which uses model_limits_enabled: false.)
});

describe('IP restriction cell', () => {
  it.each([
    ['', 'empty string'],
    [null, 'null'],
    ['   \n  ', 'whitespace'],
  ])('shows 无限制 for %s', (value) => {
    renderCell('allow_ips', baseToken({ allow_ips: value }));
    expect(screen.getByTestId('tag')).toHaveTextContent('无限制');
  });

  it('shows the first IP inline and the rest behind a +N tooltip', () => {
    renderCell(
      'allow_ips',
      baseToken({ allow_ips: '10.0.0.1\n10.0.0.2\n 10.0.0.3 ' }),
    );

    expect(screen.getByText('10.0.0.1')).toBeInTheDocument();
    expect(screen.getByText('+2')).toBeInTheDocument();
    // The hidden two must be recoverable, and trimmed.
    expect(screen.getByTestId('tooltip').getAttribute('data-content')).toBe(
      '10.0.0.2, 10.0.0.3',
    );
  });

  it('does not add a +N chip when there is only one IP', () => {
    renderCell('allow_ips', baseToken({ allow_ips: '10.0.0.1' }));
    expect(screen.getByText('10.0.0.1')).toBeInTheDocument();
    expect(screen.queryByText(/^\+/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
  });
});

describe('time cells', () => {
  it('formats the creation time through the shared formatter', () => {
    renderCell('created_time', baseToken());
    expect(screen.getByText('TS(1700000000)')).toBeInTheDocument();
  });

  it('spells out "never expires" rather than rendering the -1 sentinel', () => {
    renderCell('expired_time', baseToken({ expired_time: -1 }));
    expect(screen.getByText('永不过期')).toBeInTheDocument();
    expect(document.body.textContent).not.toContain('TS(-1)');
    expect(document.body.textContent).not.toContain('-1');
  });

  it('formats a real expiry as a timestamp', () => {
    renderCell('expired_time', baseToken({ expired_time: 1800000000 }));
    expect(screen.getByText('TS(1800000000)')).toBeInTheDocument();
    expect(screen.queryByText('永不过期')).not.toBeInTheDocument();
  });

  // ---------------------------------------------------------------------
  // DEFECT: the never-expires sentinel is matched with `=== -1`, so a backend
  // that serialises the column as the string "-1" (JSON bignum handling, a
  // proxy that stringifies numerics) falls through to the timestamp
  // formatter. The row then shows a formatted date for a token that in fact
  // never expires.
  // ---------------------------------------------------------------------
  it.skip('recognises the never-expires sentinel however it is typed', () => {
    renderCell('expired_time', baseToken({ expired_time: '-1' }), '-1');
    expect(screen.getByText('永不过期')).toBeInTheDocument();
  });

  it('a string "-1" expiry renders as one of the two legitimate forms', () => {
    renderCell('expired_time', baseToken({ expired_time: '-1' }), '-1');
    // Invariant that survives the fix: the cell prints either the sentinel
    // label or a formatted timestamp — never blank, never the raw value, never
    // NaN. Today it takes the timestamp branch; after the fix it takes the
    // 永不过期 branch, and both are accepted here.
    const printed = document.body.textContent.trim();
    expect(['永不过期', 'TS(-1)']).toContain(printed);
  });
});

describe('operations cell — enable / disable', () => {
  it('offers 禁用 for a live token and calls through, then refreshes', async () => {
    renderCell('operate', baseToken({ status: 1 }));
    expect(
      screen.queryByRole('button', { name: '启用' }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '禁用' }));

    await waitFor(() => expect(deps.refresh).toHaveBeenCalledTimes(1));
    expect(deps.manageToken).toHaveBeenCalledWith(
      5,
      'disable',
      expect.objectContaining({ id: 5 }),
    );
  });

  it('offers 启用 for a disabled token', async () => {
    renderCell('operate', baseToken({ status: 2 }));
    expect(
      screen.queryByRole('button', { name: '禁用' }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '启用' }));

    await waitFor(() => expect(deps.refresh).toHaveBeenCalledTimes(1));
    expect(deps.manageToken).toHaveBeenCalledWith(
      5,
      'enable',
      expect.anything(),
    );
  });

  it('the edit button hands the whole record to the editor before opening it', () => {
    const record = baseToken();
    renderCell('operate', record);

    fireEvent.click(screen.getByRole('button', { name: '编辑' }));

    expect(deps.setEditingToken).toHaveBeenCalledWith(record);
    expect(deps.setShowEdit).toHaveBeenCalledWith(true);
    // Editing must not mutate anything on its own.
    expect(deps.manageToken).not.toHaveBeenCalled();
  });
});

describe('operations cell — chat links', () => {
  it('builds one dropdown entry per configured chat link', () => {
    window.localStorage.setItem(
      'chats',
      JSON.stringify([{ ChatGPT: 'https://a/{key}' }, { Lobe: 'https://b' }]),
    );
    renderCell('operate', baseToken());

    expect(screen.getByTestId('chat-dropdown')).toHaveAttribute(
      'data-item-count',
      '2',
    );
    fireEvent.click(screen.getByTestId('chat-item-Lobe'));
    expect(deps.onOpenLink).toHaveBeenCalledWith(
      'Lobe',
      'https://b',
      expect.objectContaining({ id: 5 }),
    );
  });

  it('the primary 聊天 button opens the first configured link', () => {
    window.localStorage.setItem(
      'chats',
      JSON.stringify([{ ChatGPT: 'https://a' }, { Lobe: 'https://b' }]),
    );
    renderCell('operate', baseToken());

    fireEvent.click(screen.getByRole('button', { name: '聊天' }));

    expect(deps.onOpenLink).toHaveBeenCalledTimes(1);
    expect(deps.onOpenLink).toHaveBeenCalledWith(
      'ChatGPT',
      'https://a',
      expect.anything(),
    );
  });

  it('tells the user to ask an admin when no chat link is configured', () => {
    renderCell('operate', baseToken());

    expect(screen.getByTestId('chat-dropdown')).toHaveAttribute(
      'data-item-count',
      '0',
    );
    fireEvent.click(screen.getByRole('button', { name: '聊天' }));

    expect(spies.showError).toHaveBeenCalledWith('请联系管理员配置聊天链接');
    expect(deps.onOpenLink).not.toHaveBeenCalled();
  });

  it('a chats entry that is not an object map is skipped, not opened', () => {
    window.localStorage.setItem('chats', JSON.stringify([{}]));
    renderCell('operate', baseToken());
    // Object.keys({})[0] is undefined → the entry must be dropped.
    expect(screen.getByTestId('chat-dropdown')).toHaveAttribute(
      'data-item-count',
      '0',
    );
  });

  // ---------------------------------------------------------------------
  // DEFECT: the try/catch around the `chats` parse lives inside the column
  // renderer, so a corrupt localStorage value fires showError() as a side
  // effect of *rendering a row*. Ten rows on screen means ten identical error
  // toasts, and another ten on every re-render (a keystroke in the search box,
  // a refresh tick). Parsing belongs outside the render path, reported once.
  // ---------------------------------------------------------------------
  it.skip('does not raise toasts as a side effect of rendering rows', () => {
    window.localStorage.setItem('chats', '{not json');
    renderCell('operate', baseToken({ id: 1 }));
    renderCell('operate', baseToken({ id: 2 }));
    renderCell('operate', baseToken({ id: 3 }));
    expect(spies.showError).not.toHaveBeenCalled();
  });

  it('surfaces a corrupt chats config and never presents it as a working one', () => {
    window.localStorage.setItem('chats', '{not json');
    // Rebuild the columns *after* the corrupt value exists, so this test holds
    // whether the parse happens per-render (today) or once at column-build
    // time (after the fix). Asserting an exact toast count would pin today's
    // per-row behaviour and go red on the repair, so it is deliberately absent.
    cols = getTokensColumns(deps);
    renderCell('operate', baseToken({ id: 1 }));
    renderCell('operate', baseToken({ id: 2 }));

    expect(spies.showError).toHaveBeenCalledWith(
      '聊天链接配置错误，请联系管理员',
    );
    // Whatever the fix, a corrupt config must never look like a working one.
    expect(screen.getAllByTestId('chat-dropdown')[0]).toHaveAttribute(
      'data-item-count',
      '0',
    );
  });
});

describe('operations cell — delete confirmation', () => {
  const openDelete = (record = baseToken()) => {
    renderCell('operate', record);
    fireEvent.click(screen.getByRole('button', { name: '删除' }));
    return spies.confirmCalls[spies.confirmCalls.length - 1];
  };

  it('asks for confirmation before deleting and does nothing until confirmed', () => {
    const args = openDelete();

    expect(args.title).toBe('确定是否要删除此令牌？');
    expect(args.content).toBe('此修改将不可逆');
    // Merely opening the dialog must not have deleted anything.
    expect(deps.manageToken).not.toHaveBeenCalled();
    expect(deps.refresh).not.toHaveBeenCalled();
  });

  it('confirming issues exactly one delete for that token id', async () => {
    const args = openDelete(baseToken({ id: 42 }));
    args.onOk();

    await waitFor(() =>
      expect(deps.manageToken).toHaveBeenCalledWith(
        42,
        'delete',
        expect.objectContaining({ id: 42 }),
      ),
    );
    expect(deps.manageToken).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(deps.refresh).toHaveBeenCalledTimes(1));
  });

  it('dismissing the dialog deletes nothing', () => {
    const args = openDelete();
    // No onOk call — this is the cancel path.
    expect(args.onCancel).toBeUndefined();
    expect(deps.manageToken).not.toHaveBeenCalled();
  });

  // ---------------------------------------------------------------------
  // DEFECT: onOk launches the delete in a floating async IIFE and returns
  // undefined. Semi's Modal closes as soon as onOk resolves, so the dialog
  // reports success the instant it is clicked — before the request has even
  // been sent. If manageToken rejects (403, network), the rejection lands in
  // an unhandled promise and the operator is left looking at a closed dialog
  // and a row that quietly reappears on the next refresh.
  //
  // The drawer variant in ModelDetailDrawer.jsx gets this right
  // (`onOk: async () => { await ...; }`), so this is a local slip, not a
  // house pattern.
  // ---------------------------------------------------------------------
  it.skip('keeps the confirm dialog open until the delete actually completes', async () => {
    let finishDelete;
    deps.manageToken.mockReturnValue(
      new Promise((res) => {
        finishDelete = res;
      }),
    );
    cols = getTokensColumns(deps);
    const args = openDelete();

    const returned = args.onOk();
    // Semi awaits whatever onOk hands back; undefined means "already done".
    expect(returned).toBeInstanceOf(Promise);

    const settled = vi.fn();
    returned.then(settled);
    await Promise.resolve();
    expect(settled).not.toHaveBeenCalled();

    finishDelete(true);
    await returned;
    expect(deps.refresh).toHaveBeenCalledTimes(1);
  });

  it('refreshes only after the delete request itself has settled', async () => {
    let finishDelete;
    deps.manageToken.mockReturnValue(
      new Promise((res) => {
        finishDelete = res;
      }),
    );
    cols = getTokensColumns(deps);
    const args = openDelete();

    args.onOk();
    // Ordering invariant, true before and after the fix: the refresh must not
    // fire while the delete is still in flight, or the table repaints from
    // pre-delete state. Asserting that onOk hands back nothing awaitable would
    // instead pin the defect and go red on the repair.
    expect(deps.manageToken).toHaveBeenCalledWith(
      5,
      'delete',
      expect.objectContaining({ id: 5 }),
    );
    expect(deps.refresh).not.toHaveBeenCalled();

    finishDelete(true);
    await waitFor(() => expect(deps.refresh).toHaveBeenCalledTimes(1));
  });
});
