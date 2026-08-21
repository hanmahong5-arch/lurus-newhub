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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// Semi UI's barrel pulls lottie-web -> canvas, which jsdom has not got. Every
// stand-in below renders a MARKER and surfaces the props that carry the
// decision under test (tag colour, tooltip copy, ellipsis row count, whether a
// suffix icon was attached) as attributes. Nothing is stubbed to render null:
// a branch that leaves no DOM trace cannot be asserted on.
vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  return {
    Tag: ({ children, color, shape, onClick }) =>
      h(
        'span',
        {
          'data-testid': 'tag',
          'data-color': String(color),
          'data-shape': shape,
          onClick,
        },
        children,
      ),
    Space: ({ children }) => h('div', { 'data-testid': 'space' }, children),
    Tooltip: ({ content, children }) =>
      h(
        'span',
        { 'data-testid': 'tooltip' },
        h('span', { 'data-testid': 'tooltip-content' }, content),
        children,
      ),
    Popover: ({ content, children }) =>
      h(
        'span',
        { 'data-testid': 'popover' },
        h('span', { 'data-testid': 'popover-content' }, content),
        children,
      ),
    Avatar: ({ children, color, size, onClick }) =>
      h(
        'span',
        {
          'data-testid': 'avatar',
          'data-color': String(color),
          'data-size': size,
          onClick,
        },
        children,
      ),
    Typography: {
      Text: ({ children }) =>
        h('span', { 'data-testid': 'typo-text' }, children),
      Paragraph: ({ children, ellipsis }) =>
        h(
          'p',
          {
            'data-testid': 'paragraph',
            'data-rows': String(ellipsis?.rows ?? ''),
            'data-tooltip': ellipsis?.showTooltip ? 'yes' : 'no',
          },
          children,
        ),
    },
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconHelpCircle: () =>
    React.createElement('i', { 'data-testid': 'help-icon' }),
}));

vi.mock('lucide-react', () => ({
  Route: () => React.createElement('i', { 'data-testid': 'route-icon' }),
}));

// The shared render helpers already have their own suites; here they are
// replaced by argument-recording markers so the assertions are about what the
// COLUMN chose to hand them (e.g. six decimal places for the money column,
// which provider's cache-pricing shape to use) rather than re-testing the
// helper. getLogOther is deliberately the REAL implementation -- one of the
// defects locked below is that it is allowed to throw straight through the
// column render, and a stubbed copy could not demonstrate that.
vi.mock('../../../helpers', async () => {
  const h = React.createElement;
  const actualLog = await vi.importActual('../../../helpers/log.js');
  return {
    getLogOther: actualLog.getLogOther,
    timestamp2string: (ts) => `TS(${ts})`,
    stringToColor: (s) => `color-${s}`,
    renderGroup: (g) =>
      h(
        'span',
        { 'data-testid': 'group-tag', 'data-group': String(g) },
        String(g),
      ),
    renderQuota: (q, digits) =>
      h(
        'span',
        {
          'data-testid': 'quota',
          'data-quota': String(q),
          'data-digits': String(digits),
        },
        `quota(${q})`,
      ),
    renderModelTag: (name, options = {}) =>
      h(
        'span',
        {
          'data-testid': 'model-tag',
          'data-model': String(name),
          'data-suffix': options.suffixIcon ? 'yes' : 'no',
          onClick: options.onClick,
        },
        String(name),
      ),
    renderModelPriceSimple: (...a) =>
      [
        'PRICE',
        `provider=${a[15]}`,
        `ratio=${a[0]}`,
        `price=${a[1]}`,
        `group=${a[2]}`,
        `userGroup=${a[3]}`,
        `cacheTok=${a[4]}`,
        `cacheRatio=${a[5]}`,
        `cc=${a[6]}`,
        `ccr=${a[7]}`,
        `cc5m=${a[8]}`,
        `ccr5m=${a[9]}`,
        `cc1h=${a[10]}`,
        `ccr1h=${a[11]}`,
        `sysPrompt=${a[14]}`,
      ].join('|'),
    renderClaudeLogContent: () => 'claude-log-content',
    renderLogContent: () => 'log-content',
    renderAudioModelPrice: () => 'audio-price',
    renderClaudeModelPrice: () => 'claude-price',
    renderModelPrice: () => 'model-price',
  };
});

