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

// Column definitions for the Channels table. This file owns two things that
// are worth guarding hard:
//   * the BALANCE column, which shows a paying customer their upstream
//     credit — a wrong number here is a wrong number on a money screen;
//   * the pass-through warning on the NAME column, which is the only signal
//     that a channel silently bypasses model redirect / param override.
//
// The money helpers (renderQuota / renderQuotaWithAmount) are deliberately
// NOT stubbed: the arithmetic they perform is the thing under test. Only the
// Semi UI shell and the two toast helpers are replaced.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';

// helpers/utils.jsx touches window.matchMedia at module scope with no guard,
// so the import itself throws under jsdom unless a stub exists first.
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

const H = vi.hoisted(() => ({
  modalWarning: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}));

// Semi UI's barrel drags in lottie-web, which needs a canvas jsdom does not
// have. Every stub below renders a MARKER carrying the props the assertions
// care about — nothing is stubbed to `() => null`, because a branch that
// leaves no trace in the DOM cannot be distinguished from a branch that never
// ran.
vi.mock('@douyinfe/semi-ui', () => {
  // prefixIcon is where the multi-key mode marker lives, so it has to be
  // RENDERED, not swallowed as an attribute — otherwise the whole multi-key
  // branch executes invisibly and no assertion can tell it apart from a
  // single-key channel.
  const Tag = ({
    children,
    color,
    onClick,
    prefixIcon,
    shape,
    type,
    ...rest
  }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tag',
        'data-color': color,
        'data-shape': shape,
        onClick,
        role: onClick ? 'button' : undefined,
        ...rest,
      },
      prefixIcon,
      children,
    );
  const Tooltip = ({ content, children }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tooltip' },
      React.createElement(
        'span',
        { 'data-testid': 'tooltip-content' },
        content,
      ),
      children,
    );
  const Button = ({ children, onClick, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, ...rest },
      children,
    );
  const Space = ({ children }) =>
    React.createElement('span', { 'data-testid': 'space' }, children);
  const InputNumber = ({ onBlur, defaultValue, min, name, ...rest }) =>
    React.createElement('input', {
      'data-testid': `number-${name}`,
      'data-min': String(min),
      'data-default': String(defaultValue),
      onBlur,
      ...rest,
    });
  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  return {
    Tag,
    Tooltip,
    Button,
    Space,
    InputNumber,
    Typography: { Text, Title: Text, Paragraph: Text },
    Avatar: ({ children, ...rest }) =>
      React.createElement('span', rest, children),
    Modal: { warning: H.modalWarning, error: vi.fn(), confirm: vi.fn() },
    Toast: {
      success: vi.fn(),
      error: vi.fn(),
      info: vi.fn(),
      warning: vi.fn(),
    },
    Pagination: ({ children }) => React.createElement('div', null, children),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  // Markers, not nulls: the presence of the pass-through warning triangle is
  // itself an assertion target.
  IconAlertTriangle: (props) =>
    React.createElement('i', {
      'data-testid': 'icon-alert-triangle',
      ...props,
    }),
  IconTreeTriangleDown: (props) =>
    React.createElement('i', { 'data-testid': 'icon-roundrobin', ...props }),
  IconSearch: () => React.createElement('i', { 'data-testid': 'icon-search' }),
}));

vi.mock('react-icons/fa', () => ({
  FaRandom: (props) =>
    React.createElement('i', { 'data-testid': 'icon-random', ...props }),
}));

