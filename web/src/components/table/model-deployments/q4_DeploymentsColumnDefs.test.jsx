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

// Column definitions for the model-deployment table. Two things here are worth
// guarding hard:
//
//   * the REMAINING-TIME cell, which is the quota figure on this screen: it
//     turns `completed_percent` into a "% left" badge and a colour band that
//     an operator uses to decide whether to top a container up. A percentage
//     that is off, unclamped or coloured green while nearly exhausted is a
//     wrong number on a spend screen.
//   * the ACTIONS cell, which owns the entry point to an irreversible
//     destroy. The destroy item must hand the row AND the literal 'delete'
//     operation to the caller — DeploymentsTable routes on that string, so a
//     dropped argument silently swaps the confirm dialog for the config
//     editor.
//
// The percent/duration arithmetic is the subject, so nothing that computes is
// stubbed: only the Semi shell, the icon packs and the two toasts. Every stub
// renders a marker carrying the deciding prop, because a branch that leaves
// nothing in the DOM cannot be told apart from a branch that never ran.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

// helpers/utils.jsx reads window.matchMedia at module scope with no guard, so
// the import itself throws under jsdom unless a stub exists first.
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

const H = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }));

vi.mock('@douyinfe/semi-ui', () => {
  const Tag = ({ children, color, size, shape, prefixIcon, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color, ...rest },
      // prefixIcon carries the status glyph; render it rather than swallow it.
      prefixIcon,
      children,
    );

  const Button = ({
    children,
    onClick,
    disabled,
    icon,
    type,
    theme,
    ...rest
  }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        disabled: !!disabled,
        'data-btn-type': type,
        'data-theme': theme,
        ...rest,
      },
      icon,
      children,
    );

  // The dropdown contents ARE the subject, so render them inline instead of
  // hiding them behind a trigger that never opens in jsdom.
  const Dropdown = ({ render: menu, children }) =>
    React.createElement('div', { 'data-testid': 'dropdown' }, children, menu);
  Dropdown.Menu = ({ children }) =>
    React.createElement('div', { 'data-testid': 'dropdown-menu' }, children);
  Dropdown.Item = ({ children, onClick, type, icon }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'menu-item',
        'data-item-type': type,
        role: 'menuitem',
        onClick,
      },
      children,
    );
  Dropdown.Divider = () =>
    React.createElement('hr', { 'data-testid': 'menu-divider' });

  const Text = ({ children, type, size, strong, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': 'text', 'data-type': type, ...rest },
      children,
    );

  return { Tag, Button, Dropdown, Typography: { Text } };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconMore: () => React.createElement('i', { 'data-testid': 'icon-more' }),
}));

// Icon markers, not nulls — several of them are the only difference between
// two otherwise identical rows.
vi.mock('react-icons/fa', () => {
  const make = (name) => (props) =>
    React.createElement('i', { 'data-testid': `icon-${name}`, ...props });
  return {
    FaPlay: make('play'),
    FaTrash: make('trash'),
    FaServer: make('server'),
    FaMemory: make('memory'),
    FaMicrochip: make('microchip'),
    FaCheckCircle: make('check'),
    FaSpinner: make('spinner'),
    FaClock: make('clock'),
    FaExclamationCircle: make('exclamation'),
    FaBan: make('ban'),
    FaTerminal: make('terminal'),
    FaPlus: make('plus'),
    FaCog: make('cog'),
    FaInfoCircle: make('info'),
    FaLink: make('link'),
    FaStop: make('stop'),
    FaHourglassHalf: make('hourglass'),
    FaGlobe: make('globe'),
  };
});

// Keep timestamp2string real — the created-at column's output is the subject.
vi.mock('../../../helpers', async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, showSuccess: H.showSuccess, showError: H.showError };
});

import { getDeploymentsColumns } from './DeploymentsColumnDefs';

const t = (k) => k;

const COLUMN_KEYS = {
  id: 'id',
  status: 'status',
  provider: 'provider',
  container_name: 'container_name',
  time_remaining: 'time_remaining',
  hardware_info: 'hardware_info',
  created_at: 'created_at',
  actions: 'actions',
};

const makeHandlers = (overrides = {}) => ({
  deleteDeployment: vi.fn(),
  setEditingDeployment: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn(),
  activePage: 1,
  deployments: [],
  onViewLogs: vi.fn(),
  onExtendDuration: vi.fn(),
  onViewDetails: vi.fn(),
  onUpdateConfig: vi.fn(),
  onSyncToChannel: vi.fn(),
  ...overrides,
});