import { getLogsColumns } from './UsageLogsColumnDefs';

// Mirrors useUsageLogsData's COLUMN_KEYS exactly.
const COLUMN_KEYS = {
  TIME: 'time',
  CHANNEL: 'channel',
  USERNAME: 'username',
  TOKEN: 'token',
  GROUP: 'group',
  TYPE: 'type',
  MODEL: 'model',
  USE_TIME: 'use_time',
  PROMPT: 'prompt',
  COMPLETION: 'completion',
  COST: 'cost',
  RETRY: 'retry',
  IP: 'ip',
  DETAILS: 'details',
};

const t = (s) => s;
let copyText;
let showUserInfoFunc;

const columns = (isAdminUser = true) =>
  getLogsColumns({ t, COLUMN_KEYS, copyText, showUserInfoFunc, isAdminUser });

const col = (key, isAdminUser = true) =>
  columns(isAdminUser).find((c) => c.key === key);

/** Render one cell and hand back the RTL result plus its text. */
const cell = (key, record, { admin = true, text } = {}) => {
  const definition = col(key, admin);
  const value = text === undefined ? record[definition.dataIndex] : text;
  const result = render(
    <div data-testid='cell'>{definition.render(value, record, 0)}</div>,
  );
  return { ...result, text: result.container.textContent };
};

beforeEach(() => {
  copyText = vi.fn(() => Promise.resolve(true));
  showUserInfoFunc = vi.fn();
});

describe('UsageLogsColumnDefs — column set', () => {
  it('publishes the fourteen console columns in display order', () => {
    expect(columns().map((c) => c.key)).toEqual([
      'time',
      'channel',
      'username',
      'token',
      'group',
      'type',
      'model',
      'use_time',
      'prompt',
      'completion',
      'cost',
      'ip',
      'retry',
      'details',
    ]);
  });

  it('pins each column to the log field it reads', () => {
    const map = Object.fromEntries(columns().map((c) => [c.key, c.dataIndex]));
    expect(map).toMatchObject({
      time: 'timestamp2string',
      channel: 'channel',
      username: 'username',
      token: 'token_name',
      group: 'group',
      type: 'type',
      model: 'model_name',
      use_time: 'use_time',
      prompt: 'prompt_tokens',
      completion: 'completion_tokens',
      cost: 'quota',
      ip: 'ip',
      retry: 'retry',
      details: 'content',
    });
  });

  it('pins only the details column to the right edge', () => {
    // Compact mode strips `fixed`; if another column gained it, the compact
    // layout would silently start reserving a frozen lane.
    expect(
      columns()
        .filter((c) => c.fixed)
        .map((c) => c.key),
    ).toEqual(['details']);
  });

  it('builds the same column list for admins and non-admins', () => {
    // Role only changes what a cell RENDERS, never the column list itself --
    // the column-selector modal relies on that to show a stable set.
    expect(columns(false).map((c) => c.key)).toEqual(
      columns(true).map((c) => c.key),
    );
  });

  it('renders the time column straight from the pre-formatted field', () => {
    expect(col('time').render).toBeUndefined();
  });
});