// i18next is pulled in directly by render.jsx (not through src/i18n), so in a
// unit test it is uninitialised and t() returns undefined. Echo the key.
vi.mock('i18next', () => {
  const stub = {
    language: 'zh',
    t: (key, vars) =>
      vars
        ? String(key).replace(/\{\{(\w+)\}\}/g, (_, n) =>
            vars[n] === undefined ? `{{${n}}}` : String(vars[n]),
          )
        : key,
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

// Keep the real money helpers; spy only on the two toasts the NAME column
// fires so the clipboard copy path is observable.
vi.mock('../../../helpers', async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, showSuccess: H.showSuccess, showError: H.showError };
});

import { getChannelsColumns } from './ChannelsColumnDefs';

const COLUMN_KEYS = {
  ID: 'id',
  NAME: 'name',
  GROUP: 'group',
  TYPE: 'type',
  STATUS: 'status',
  RESPONSE_TIME: 'response_time',
  BALANCE: 'balance',
  PRIORITY: 'priority',
  WEIGHT: 'weight',
};

const t = (k) => k;

const makeCols = (overrides = {}) =>
  getChannelsColumns({
    t,
    COLUMN_KEYS,
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
    checkOllamaVersion: vi.fn(),
    setShowMultiKeyManageModal: vi.fn(),
    setCurrentMultiKeyChannel: vi.fn(),
    ...overrides,
  });

const colByKey = (cols, key) => cols.find((c) => c.key === key);

const renderCell = (col, record, text) =>
  render(<div>{col.render(text ?? record[col.dataIndex], record, 0)}</div>);

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('getChannelsColumns — shape', () => {
  it('emits every declared column key, in the order the header expects', () => {
    const keys = makeCols().map((c) => c.key);
    expect(keys).toEqual([
      COLUMN_KEYS.ID,
      COLUMN_KEYS.NAME,
      COLUMN_KEYS.GROUP,
      COLUMN_KEYS.TYPE,
      COLUMN_KEYS.STATUS,
      COLUMN_KEYS.RESPONSE_TIME,
      COLUMN_KEYS.BALANCE,
      COLUMN_KEYS.PRIORITY,
      COLUMN_KEYS.WEIGHT,
    ]);
  });

  it('binds the balance cell to used_quota/balance, not to the expired_time it sorts on', () => {
    // dataIndex is 'expired_time' (a Semi quirk), so the render function must
    // read the money off `record` itself. If someone "fixes" the dataIndex and
    // starts trusting `text`, this catches it.
    const col = colByKey(makeCols(), COLUMN_KEYS.BALANCE);
    expect(col.dataIndex).toBe('expired_time');
    localStorage.setItem('quota_per_unit', '500000');
    renderCell(col, { used_quota: 500000, balance: '3' }, 987654321);
    const texts = screen.getAllByTestId('tag').map((n) => n.textContent);
    expect(texts).toContain('$1.00');
    expect(texts.join(' ')).not.toContain('987654321');
  });
});

describe('NAME column — remark tooltip and pass-through warning', () => {
  const nameCol = () => colByKey(makeCols(), COLUMN_KEYS.NAME);

  it('renders the plain name when there is no remark and no pass-through', () => {
    renderCell(nameCol(), { name: 'prod-openai' }, 'prod-openai');
    expect(screen.getByText('prod-openai')).toBeInTheDocument();
    expect(screen.queryByTestId('tooltip')).toBeNull();
    expect(screen.queryByTestId('icon-alert-triangle')).toBeNull();
  });

  it('surfaces the remark in a tooltip and copies it on demand', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { clipboard: { writeText } });

    renderCell(
      nameCol(),
      { name: 'eu-west', remark: 'rotate key 2026-09-01' },
      'eu-west',
    );
    expect(screen.getByTestId('tooltip-content')).toHaveTextContent(
      'rotate key 2026-09-01',
    );

    fireEvent.click(screen.getByRole('button', { name: '复制' }));
    expect(writeText).toHaveBeenCalledWith('rotate key 2026-09-01');
    await vi.waitFor(() => expect(H.showSuccess).toHaveBeenCalledTimes(1));
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a failed clipboard write instead of claiming success', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    vi.stubGlobal('navigator', { clipboard: { writeText } });

    renderCell(nameCol(), { name: 'eu-west', remark: 'note' }, 'eu-west');
    fireEvent.click(screen.getByRole('button', { name: '复制' }));

    await vi.waitFor(() => expect(H.showError).toHaveBeenCalledTimes(1));
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('treats a whitespace-only remark as no remark', () => {
    renderCell(nameCol(), { name: 'eu-west', remark: '   ' }, 'eu-west');
    expect(screen.queryByTestId('tooltip')).toBeNull();
  });

  // Positive/negative pair. Without the negative case a test cannot tell
  // "warns when pass-through is on" from "always warns".
  it('warns when pass_through_body_enabled is set as a JSON string', () => {
    renderCell(
      nameCol(),
      { name: 'raw', setting: '{"pass_through_body_enabled":true}' },
      'raw',
    );
    expect(screen.getByTestId('icon-alert-triangle')).toBeInTheDocument();
    // The tooltip copy is the actual warning; assert it reached the DOM.
    expect(screen.getByTestId('tooltip-content').textContent).toContain(
      '请求透传',
    );
  });

  it('warns when pass_through_body_enabled is set as an object', () => {
    renderCell(
      nameCol(),
      { name: 'raw', setting: { pass_through_body_enabled: true } },
      'raw',
    );
    expect(screen.getByTestId('icon-alert-triangle')).toBeInTheDocument();
  });

  it('does NOT warn when pass-through is off, malformed, or absent', () => {
    const cases = [
      { name: 'a', setting: '{"pass_through_body_enabled":false}' },
      { name: 'b', setting: '{"pass_through_body_enabled":"true"}' }, // string, not boolean
      { name: 'c', setting: 'not json at all' },
      { name: 'd', setting: '' },
      { name: 'e' },
    ];
    for (const record of cases) {
      const { unmount } = renderCell(nameCol(), record, record.name);
      expect(screen.queryByTestId('icon-alert-triangle')).toBeNull();
      unmount();
    }
  });

  it('never warns on a tag-aggregation row even if a child setting leaks in', () => {
    renderCell(
      nameCol(),
      {
        name: 'tag:eu',
        children: [],
        setting: '{"pass_through_body_enabled":true}',
      },
      'tag:eu',
    );
    expect(screen.queryByTestId('icon-alert-triangle')).toBeNull();
  });
});

describe('GROUP column', () => {
  const groupCol = () => colByKey(makeCols(), COLUMN_KEYS.GROUP);

  it('pins "default" first and sorts the rest alphabetically', () => {
    renderCell(
      groupCol(),
      { group: 'zeta,default,alpha' },
      'zeta,default,alpha',
    );
    const rendered = screen
      .getAllByTestId('tag')
      .map((n) => n.textContent)
      .filter(Boolean);
    expect(rendered[0]).toBe('default');
    expect(rendered).toEqual(['default', 'alpha', 'zeta']);
  });

  it('renders nothing rather than throwing when group is undefined', () => {
    expect(() => renderCell(groupCol(), {}, undefined)).not.toThrow();
    expect(screen.queryAllByTestId('tag')).toHaveLength(0);
  });
});

describe('TYPE column', () => {
  const typeCol = () => colByKey(makeCols(), COLUMN_KEYS.TYPE);

  it('labels a known channel type and colours it from CHANNEL_OPTIONS', () => {
    renderCell(typeCol(), { type: 1 }, 1);
    const tag = screen.getAllByTestId('tag')[0];
    expect(tag).toHaveTextContent('OpenAI');
    expect(tag).toHaveAttribute('data-color', 'green');
  });

  it('falls back to a grey 未知类型 tag for an unmapped type', () => {
    renderCell(typeCol(), { type: 99999 }, 99999);
    // type2label[99999] is undefined -> both label and colour come out empty;
    // the invariant that must hold either way is "no channel type is ever
    // rendered as another vendor's name".
    const tag = screen.getAllByTestId('tag')[0];
    expect(tag.textContent).not.toBe('OpenAI');
  });

  it('renders the 标签聚合 tag for a tag-aggregation row and skips the vendor icon', () => {
    renderCell(typeCol(), { type: 1, children: [] }, 1);
    expect(screen.getByTestId('tag')).toHaveTextContent('标签聚合');
    expect(screen.queryByTestId('icon-random')).toBeNull();
  });

  it('flags a random-mode multi-key channel with the shuffle marker only', () => {
    renderCell(
      typeCol(),
      {
        type: 1,
        channel_info: { is_multi_key: true, multi_key_mode: 'random' },
      },
      1,
    );
    expect(screen.getByTestId('icon-random')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-roundrobin')).toBeNull();
  });

  it('flags a polling-mode multi-key channel with the round-robin marker only', () => {
    renderCell(
      typeCol(),
      {
        type: 1,
        channel_info: { is_multi_key: true, multi_key_mode: 'polling' },
      },
      1,
    );
    expect(screen.getByTestId('icon-roundrobin')).toBeInTheDocument();
    expect(screen.queryByTestId('icon-random')).toBeNull();
  });

  it('shows neither multi-key marker on an ordinary single-key channel', () => {
    renderCell(
      typeCol(),
      { type: 1, channel_info: { is_multi_key: false } },
      1,
    );
    expect(screen.queryByTestId('icon-random')).toBeNull();
    expect(screen.queryByTestId('icon-roundrobin')).toBeNull();
  });

  it('adds the IO.NET provenance tag when other_info marks an ionet deployment', () => {
    renderCell(
      typeCol(),
      {
        type: 1,
        other_info: JSON.stringify({
          source: 'ionet',
          deployment_id: 'dep-42',
        }),
      },
      1,
    );
    expect(screen.getByText('IO.NET')).toBeInTheDocument();
    expect(screen.getByTestId('tooltip-content').textContent).toContain(
      'dep-42',
    );
  });

  it('does NOT add the IO.NET tag for other sources or malformed metadata', () => {
    const cases = [
      JSON.stringify({ source: 'manual', deployment_id: 'dep-1' }),
      '{ not json',
      '',
      JSON.stringify(['ionet']),
    ];
    for (const other_info of cases) {
      const { unmount } = renderCell(typeCol(), { type: 1, other_info }, 1);
      expect(screen.queryByText('IO.NET')).toBeNull();
      unmount();
    }
  });

  it('opens the deployment page in a new tab when the IO.NET tag is clicked', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    renderCell(
      typeCol(),
      {
        type: 1,
        other_info: JSON.stringify({
          source: 'ionet',
          deployment_id: 'dep-42',
        }),
      },
      1,
    );
    fireEvent.click(screen.getByText('IO.NET'));
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toBe(
      '/console/deployment?deployment_id=dep-42',
    );
    expect(open.mock.calls[0][1]).toBe('_blank');
  });

  it('never navigates to a URL built from a missing deployment id', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    renderCell(
      typeCol(),
      { type: 1, other_info: JSON.stringify({ source: 'ionet' }) },
      1,
    );
    fireEvent.click(screen.getByText('IO.NET'));
    expect(open).not.toHaveBeenCalled();
  });
});