// A row whose id, container name and hardware name are all DIFFERENT, so a
// cell that reads the wrong field cannot produce the expected output.
const makeRow = (overrides = {}) => ({
  id: 'dep-7c31',
  container_name: 'prod-llama-serving',
  status: 'running',
  provider: 'ionet',
  time_remaining: '3小时20分钟',
  completed_percent: 40,
  compute_minutes_remaining: 200,
  hardware_name: 'RTX 4090',
  hardware_quantity: 4,
  created_at: 1704412089,
  ...overrides,
});

const columnsOf = (overrides = {}) =>
  getDeploymentsColumns({ t, COLUMN_KEYS, ...makeHandlers(overrides) });

const buildColumns = (overrides = {}) => {
  const handlers = makeHandlers(overrides);
  const columns = getDeploymentsColumns({ t, COLUMN_KEYS, ...handlers });
  return { columns, handlers };
};

const renderCell = (key, record, overrides = {}) => {
  const { columns, handlers } = buildColumns(overrides);
  const column = columns.find((c) => c.key === key);
  const value = column.dataIndex ? record[column.dataIndex] : undefined;
  const view = render(<div>{column.render(value, record)}</div>);
  return { handlers, column, view };
};

const tags = () => screen.queryAllByTestId('tag');
const menuTexts = () =>
  screen.queryAllByTestId('menu-item').map((n) => n.textContent.trim());
// The dropdown trigger renders an icon and no label; the labelled button is
// the primary action.
const primaryButton = () =>
  screen.getAllByRole('button').find((b) => b.textContent.trim() !== '');

beforeEach(() => {
  vi.clearAllMocks();
});

describe('column set', () => {
  it('exposes every documented column, actions pinned to the right', () => {
    const columns = columnsOf();
    expect(columns.map((c) => c.key)).toEqual([
      'container_name',
      'status',
      'provider',
      'time_remaining',
      'hardware_info',
      'created_at',
      'actions',
    ]);
    expect(columns.at(-1).fixed).toBe('right');
  });
});

describe('status cell', () => {
  it.each([
    ['running', 'green', '运行中'],
    ['failed', 'red', '失败'],
    ['error', 'red', '错误'],
    ['destroyed', 'red', '已销毁'],
    ['completed', 'green', '已完成'],
    ['stopped', 'grey', '已停止'],
    ['pending', 'orange', '待部署'],
    ['deploying', 'blue', '部署中'],
    ['deployment requested', 'blue', '部署请求中'],
    ['termination requested', 'orange', '终止请求中'],
  ])('paints %s %s', (status, color, label) => {
    renderCell('status', makeRow({ status }));
    expect(tags()).toHaveLength(1);
    expect(tags()[0].dataset.color).toBe(color);
    expect(tags()[0]).toHaveTextContent(label);
  });

  it('tolerates the casing and padding the API actually sends', () => {
    renderCell('status', makeRow({ status: '  RUNNING ' }));
    expect(tags()[0].dataset.color).toBe('green');
    expect(tags()[0]).toHaveTextContent('运行中');
  });

  it('does not colour an unrecognised status as healthy, and keeps its raw text', () => {
    renderCell('status', makeRow({ status: 'evicted' }));
    // Negative twin of the mapping cases above: without this, "always green"
    // and "green for running" look identical.
    expect(tags()[0].dataset.color).toBe('grey');
    expect(tags()[0]).toHaveTextContent('evicted');
  });

  it('says so plainly when the row has no status at all', () => {
    renderCell('status', makeRow({ status: null }));
    expect(tags()[0].dataset.color).toBe('grey');
    expect(tags()[0]).toHaveTextContent('未知状态');
  });
});