describe('UsageLogsColumnDefs — channel column', () => {
  const consumption = {
    type: 2,
    channel: 3,
    channel_name: 'openai-main',
    other: '',
  };

  it('shows the channel id tagged with the upstream name for an admin', () => {
    const { text } = cell('channel', consumption, { text: 3 });

    expect(text).toContain('3');
    expect(screen.getByTestId('tooltip-content')).toHaveTextContent(
      'openai-main',
    );
  });

  it('falls back to "未知渠道" when the channel name is missing', () => {
    cell('channel', { ...consumption, channel_name: '' }, { text: 3 });

    expect(screen.getByTestId('tooltip-content')).toHaveTextContent('未知渠道');
  });

  it('derives a stable colour from the channel id', () => {
    // colors[] has 15 entries, so id 3 and id 18 must land on the same swatch.
    cell('channel', consumption, { text: 3 });
    const first = screen.getByTestId('tag').getAttribute('data-color');
    expect(first).toBe('green');

    cell('channel', { ...consumption, channel: 18 }, { text: 18 });
    const swatches = screen
      .getAllByTestId('tag')
      .map((n) => n.getAttribute('data-color'));
    expect(swatches).toContain('green');
  });

  it('renders nothing for a non-admin viewer', () => {
    const { text } = cell('channel', consumption, { admin: false, text: 3 });

    expect(text).toBe('');
    expect(screen.queryByTestId('tag')).toBeNull();
  });

  it.each([
    [1, '充值'],
    [3, '管理'],
    [4, '系统'],
  ])('renders nothing for a type %i (%s) row', (type) => {
    const { text } = cell('channel', { ...consumption, type }, { text: 3 });

    expect(text).toBe('');
  });

  it.each([0, 2, 5])('renders the channel for a type %i row', (type) => {
    const { text } = cell('channel', { ...consumption, type }, { text: 3 });

    expect(text).toContain('3');
  });

  it('adds a second tag carrying the sub-key index on a multi-key channel', () => {
    const { text } = cell(
      'channel',
      {
        ...consumption,
        other: JSON.stringify({
          admin_info: { is_multi_key: true, multi_key_index: 7 },
        }),
      },
      { text: 3 },
    );

    expect(screen.getAllByTestId('tag')).toHaveLength(2);
    expect(text).toContain('7');
  });

  it('does NOT add the sub-key tag when the channel is single-key', () => {
    // Negative half of the pair above: without it "always renders two tags"
    // would pass just as well as "renders two tags only for multi-key".
    cell(
      'channel',
      {
        ...consumption,
        other: JSON.stringify({
          admin_info: { is_multi_key: false, multi_key_index: 7 },
        }),
      },
      { text: 3 },
    );

    expect(screen.getAllByTestId('tag')).toHaveLength(1);
  });

  it('does NOT add the sub-key tag when admin_info is absent', () => {
    cell(
      'channel',
      { ...consumption, other: JSON.stringify({ group: 'default' }) },
      { text: 3 },
    );

    expect(screen.getAllByTestId('tag')).toHaveLength(1);
  });

  // DEFECT: the channel cell hands record.other straight to getLogOther, which
  // is a bare JSON.parse. One corrupt `other` string anywhere in the page kills
  // the render of the WHOLE table (React unmounts the tree on a throw during
  // render), so a customer opening "why was I charged this?" sees a blank
  // screen instead of one odd row. The sibling group column proves the codebase
  // knows this input is untrusted -- it wraps the same parse in try/catch.
  // Worse, the parse happens BEFORE the isAdminUser check, so it also takes the
  // table down for viewers who cannot even see this column.
  it('survives a log row whose other field is not valid JSON', () => {
    expect(() =>
      cell('channel', { ...consumption, other: '{"admin_info"' }, { text: 3 }),
    ).not.toThrow();
  });

  it('survives a corrupt other field even for a non-admin viewer', () => {
    expect(() =>
      cell(
        'channel',
        { ...consumption, other: 'not-json' },
        { admin: false, text: 3 },
      ),
    ).not.toThrow();
  });

  // (No companion pin asserting "this throws today". Such a pin is the
  // literal negation of the two locks above, so guarding the parse would turn
  // it red and the repair would read as a regression. The locks carry the
  // record; the healthy shapes of this cell are covered by the tests above.)
});

describe('UsageLogsColumnDefs — username column', () => {
  const record = { type: 2, username: 'alice', user_id: 42 };

  it('shows an initial-avatar next to the full username for an admin', () => {
    const { text } = cell('username', record, { text: 'alice' });

    expect(screen.getByTestId('avatar')).toHaveTextContent('a');
    expect(text).toContain('alice');
  });

  it('opens the user drawer for the row owner when the avatar is clicked', () => {
    cell('username', record, { text: 'alice' });

    fireEvent.click(screen.getByTestId('avatar'));

    expect(showUserInfoFunc).toHaveBeenCalledWith(42);
  });

  it('stops the avatar click from also toggling the row expander', () => {
    // The table sets expandRowByClick; without stopPropagation every attempt to
    // inspect a user would also expand/collapse the row underneath.
    const rowClick = vi.fn();
    const definition = col('username');
    render(
      <div onClick={rowClick}>{definition.render('alice', record, 0)}</div>,
    );

    fireEvent.click(screen.getByTestId('avatar'));

    expect(showUserInfoFunc).toHaveBeenCalledTimes(1);
    expect(rowClick).not.toHaveBeenCalled();
  });

  it('renders nothing at all for a non-admin viewer', () => {
    const { text } = cell('username', record, {
      admin: false,
      text: 'alice',
    });

    expect(text).toBe('');
    expect(screen.queryByTestId('avatar')).toBeNull();
  });

  it('omits the initial when the username is not a string', () => {
    const { text } = cell(
      'username',
      { ...record, username: null },
      {
        text: null,
      },
    );

    expect(screen.getByTestId('avatar')).toHaveTextContent('');
    expect(text).not.toContain('false');
  });
});