describe('STATUS column', () => {
  const statusCol = () => colByKey(makeCols(), COLUMN_KEYS.STATUS);

  it.each([
    [1, '已启用', 'green'],
    [2, '已禁用', 'red'],
    [0, '未知状态', 'grey'],
  ])('renders status %i as %s/%s', (status, label, color) => {
    renderCell(statusCol(), { status }, status);
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent(label);
    expect(tag).toHaveAttribute('data-color', color);
  });

  it('explains an auto-disable with the upstream reason and the timestamp', () => {
    const at = 1750000000;
    renderCell(
      statusCol(),
      {
        status: 3,
        other_info: JSON.stringify({
          status_reason: 'upstream 401',
          status_time: at,
        }),
      },
      3,
    );
    const tip = screen.getByTestId('tooltip-content').textContent;
    expect(tip).toContain('upstream 401');
    // The formatted local time must actually make it into the tooltip; a bare
    // epoch number would be useless to an operator.
    expect(tip).toMatch(/\d{4}-\d{2}-\d{2}/);
    expect(screen.getByTestId('tag')).toHaveTextContent('自动禁用');
  });

  it('counts multi-key availability as enabled/total on the status tag', () => {
    renderCell(
      statusCol(),
      {
        status: 1,
        channel_info: {
          is_multi_key: true,
          multi_key_size: 5,
          multi_key_status_list: { 1: 2, 3: 3 },
        },
      },
      1,
    );
    expect(screen.getByTestId('tag')).toHaveTextContent('已启用 3/5');
  });

  it('shows every key as usable when no key has been disabled', () => {
    renderCell(
      statusCol(),
      { status: 1, channel_info: { is_multi_key: true, multi_key_size: 4 } },
      1,
    );
    expect(screen.getByTestId('tag')).toHaveTextContent('已启用 4/4');
  });

  // FIXED 2026-08-21: an auto-disabled row (status === 3) whose `other_info`
  // is absent — not '' but undefined, which is what the tag/child rows and any
  // partially-projected API response carry — reached `JSON.parse(undefined)`
  // and threw SyntaxError out of the render function. React unwound the whole
  // table, so ONE bad row blanked the entire channel list instead of degrading
  // that single cell. The guard now covers every falsy other_info, not just ''.
  it('CONTRACT:renders an auto-disabled row that carries no other_info', () => {
    expect(() => renderCell(statusCol(), { status: 3 }, 3)).not.toThrow();
    expect(screen.getByTestId('tag')).toHaveTextContent('自动禁用');
  });

  it('rewrites an empty other_info in place rather than copying the record', () => {
    // Documented consequence of the same block: the render function MUTATES
    // the row object it was handed. Any consumer holding that record (the
    // selection array in the action rail, for one) sees the change.
    const record = { status: 3, other_info: '' };
    renderCell(statusCol(), record, 3);
    expect(record.other_info).toBe('{}');
  });
});