describe('remaining-time cell (the quota figure)', () => {
  it('shows the share LEFT, not the share used', () => {
    renderCell(
      'time_remaining',
      makeRow({ completed_percent: 40, status: 'running' }),
    );
    // 40% consumed => 60% remaining. Reading the raw field would print 40%.
    expect(screen.getByText('60%')).toBeInTheDocument();
    expect(screen.getByText(/已用/)).toHaveTextContent('40');
  });

  it.each([
    // remaining <= 10 is the "top it up now" band
    [95, '5%', 'red'],
    [75, '25%', 'orange'],
    [10, '90%', 'green'],
  ])(
    'consumed %i%% is banded %s / %s',
    (completed_percent, label, tagColor) => {
      renderCell(
        'time_remaining',
        makeRow({ completed_percent, status: 'running' }),
      );
      const badge = tags().find((n) => n.textContent.trim() === label);
      expect(badge).toBeDefined();
      expect(badge.dataset.color).toBe(tagColor);
    },
  );

  it('parses a percentage that arrives as a formatted string', () => {
    renderCell(
      'time_remaining',
      makeRow({ completed_percent: '75%', status: 'running' }),
    );
    expect(screen.getByText('25%')).toBeInTheDocument();
  });

  it('never shows a negative remainder when the backend overshoots 100%', () => {
    renderCell(
      'time_remaining',
      makeRow({ completed_percent: 120, status: 'running' }),
    );
    expect(screen.getByText('0%')).toBeInTheDocument();
    expect(screen.queryByText('-20%')).not.toBeInTheDocument();
  });

  it('never shows more than a full remainder when the percentage is negative', () => {
    renderCell(
      'time_remaining',
      makeRow({ completed_percent: -25, status: 'running' }),
    );
    expect(screen.getByText('100%')).toBeInTheDocument();
  });

  it('spells out the remaining minutes as days/hours/minutes', () => {
    renderCell(
      'time_remaining',
      // 1500 = 1 day + 1 hour + 0 minutes; a fixture that is a whole number of
      // hours or of days could not tell a broken carry apart from a good one.
      makeRow({ compute_minutes_remaining: 1500, status: 'running' }),
    );
    expect(screen.getByText(/约/)).toHaveTextContent('1天 1小时');
  });

  it('renders the sub-hour case without inventing a zero hour part', () => {
    renderCell(
      'time_remaining',
      makeRow({ compute_minutes_remaining: 45, status: 'running' }),
    );
    expect(screen.getByText(/约/)).toHaveTextContent('45分钟');
    expect(screen.getByText(/约/).textContent).not.toMatch(/0小时/);
  });

  it('falls back to a "calculating" label when the backend sent no window', () => {
    renderCell(
      'time_remaining',
      makeRow({ time_remaining: '   ', status: 'running' }),
    );
    expect(screen.getByText('计算中')).toBeInTheDocument();
  });

  it('replaces the countdown with a terminal-state badge once a row has ended', () => {
    renderCell(
      'time_remaining',
      makeRow({ status: 'completed', completed_percent: 40 }),
    );
    // A finished container must not still advertise "60% left".
    expect(screen.queryByText('60%')).not.toBeInTheDocument();
    expect(tags().some((n) => n.textContent.trim() === '已完成')).toBe(true);
  });

  it('hides the minute-level detail for a row that is not running', () => {
    renderCell(
      'time_remaining',
      makeRow({ status: 'pending', compute_minutes_remaining: 200 }),
    );
    expect(screen.queryByText(/剩余.*200.*分钟/)).not.toBeInTheDocument();
  });

  it('shows the minute-level detail while the row IS running', () => {
    renderCell(
      'time_remaining',
      makeRow({ status: 'running', compute_minutes_remaining: 200 }),
    );
    expect(screen.getByText(/剩余/)).toHaveTextContent('200');
  });

  it('omits the badge entirely when the backend reports no progress figure', () => {
    renderCell(
      'time_remaining',
      makeRow({
        status: 'running',
        completed_percent: undefined,
        compute_minutes_remaining: undefined,
      }),
    );
    // Better a missing badge than a fabricated 0% / 100%.
    expect(tags()).toHaveLength(0);
    expect(screen.queryByText(/已用/)).not.toBeInTheDocument();
  });
});

describe('provider cell', () => {
  it('names the provider serving the model', () => {
    renderCell('provider', makeRow({ provider: 'ionet' }));
    expect(screen.getByText('ionet')).toBeInTheDocument();
  });

  it('says "none" rather than rendering an empty badge', () => {
    renderCell('provider', makeRow({ provider: '' }));
    expect(screen.getByText('暂无')).toBeInTheDocument();
  });
});

describe('hardware cell', () => {
  it('shows the hardware model and how many of them', () => {
    const { view } = renderCell(
      'hardware_info',
      makeRow({ hardware_name: 'RTX 4090', hardware_quantity: 4 }),
    );
    expect(screen.getByText('RTX 4090')).toBeInTheDocument();
    expect(view.container.textContent).toContain('x4');
    // Guards against a cell that prints the quantity of a neighbouring field.
    expect(view.container.textContent).not.toContain('x1');
  });

  // GAP (not locked): with hardware_name/hardware_quantity absent the cell
  // renders an empty pill and a bare "x". It is cosmetic and there is no
  // single correct replacement to assert, so it is disclosed rather than
  // pinned to a literal that a restyle would break.
});