describe('UsageLogsColumnDefs — token column', () => {
  it.each([0, 2, 5])('shows the token name on a type %i row', (type) => {
    const { text } = cell(
      'token',
      { type, token_name: 'prod-key' },
      { text: 'prod-key' },
    );

    expect(text).toContain('prod-key');
  });

  it.each([1, 3, 4])('hides the token name on a type %i row', (type) => {
    const { text } = cell(
      'token',
      { type, token_name: 'prod-key' },
      { text: 'prod-key' },
    );

    expect(text).toBe('');
  });

  it('copies the token name with the event as first argument', () => {
    // This page's copyText is copyText(event, text) -- it calls
    // event.stopPropagation() before copying. mj-logs/task-logs use the
    // one-argument form, so "unifying" them would break this page silently.
    cell('token', { type: 2, token_name: 'prod-key' }, { text: 'prod-key' });

    fireEvent.click(screen.getByTestId('tag'));

    expect(copyText).toHaveBeenCalledTimes(1);
    const [event, value] = copyText.mock.calls[0];
    expect(value).toBe('prod-key');
    expect(typeof event?.stopPropagation).toBe('function');
  });
});

describe('UsageLogsColumnDefs — group column', () => {
  it('prefers the group recorded on the row itself', () => {
    cell('group', { type: 2, group: 'vip', other: '{"group":"default"}' });

    expect(screen.getByTestId('group-tag')).toHaveAttribute(
      'data-group',
      'vip',
    );
  });

  it('falls back to the group embedded in the other blob', () => {
    cell('group', { type: 2, group: '', other: '{"group":"default"}' });

    expect(screen.getByTestId('group-tag')).toHaveAttribute(
      'data-group',
      'default',
    );
  });

  it('renders nothing when neither source carries a group', () => {
    const { text } = cell('group', { type: 2, group: '', other: '{}' });

    expect(text).toBe('');
  });

  it.each([1, 3, 4])('renders nothing on a type %i row', (type) => {
    const { text } = cell('group', { type, group: 'vip', other: '' });

    expect(text).toBe('');
  });

  it('survives a corrupt other blob instead of taking the table down', () => {
    // This is the column that gets it right; it is the control case for the
    // channel/retry locks above and below.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {});

    let out;
    expect(() => {
      out = cell('group', { type: 2, group: '', other: '{"group"' });
    }).not.toThrow();
    expect(out.text).toBe('');
    expect(spy).toHaveBeenCalled();

    spy.mockRestore();
  });

  it('renders nothing when the other blob is the literal null', () => {
    const { text } = cell('group', { type: 2, group: '', other: 'null' });

    expect(text).toBe('');
  });
});

describe('UsageLogsColumnDefs — type column', () => {
  it.each([
    [1, '充值', 'cyan'],
    [2, '消费', 'lime'],
    [3, '管理', 'orange'],
    [4, '系统', 'purple'],
    [5, '错误', 'red'],
  ])('labels type %i as %s', (type, label, color) => {
    const { text } = cell('type', { type }, { text: type });

    expect(text).toContain(label);
    expect(screen.getByTestId('tag')).toHaveAttribute('data-color', color);
  });

  it.each([0, 9, undefined, null, '2'])(
    'labels the unrecognised type %s as 未知',
    (type) => {
      const { text } = cell('type', { type }, { text: type });

      expect(text).toContain('未知');
      expect(screen.getByTestId('tag')).toHaveAttribute('data-color', 'grey');
    },
  );
});