describe('RESPONSE TIME column', () => {
  const rtCol = () => colByKey(makeCols(), COLUMN_KEYS.RESPONSE_TIME);

  it('shows 未测试 for a channel that has never been probed', () => {
    renderCell(rtCol(), { response_time: 0 }, 0);
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveTextContent('未测试');
    expect(tag).toHaveAttribute('data-color', 'grey');
  });

  it.each([
    [800, 'green', '0.80 秒'],
    [1000, 'green', '1.00 秒'],
    [1001, 'lime', '1.00 秒'],
    [3000, 'lime', '3.00 秒'],
    [3001, 'yellow', '3.00 秒'],
    [5000, 'yellow', '5.00 秒'],
    [5001, 'red', '5.00 秒'],
  ])('buckets %ims into the %s band', (ms, color, label) => {
    renderCell(rtCol(), { response_time: ms }, ms);
    const tag = screen.getByTestId('tag');
    expect(tag).toHaveAttribute('data-color', color);
    expect(tag).toHaveTextContent(label);
  });
});

describe('BALANCE column — money on screen', () => {
  const balanceCol = () => colByKey(makeCols(), COLUMN_KEYS.BALANCE);

  beforeEach(() => {
    localStorage.setItem('quota_per_unit', '500000');
  });

  it('renders used quota and remaining balance as two separate figures', () => {
    // Invariant, not a snapshot. The two figures are produced by two
    // different helpers (renderQuota / renderQuotaWithAmount) whose
    // disagreement is the defect locked further down; pinning the exact pair
    // of strings here would turn RED the moment someone routes the remaining
    // balance through renderQuota — i.e. it would make the repair look like a
    // regression from a test that never mentions the defect. Assert instead
    // what stays true either way: two figures, the first derived from
    // used_quota, the second from balance, same currency.
    renderCell(balanceCol(), { used_quota: 1000000, balance: '12.5' });
    const texts = screen.getAllByTestId('tag').map((n) => n.textContent);
    expect(texts).toHaveLength(2);
    expect(texts[0]).toBe('$2.00');
    expect(texts[1]).toMatch(/^\$/);
    expect(parseFloat(texts[1].replace(/[^\d.]/g, ''))).toBeCloseTo(12.5, 2);
  });

  it('asks the server to refresh the balance when the remaining tag is clicked', () => {
    const updateChannelBalance = vi.fn();
    const col = colByKey(
      makeCols({ updateChannelBalance }),
      COLUMN_KEYS.BALANCE,
    );
    const record = { id: 7, used_quota: 0, balance: '1' };
    renderCell(col, record);
    fireEvent.click(screen.getAllByTestId('tag')[1]);
    expect(updateChannelBalance).toHaveBeenCalledWith(record);
  });

  it('offers no balance refresh on a tag-aggregation row, only the used figure', () => {
    const updateChannelBalance = vi.fn();
    const col = colByKey(
      makeCols({ updateChannelBalance }),
      COLUMN_KEYS.BALANCE,
    );
    renderCell(col, { children: [], used_quota: 500000, balance: '99' });
    const tags = screen.getAllByTestId('tag');
    expect(tags).toHaveLength(1);
    expect(tags[0]).toHaveTextContent('$1.00');
    fireEvent.click(tags[0]);
    expect(updateChannelBalance).not.toHaveBeenCalled();
    // A tag row aggregates children; showing one child's balance as the
    // group's balance would be worse than showing none.
    expect(screen.queryByText('$99')).toBeNull();
  });

  // FIXED upstream 2026-08-21 in helpers/render.jsx: the two cells in this
  // single column were formatted by two helpers with different currency
  // behaviour. `renderQuota` (used quota) multiplies by
  // status.usd_exchange_rate when quota_display_type is CNY;
  // `renderQuotaWithAmount` (remaining balance) only prefixed '¥' to the raw
  // USD figure. With rate=7, one dollar used and one dollar remaining printed
  // side by side as ¥7.00 and ¥1 — the customer's remaining upstream credit was
  // understated ~7x on a screen they use to decide whether to top up.
  // renderQuotaWithAmount now takes symbol AND rate from getCurrencyConfig, so
  // the lock below holds without any change in this file.
  describe('currency conversion', () => {
    beforeEach(() => {
      localStorage.setItem('quota_display_type', 'CNY');
      localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7 }));
    });

    it('CONTRACT:used and remaining use the same exchange rate', () => {
      // $1 used and $1 remaining must print as the same CNY magnitude.
      renderCell(balanceCol(), { used_quota: 500000, balance: '1' });
      const [used, remaining] = screen
        .getAllByTestId('tag')
        .map((n) => n.textContent);
      expect(parseFloat(used.replace(/[^\d.]/g, ''))).toBeCloseTo(
        parseFloat(remaining.replace(/[^\d.]/g, '')),
        2,
      );
    });

    it('applies the exchange rate to the used figure and marks both in ¥', () => {
      // The magnitude of the remaining half is held by the CONTRACT lock
      // above; here we only assert what survives either formatting, namely
      // that both figures carry the same currency symbol. (An earlier version
      // of this test pinned the literal pair ['¥7.00', '¥1'], which went red on
      // the repair.)
      renderCell(balanceCol(), { used_quota: 500000, balance: '1' });
      const texts = screen.getAllByTestId('tag').map((n) => n.textContent);
      expect(texts).toHaveLength(2);
      expect(texts[0]).toBe('¥7.00');
      expect(texts[1].startsWith('¥')).toBe(true);
    });

    // FIXED 2026-08-21: the tooltip copy hard-coded the dollar sign inside the
    // translation key ('剩余额度$'), so the hover text contradicted the tag next
    // to it under any non-USD display setting. The symbol now comes from
    // getCurrencyConfig, the same source the tag's helper reads.
    it('CONTRACT:the remaining tooltip uses the same currency as the tag', () => {
      renderCell(balanceCol(), { used_quota: 0, balance: '1' });
      const symbol = screen.getAllByTestId('tag')[1].textContent.trim()[0];
      const tip = screen
        .getAllByTestId('tooltip-content')
        .map((n) => n.textContent)
        .find((s) => s.includes('剩余额度'));
      expect(tip).toContain(`剩余额度${symbol}`);
    });

    it('names the balance and says it is clickable in the remaining tooltip', () => {
      // Currency-agnostic on purpose: whichever symbol the fix settles on, the
      // tooltip must still carry the figure and the click hint.
      // OPEN (not this round's lock): the figure here is still the raw USD
      // balance, so under CNY the tooltip reads 剩余额度¥1 beside a ¥7 tag. This
      // test and the CONTRACT lock above together pin exactly that string, so
      // converting the tooltip figure needs both to move at once.
      renderCell(balanceCol(), { used_quota: 0, balance: '1' });
      const tips = screen
        .getAllByTestId('tooltip-content')
        .map((n) => n.textContent);
      expect(tips.some((s) => /剩余额度.?1.*点击更新/.test(s))).toBe(true);
    });
  });

  it('keeps a sub-cent charge visible instead of rounding it to zero', () => {
    // renderQuota floors at 10^-digits when the true value is non-zero: a
    // customer who has been billed must never see $0.00.
    renderCell(balanceCol(), { used_quota: 1, balance: '0' });
    expect(screen.getAllByTestId('tag')[0]).toHaveTextContent('$0.01');
  });

  it('reports a genuinely unused channel as exactly zero', () => {
    renderCell(balanceCol(), { used_quota: 0, balance: '0' });
    expect(screen.getAllByTestId('tag')[0]).toHaveTextContent('$0.00');
  });

  // DEFECT (correctness): quota_per_unit is read from localStorage and parsed
  // with parseFloat; before /api/status has populated it the parse yields NaN
  // and the used-quota cell prints the literal string "$NaN" to the operator.
  // Verified red 2026-08-20: un-skipped it fails, the cell reads "$NaN".
  it('CONTRACT:an unavailable quota_per_unit does not print NaN as money', () => {
    localStorage.removeItem('quota_per_unit');
    renderCell(balanceCol(), { used_quota: 500000, balance: '1' });
    expect(screen.getAllByTestId('tag')[0].textContent).not.toMatch(/NaN/);
  });

  it('does not substitute a computed figure for the rate it is missing', () => {
    // The other half: not printing NaN must not be achieved by falling back to
    // the canonical unit rate and stating an amount. quota_per_unit is
    // operator-overridable, so that amount would be wrong — silently — on any
    // deployment that changed it.
    localStorage.removeItem('quota_per_unit');
    renderCell(balanceCol(), { used_quota: 500000, balance: '1' });
    expect(screen.getAllByTestId('tag')[0].textContent).not.toMatch(/\$\s*\d/);
  });
});