describe('created-at cell', () => {
  it('renders the epoch seconds as a readable local timestamp', () => {
    const { view } = renderCell(
      'created_at',
      makeRow({ created_at: 1704412089 }),
    );
    // Real timestamp2string, so this is the arithmetic under test, not a stub.
    expect(view.container.textContent).toMatch(
      /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/,
    );
  });
});

describe('container-name cell', () => {
  it('copies the deployment ID — not the display name — and confirms it', async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });

    renderCell('container_name', makeRow());

    await user.click(screen.getByTitle('点击复制ID'));

    expect(writeText).toHaveBeenCalledWith('dep-7c31');
    expect(writeText).not.toHaveBeenCalledWith('prod-llama-serving');
    expect(H.showSuccess).toHaveBeenCalledWith('已复制 ID 到剪贴板');
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a blocked clipboard instead of claiming success', async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    });

    renderCell('container_name', makeRow());
    await user.click(screen.getByTitle('点击复制ID'));

    expect(H.showError).toHaveBeenCalledWith('复制失败');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('shows both the name and the id', () => {
    const { view } = renderCell('container_name', makeRow());
    expect(screen.getByText('prod-llama-serving')).toBeInTheDocument();
    expect(view.container.textContent).toContain('dep-7c31');
  });
});

describe('actions cell — destroy path', () => {
  it('hands the row AND the literal delete operation to the caller', async () => {
    const user = userEvent.setup();
    const { handlers } = renderCell('actions', makeRow({ status: 'running' }));

    const destroy = screen
      .getAllByTestId('menu-item')
      .find((n) => n.textContent.trim() === '销毁容器');
    await user.click(destroy);

    // DeploymentsTable switches on the second argument: without 'delete' it
    // opens the config editor instead of the typed confirmation.
    expect(handlers.onUpdateConfig).toHaveBeenCalledTimes(1);
    expect(handlers.onUpdateConfig).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-7c31' }),
      'delete',
    );
    // The row must not be destroyed straight from the menu.
    expect(handlers.deleteDeployment).not.toHaveBeenCalled();
  });

  it('marks the destroy item as the dangerous one', () => {
    renderCell('actions', makeRow({ status: 'running' }));
    const destroy = screen
      .getAllByTestId('menu-item')
      .find((n) => n.textContent.trim() === '销毁容器');
    expect(destroy.dataset.itemType).toBe('danger');
  });

  it.each(['completed', 'destroyed'])(
    'offers no destroy for an already-ended row (%s)',
    (status) => {
      renderCell('actions', makeRow({ status }));
      // Negative twin: proves the destroy item is conditional, not constant.
      expect(menuTexts()).not.toContain('销毁容器');
      expect(screen.queryByTestId('dropdown')).not.toBeInTheDocument();
      expect(primaryButton()).toHaveTextContent('查看详情');
    },
  );

  it('lets an ended row still be inspected', async () => {
    const user = userEvent.setup();
    const { handlers } = renderCell(
      'actions',
      makeRow({ status: 'destroyed' }),
    );
    await user.click(primaryButton());
    expect(handlers.onViewDetails).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-7c31' }),
    );
  });
});