describe('UsageLogsColumnDefs — model column', () => {
  const plain = { type: 2, model_name: 'gpt-4o', other: '' };

  it('renders a single copyable tag when no model mapping happened', () => {
    cell('model', plain, { text: 'gpt-4o' });

    const tags = screen.getAllByTestId('model-tag');
    expect(tags).toHaveLength(1);
    expect(tags[0]).toHaveAttribute('data-model', 'gpt-4o');
    expect(tags[0]).toHaveAttribute('data-suffix', 'no');
  });

  it('copies the billed model name when the tag is clicked', () => {
    cell('model', plain, { text: 'gpt-4o' });

    fireEvent.click(screen.getByTestId('model-tag'));

    expect(copyText).toHaveBeenCalledWith(expect.anything(), 'gpt-4o');
  });

  it('marks a remapped model with a route icon and both names in the popover', () => {
    const mapped = {
      type: 2,
      model_name: 'gpt-4o',
      other: JSON.stringify({
        is_model_mapped: true,
        upstream_model_name: 'gpt-4o-mini',
      }),
    };

    const { text } = cell('model', mapped, { text: 'gpt-4o' });

    // The visible trigger tag carries the suffix icon...
    const suffixed = screen
      .getAllByTestId('model-tag')
      .filter((n) => n.getAttribute('data-suffix') === 'yes');
    expect(suffixed).toHaveLength(1);
    // ...and the popover explains which model was actually charged.
    const popover = screen.getByTestId('popover-content');
    expect(popover).toHaveTextContent('请求并计费模型');
    expect(popover).toHaveTextContent('实际模型');
    expect(popover.textContent).toContain('gpt-4o-mini');
    expect(text).toContain('gpt-4o');
  });

  it('copies the upstream name from the popover, not the billed name', () => {
    const mapped = {
      type: 2,
      model_name: 'gpt-4o',
      other: JSON.stringify({
        is_model_mapped: true,
        upstream_model_name: 'gpt-4o-mini',
      }),
    };
    cell('model', mapped, { text: 'gpt-4o' });

    const upstreamTag = screen
      .getAllByTestId('model-tag')
      .find((n) => n.getAttribute('data-model') === 'gpt-4o-mini');
    fireEvent.click(upstreamTag);

    expect(copyText).toHaveBeenCalledWith(expect.anything(), 'gpt-4o-mini');
  });

  it('does NOT claim a mapping when the upstream name is blank', () => {
    const { text } = cell(
      'model',
      {
        type: 2,
        model_name: 'gpt-4o',
        other: JSON.stringify({
          is_model_mapped: true,
          upstream_model_name: '',
        }),
      },
      { text: 'gpt-4o' },
    );

    expect(screen.queryByTestId('popover')).toBeNull();
    expect(screen.getByTestId('model-tag')).toHaveAttribute(
      'data-suffix',
      'no',
    );
    expect(text).toContain('gpt-4o');
  });

  it('does NOT claim a mapping when is_model_mapped is false', () => {
    cell(
      'model',
      {
        type: 2,
        model_name: 'gpt-4o',
        other: JSON.stringify({
          is_model_mapped: false,
          upstream_model_name: 'gpt-4o-mini',
        }),
      },
      { text: 'gpt-4o' },
    );

    expect(screen.queryByTestId('popover')).toBeNull();
  });

  it.each([1, 3, 4])('renders nothing on a type %i row', (type) => {
    const { text } = cell('model', { ...plain, type }, { text: 'gpt-4o' });

    expect(text).toBe('');
  });
});