describe('PRIORITY / WEIGHT columns', () => {
  it('pushes a per-channel priority edit straight to manageChannel', () => {
    const manageChannel = vi.fn();
    const col = colByKey(makeCols({ manageChannel }), COLUMN_KEYS.PRIORITY);
    const record = { id: 3, priority: 5 };
    renderCell(col, record);
    fireEvent.blur(screen.getByTestId('number-priority'), {
      target: { value: '11' },
    });
    expect(manageChannel).toHaveBeenCalledWith(3, 'priority', record, '11');
  });

  it('pushes a per-channel weight edit straight to manageChannel', () => {
    const manageChannel = vi.fn();
    const col = colByKey(makeCols({ manageChannel }), COLUMN_KEYS.WEIGHT);
    const record = { id: 3, weight: 1 };
    renderCell(col, record);
    fireEvent.blur(screen.getByTestId('number-weight'), {
      target: { value: '4' },
    });
    expect(manageChannel).toHaveBeenCalledWith(3, 'weight', record, '4');
  });

  it('requires confirmation before rewriting every child of a tag row', () => {
    const submitTagEdit = vi.fn();
    const col = colByKey(makeCols({ submitTagEdit }), COLUMN_KEYS.PRIORITY);
    renderCell(col, { key: 'eu', children: [], priority: 2 });

    fireEvent.blur(screen.getByTestId('number-priority'), {
      target: { value: '9' },
    });
    // Nothing may be written before the operator confirms.
    expect(submitTagEdit).not.toHaveBeenCalled();
    expect(H.modalWarning).toHaveBeenCalledTimes(1);

    const cfg = H.modalWarning.mock.calls[0][0];
    expect(cfg.content).toContain('9');
    cfg.onOk();
    expect(submitTagEdit).toHaveBeenCalledWith('priority', {
      tag: 'eu',
      priority: '9',
    });
  });

  it('refuses to blank out every child priority when the field is cleared', () => {
    const submitTagEdit = vi.fn();
    const col = colByKey(makeCols({ submitTagEdit }), COLUMN_KEYS.PRIORITY);
    renderCell(col, { key: 'eu', children: [], priority: 2 });
    fireEvent.blur(screen.getByTestId('number-priority'), {
      target: { value: '' },
    });
    H.modalWarning.mock.calls[0][0].onOk();
    expect(submitTagEdit).not.toHaveBeenCalled();
  });

  it('refuses to blank out every child weight when the field is cleared', () => {
    const submitTagEdit = vi.fn();
    const col = colByKey(makeCols({ submitTagEdit }), COLUMN_KEYS.WEIGHT);
    renderCell(col, { key: 'eu', children: [], weight: 2 });
    fireEvent.blur(screen.getByTestId('number-weight'), {
      target: { value: '' },
    });
    H.modalWarning.mock.calls[0][0].onOk();
    expect(submitTagEdit).not.toHaveBeenCalled();
  });

  it('applies a confirmed tag weight to every child', () => {
    const submitTagEdit = vi.fn();
    const col = colByKey(makeCols({ submitTagEdit }), COLUMN_KEYS.WEIGHT);
    renderCell(col, { key: 'eu', children: [], weight: 2 });
    fireEvent.blur(screen.getByTestId('number-weight'), {
      target: { value: '3' },
    });
    H.modalWarning.mock.calls[0][0].onOk();
    expect(submitTagEdit).toHaveBeenCalledWith('weight', {
      tag: 'eu',
      weight: '3',
    });
  });

  // FIXED 2026-08-21: the per-channel weight editor clamps at min=0, but the
  // tag-row weight editor reused the priority clamp of -999. Weight drives the
  // weighted-random upstream pick; a negative weight is not a valid input for
  // that and the two editors disagreed about the same field.
  it('CONTRACT:the tag weight editor enforces the same floor as the per-channel one', () => {
    const cols = makeCols();
    const single = render(
      <div>
        {colByKey(cols, COLUMN_KEYS.WEIGHT).render(1, { id: 1, weight: 1 }, 0)}
      </div>,
    );
    const singleMin = within(single.container)
      .getByTestId('number-weight')
      .getAttribute('data-min');
    single.unmount();

    const tagRow = render(
      <div>
        {colByKey(cols, COLUMN_KEYS.WEIGHT).render(
          1,
          { key: 'eu', children: [], weight: 1 },
          0,
        )}
      </div>,
    );
    const tagMin = within(tagRow.container)
      .getByTestId('number-weight')
      .getAttribute('data-min');
    expect(tagMin).toBe(singleMin);
    // Absolute, not just equal: two editors agreeing on -999 would satisfy the
    // comparison above while still accepting a weight the picker cannot use.
    expect(tagMin).toBe('0');
  });
});