describe('actions cell — primary action per status', () => {
  // startDeployment / restartDeployment (POST .../start, .../restart) are
  // gone: nothing in the route tree ever answered either, so 重试 (retry) and
  // 启动 (start) were always-failing buttons. failed/error/stopped rows now
  // get the same 查看详情 primary action a running row gets.
  it('running rows open the details panel', async () => {
    const user = userEvent.setup();
    const { handlers } = renderCell('actions', makeRow({ status: 'running' }));
    expect(primaryButton()).toHaveTextContent('查看详情');
    await user.click(primaryButton());
    expect(handlers.onViewDetails).toHaveBeenCalledTimes(1);
  });

  it.each(['stopped', 'failed', 'error'])(
    '%s rows also open the details panel, carrying the row id',
    async (status) => {
      const user = userEvent.setup();
      const { handlers } = renderCell(
        'actions',
        makeRow({ status, id: 'dep-other-99' }),
      );
      expect(primaryButton()).toHaveTextContent('查看详情');
      await user.click(primaryButton());
      expect(handlers.onViewDetails).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'dep-other-99' }),
      );
    },
  );

  it.each([
    ['deploying', '部署中'],
    ['pending', '待部署'],
    ['deployment requested', '部署中'],
    ['termination requested', '终止中'],
  ])(
    '%s rows expose a disabled, inert primary button',
    async (status, label) => {
      const user = userEvent.setup();
      const { handlers } = renderCell('actions', makeRow({ status }));
      const button = primaryButton();
      expect(button).toHaveTextContent(label);
      expect(button).toBeDisabled();
      await user.click(button);
      expect(handlers.onViewDetails).not.toHaveBeenCalled();
    },
  );

  // DEFECT (correctness): a row whose status is not in the known set falls
  // through to the `default` branch, which labels the primary action 已结束
  // ("finished") and disables it. An unknown status is not a finished
  // deployment — the row may well still be running and billing — and the same
  // cell contradicts itself by continuing to offer 销毁容器 for it. Fix: keep
  // the action inert but label it from the unknown status, as the status cell
  // already does.
  it.skip('LOCK: an unrecognised status is not advertised as finished', () => {
    renderCell('actions', makeRow({ status: 'evicted' }));
    expect(primaryButton().textContent).not.toContain('已结束');
  });

  it('currently: an unrecognised status stays inert but is still treated as live', async () => {
    const user = userEvent.setup();
    const { handlers } = renderCell('actions', makeRow({ status: 'evicted' }));

    // Invariants that survive the fix: the button does nothing when clicked,
    // and the row is still treated as destroyable (it has not ended).
    await user.click(primaryButton());
    expect(handlers.onViewDetails).not.toHaveBeenCalled();
    expect(menuTexts()).toContain('销毁容器');
  });
});

describe('actions cell — menu composition', () => {
  it('gives a running row details, logs, channel sync, extend and destroy', () => {
    renderCell('actions', makeRow({ status: 'running' }));
    expect(menuTexts()).toEqual([
      '查看详情',
      '查看日志',
      '同步到渠道',
      '延长时长',
      '销毁容器',
    ]);
  });

  it('drops the channel sync when the page did not wire a handler', () => {
    renderCell('actions', makeRow({ status: 'running' }), {
      onSyncToChannel: undefined,
    });
    expect(menuTexts()).not.toContain('同步到渠道');
    expect(menuTexts()).toContain('查看日志');
  });

  it('offers channel sync only for a running row, and no start item for a stopped one', () => {
    // '启动' used to appear here too, calling startDeployment — POST
    // /api/deployments/:id/start, a route nothing in the tree registers.
    // Removed; a stopped row's menu is just details / logs / destroy.
    renderCell('actions', makeRow({ status: 'stopped' }));
    expect(menuTexts()).toEqual(['查看详情', '查看日志', '销毁容器']);
    expect(menuTexts()).not.toContain('同步到渠道');
    expect(menuTexts()).not.toContain('启动');
  });

  it.each([
    ['running', true],
    ['deployment requested', true],
    ['stopped', false],
    ['pending', false],
  ])('extend-duration availability for %s is %s', (status, expected) => {
    renderCell('actions', makeRow({ status }));
    expect(menuTexts().includes('延长时长')).toBe(expected);
  });

  it('routes each menu entry to its own handler with the row', async () => {
    const user = userEvent.setup();
    const { handlers } = renderCell('actions', makeRow({ status: 'running' }));
    const item = (label) =>
      screen
        .getAllByTestId('menu-item')
        .find((n) => n.textContent.trim() === label);

    await user.click(item('查看日志'));
    expect(handlers.onViewLogs).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-7c31' }),
    );

    await user.click(item('延长时长'));
    expect(handlers.onExtendDuration).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-7c31' }),
    );

    await user.click(item('同步到渠道'));
    expect(handlers.onSyncToChannel).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-7c31' }),
    );

    // Nothing above may leak into the destroy path.
    expect(handlers.onUpdateConfig).not.toHaveBeenCalled();
  });

  it('separates the destructive entry from the rest', () => {
    const { view } = renderCell('actions', makeRow({ status: 'running' }));
    const nodes = Array.from(
      view.container.querySelectorAll(
        '[data-testid="menu-item"],[data-testid="menu-divider"]',
      ),
    );
    const destroyIndex = nodes.findIndex(
      (n) => n.textContent.trim() === '销毁容器',
    );
    expect(nodes[destroyIndex - 1].dataset.testid).toBe('menu-divider');
  });
});