describe('UsageLogsColumnDefs — use time column', () => {
  const streamed = (frt) => ({
    type: 2,
    use_time: 12,
    is_stream: true,
    other: JSON.stringify({ frt }),
  });

  it.each([
    [12, 'green'],
    [100, 'green'],
    [101, 'orange'],
    [299, 'orange'],
    [300, 'red'],
    [900, 'red'],
  ])('colours a %is total duration %s', (useTime, color) => {
    cell(
      'use_time',
      { type: 2, use_time: useTime, is_stream: false, other: '' },
      { text: useTime },
    );

    const tags = screen.getAllByTestId('tag');
    expect(tags[0]).toHaveAttribute('data-color', color);
    expect(tags[0].textContent).toContain(String(useTime));
  });

  it.each([
    [1200, '1.2', 'green'],
    [2999, '3.0', 'orange'],
    [9000, '9.0', 'orange'],
    [10000, '10.0', 'red'],
  ])('renders a %sms first-token time as %ss (%s)', (frt, shown, color) => {
    const { text } = cell('use_time', streamed(frt), { text: 12 });

    const tags = screen.getAllByTestId('tag');
    expect(tags).toHaveLength(3);
    expect(tags[1]).toHaveAttribute('data-color', color);
    expect(text).toContain(shown);
  });

  it('shows the stream marker and omits first-token time for a non-stream row', () => {
    const { text } = cell(
      'use_time',
      { type: 2, use_time: 12, is_stream: false, other: '' },
      { text: 12 },
    );

    expect(screen.getAllByTestId('tag')).toHaveLength(2);
    expect(text).toContain('非流');
    expect(text).not.toContain('流 ');
  });

  it('shows the stream marker for a streamed row', () => {
    const { text } = cell('use_time', streamed(1200), { text: 12 });

    expect(text).toContain('流');
    expect(text).not.toContain('非流');
  });

  it.each([0, 1, 3, 4])('renders nothing on a type %i row', (type) => {
    const { text } = cell(
      'use_time',
      { type, use_time: 12, is_stream: false, other: '' },
      { text: 12 },
    );

    expect(text).toBe('');
  });

  // DEFECT: renderFirstUseTime does parseFloat(undefined)/1000 -> NaN and then
  // NaN.toFixed(1) -> "NaN", which fails both threshold comparisons and lands
  // in the red branch. A streamed request whose log has no `frt` (an older row,
  // or one whose other blob was written without it) therefore shows the
  // customer a red "NaN s" first-token time on the usage page.
  it('does not print NaN when a streamed row has no first-token time', () => {
    const { text } = cell(
      'use_time',
      { type: 2, use_time: 12, is_stream: true, other: '' },
      { text: 12 },
    );

    expect(text).not.toContain('NaN');
  });

  // (No companion pin on the "NaN s" text/red colour. It is the literal
  // negation of the lock above and would go red on the repair. The tag's
  // colour bands for real first-token times are already asserted by the
  // green/orange/red threshold tests above.)
});

describe('UsageLogsColumnDefs — token count columns', () => {
  it.each([0, 2, 5])(
    'shows the prompt token count on a type %i row',
    (type) => {
      const { text } = cell(
        'prompt',
        { type, prompt_tokens: 1234 },
        { text: 1234 },
      );

      expect(text).toContain('1234');
    },
  );

  it.each([1, 3, 4])(
    'hides the prompt token count on a type %i row',
    (type) => {
      const { text } = cell(
        'prompt',
        { type, prompt_tokens: 1234 },
        { text: 1234 },
      );

      expect(text).toBe('');
    },
  );

  it('shows a positive completion token count', () => {
    const { text } = cell(
      'completion',
      { type: 2, completion_tokens: 88 },
      { text: 88 },
    );

    expect(text).toContain('88');
  });

  it('never shows a misleading count for a zero-output row', () => {
    // Today the cell is left blank for 0 (the guard is parseInt(text) > 0).
    // Asserting "blank" would block a change to render a literal 0, so the
    // invariant asserted is the one that holds either way: the cell must not
    // display a non-zero figure.
    const { text } = cell(
      'completion',
      { type: 2, completion_tokens: 0 },
      { text: 0 },
    );

    expect(text).not.toMatch(/[1-9]/);
  });

  it('hides the completion count on a non-billable row type', () => {
    const { text } = cell(
      'completion',
      { type: 3, completion_tokens: 88 },
      { text: 88 },
    );

    expect(text).toBe('');
  });
});

describe('UsageLogsColumnDefs — cost column', () => {
  it.each([0, 2, 5])('renders the quota of a type %i row', (type) => {
    cell('cost', { type, quota: 12345 }, { text: 12345 });

    expect(screen.getByTestId('quota')).toHaveAttribute('data-quota', '12345');
  });

  it('asks for six decimal places, not the two-digit default', () => {
    // Per-request charges are fractions of a cent; rounding this column to two
    // digits would show a whole page of "$0.00" line items.
    cell('cost', { type: 2, quota: 12345 }, { text: 12345 });

    expect(screen.getByTestId('quota')).toHaveAttribute('data-digits', '6');
  });

  it.each([1, 3, 4])('renders no cost on a type %i row', (type) => {
    const { text } = cell('cost', { type, quota: 12345 }, { text: 12345 });

    expect(text).toBe('');
    expect(screen.queryByTestId('quota')).toBeNull();
  });
});

describe('UsageLogsColumnDefs — ip column', () => {
  it('explains in the header why the column can be empty', () => {
    render(<div>{col('ip').title}</div>);

    expect(screen.getByTestId('tooltip-content').textContent).toContain(
      'IP记录',
    );
    expect(screen.getByTestId('help-icon')).toBeInTheDocument();
  });

  it.each([2, 5])('shows the ip of a type %i row', (type) => {
    const { text } = cell(
      'ip',
      { type, ip: '203.0.113.9' },
      { text: '203.0.113.9' },
    );

    expect(text).toContain('203.0.113.9');
    expect(screen.getByTestId('tooltip-content')).toHaveTextContent(
      '203.0.113.9',
    );
  });

  it('copies the ip when the tag is clicked', () => {
    cell('ip', { type: 2, ip: '203.0.113.9' }, { text: '203.0.113.9' });

    fireEvent.click(screen.getByTestId('tag'));

    expect(copyText).toHaveBeenCalledWith(expect.anything(), '203.0.113.9');
  });

  it('renders nothing when the ip was not recorded', () => {
    const { text } = cell('ip', { type: 2, ip: '' }, { text: '' });

    expect(text).toBe('');
  });

  it.each([0, 1, 3, 4])('renders nothing on a type %i row', (type) => {
    const { text } = cell(
      'ip',
      { type, ip: '203.0.113.9' },
      { text: '203.0.113.9' },
    );

    expect(text).toBe('');
  });
});

describe('UsageLogsColumnDefs — retry column', () => {
  it('names the channel that served the request when there is no retry info', () => {
    const { text } = cell(
      'retry',
      { type: 2, channel: 7, other: '' },
      { text: 0 },
    );

    expect(text).toContain('渠道');
    expect(text).toContain('7');
  });

  it('renders the whole retry chain when the request was re-routed', () => {
    const { text } = cell(
      'retry',
      {
        type: 2,
        channel: 7,
        other: JSON.stringify({ admin_info: { use_channel: [7, 9, 11] } }),
      },
      { text: 2 },
    );

    expect(text).toContain('7->9->11');
  });

  it('falls back to the serving channel when use_channel is empty', () => {
    const { text } = cell(
      'retry',
      {
        type: 2,
        channel: 7,
        other: JSON.stringify({ admin_info: { use_channel: '' } }),
      },
      { text: 0 },
    );

    expect(text).toContain('7');
    expect(text).not.toContain('->');
  });

  it('falls back to the serving channel when use_channel is null', () => {
    const { text } = cell(
      'retry',
      {
        type: 2,
        channel: 7,
        other: JSON.stringify({ admin_info: { use_channel: null } }),
      },
      { text: 0 },
    );

    expect(text).toContain('7');
  });

  it('renders nothing when the other blob is the literal null', () => {
    const { text } = cell(
      'retry',
      { type: 2, channel: 7, other: 'null' },
      { text: 0 },
    );

    expect(text).toBe('');
  });

  it('renders nothing for a non-admin viewer', () => {
    const { text } = cell(
      'retry',
      { type: 2, channel: 7, other: '' },
      { admin: false, text: 0 },
    );

    expect(text).toBe('');
  });

  it.each([0, 1, 3, 4])('renders nothing on a type %i row', (type) => {
    const { text } = cell(
      'retry',
      { type, channel: 7, other: '' },
      { text: 0 },
    );

    expect(text).toBe('');
  });

  // DEFECT: this cell calls JSON.parse(record.other) with no try/catch, and its
  // only guard is `record.other !== ''`. A row where the backend omitted the
  // field entirely (other === undefined) hits JSON.parse(undefined), which
  // throws "undefined is not valid JSON" -- so does any malformed blob. Every
  // other column in this file tolerates a missing other via getLogOther's
  // undefined -> '{}' default, and the group column tolerates malformed JSON
  // via try/catch. Here a single such row blanks the entire admin log table.
  it('survives a row with no other field at all', () => {
    expect(() =>
      cell('retry', { type: 2, channel: 7 }, { text: 0 }),
    ).not.toThrow();
  });

  it('survives a row whose other field is not valid JSON', () => {
    expect(() =>
      cell('retry', { type: 2, channel: 7, other: '{oops' }, { text: 0 }),
    ).not.toThrow();
  });

  // (No companion pin asserting "this throws today": it is the literal
  // negation of the two locks above and would turn red the moment the parse
  // is guarded. The locks carry the record.)
});

describe('UsageLogsColumnDefs — details column', () => {
  const pricing = {
    model_ratio: 3,
    model_price: -1,
    group_ratio: 1,
    user_group_ratio: 2,
    cache_tokens: 100,
    cache_ratio: 0.5,
    cache_creation_tokens: 20,
    cache_creation_ratio: 1.25,
    cache_creation_tokens_5m: 5,
    cache_creation_tokens_1h: 7,
    is_system_prompt_overwritten: true,
  };

  it('shows a price breakdown for a consumption row', () => {
    const { text } = cell(
      'details',
      { type: 2, content: 'ignored', other: JSON.stringify(pricing) },
      { text: 'ignored' },
    );

    expect(text).toContain('PRICE');
    expect(text).toContain('ratio=3');
    expect(text).toContain('group=1');
    expect(text).toContain('userGroup=2');
    expect(text).toContain('sysPrompt=true');
    expect(screen.getByTestId('paragraph')).toHaveAttribute('data-rows', '3');
  });

  it('bills a claude row through the claude cache-creation shape', () => {
    const { text } = cell(
      'details',
      {
        type: 2,
        content: 'ignored',
        other: JSON.stringify({ ...pricing, claude: true }),
      },
      { text: 'ignored' },
    );

    expect(text).toContain('provider=claude');
    expect(text).toContain('cc=20');
    expect(text).toContain('cc5m=5');
    expect(text).toContain('cc1h=7');
    // The 5m/1h ratios inherit the generic cache-creation ratio when absent.
    expect(text).toContain('ccr5m=1.25');
    expect(text).toContain('ccr1h=1.25');
  });

  it('suppresses cache-creation figures for a non-claude row', () => {
    // Negative half of the pair: OpenAI has no cache-WRITE charge, so the same
    // payload must produce zeroed creation columns. Without this, "always uses
    // the claude shape" would look identical to correct behaviour.
    const { text } = cell(
      'details',
      { type: 2, content: 'ignored', other: JSON.stringify(pricing) },
      { text: 'ignored' },
    );

    expect(text).toContain('provider=openai');
    expect(text).toContain('cc=0');
    expect(text).toContain('cc5m=0');
    expect(text).toContain('cc1h=0');
    // Cache READ figures survive on both providers.
    expect(text).toContain('cacheTok=100');
    expect(text).toContain('cacheRatio=0.5');
  });

  it.each([1, 3, 4, 5])(
    'shows the raw content instead of a price breakdown on a type %i row',
    (type) => {
      const { text } = cell(
        'details',
        {
          type,
          content: 'insufficient quota',
          other: JSON.stringify(pricing),
        },
        { text: 'insufficient quota' },
      );

      expect(text).toContain('insufficient quota');
      expect(text).not.toContain('PRICE');
      expect(screen.getByTestId('paragraph')).toHaveAttribute('data-rows', '2');
    },
  );

  it('shows the raw content when the other blob is the literal null', () => {
    const { text } = cell(
      'details',
      { type: 2, content: 'plain message', other: 'null' },
      { text: 'plain message' },
    );

    expect(text).toContain('plain message');
    expect(text).not.toContain('PRICE');
  });

  it('offers a hover popover only on the raw-content variant', () => {
    // The breakdown is multi-line pre-wrapped text; re-showing it in a tooltip
    // would truncate the itemised lines, so only the short content variant
    // opts into showTooltip.
    cell(
      'details',
      { type: 3, content: 'admin note', other: '' },
      { text: 'admin note' },
    );
    expect(screen.getByTestId('paragraph')).toHaveAttribute(
      'data-tooltip',
      'yes',
    );

    cell(
      'details',
      { type: 2, content: 'x', other: JSON.stringify(pricing) },
      { text: 'x' },
    );
    const paragraphs = screen.getAllByTestId('paragraph');
    expect(paragraphs[paragraphs.length - 1]).toHaveAttribute(
      'data-tooltip',
      'no',
    );
  });
});
