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

// The deployments table shell. It owns the route from "operator picked
// 销毁容器 in a row menu" to "deleteDeployment(id) is called", and the column
// visibility / compact-mode transforms applied on the way to CardTable.
//
// The DESTROY ROUTE is the reason this file is tested at the integration
// level: the real column definitions are kept (only spied on, pass-through)
// and a miniature CardTable actually renders the cells, so a broken hand-off
// between the menu item and the confirmation dialog shows up here rather than
// being papered over by a stub. The row that gets destroyed must be the row
// the operator confirmed — a fixture with three rows and distinct ids makes a
// hard-coded "first row" indistinguishable from correct code impossible.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

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

const H = vi.hoisted(() => ({ captured: { args: null } }));

vi.mock('@douyinfe/semi-ui', () => {
  const Tag = ({ children, color, prefixIcon, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color, ...rest },
      prefixIcon,
      children,
    );
  const Button = ({ children, onClick, disabled, icon, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, disabled: !!disabled, ...rest },
      icon,
      children,
    );
  const Dropdown = ({ render: menu, children }) =>
    React.createElement('div', { 'data-testid': 'dropdown' }, children, menu);
  Dropdown.Menu = ({ children }) => React.createElement('div', null, children);
  Dropdown.Item = ({ children, onClick }) =>
    React.createElement(
      'div',
      { 'data-testid': 'menu-item', role: 'menuitem', onClick },
      children,
    );
  Dropdown.Divider = () => React.createElement('hr', null);
  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  const Empty = ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description);
  return { Tag, Button, Dropdown, Empty, Typography: { Text } };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconMore: () => React.createElement('i', { 'data-testid': 'icon-more' }),
}));

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () => React.createElement('i', null),
  IllustrationNoResultDark: () => React.createElement('i', null),
}));

vi.mock('react-icons/fa', () => {
  const make = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
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

vi.mock('../../../helpers', async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, showSuccess: vi.fn(), showError: vi.fn() };
});

// A miniature but honest CardTable: it renders the columns it is given
// against the rows it is given, so the real cell renderers run. It also
// surfaces the props the table computes (selection, loading, scroll) as
// attributes rather than swallowing them.
vi.mock('../../common/ui/CardTable', () => ({
  default: ({ columns, dataSource, rowSelection, loading, scroll, empty }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'card-table',
        'data-column-keys': columns.map((c) => c.key).join(','),
        'data-column-widths': columns.map((c) => String(c.width)).join(','),
        'data-column-fixed': columns.map((c) => String(c.fixed)).join(','),
        'data-row-selection': rowSelection ? 'on' : 'off',
        'data-loading': String(!!loading),
        'data-scroll-x': String(scroll?.x),
      },
      (dataSource || []).length === 0
        ? empty
        : (dataSource || []).map((row) =>
            React.createElement(
              'div',
              { key: row.id, 'data-testid': `row-${row.id}` },
              columns.map((col) =>
                React.createElement(
                  'div',
                  { key: col.key, 'data-testid': `cell-${col.key}-${row.id}` },
                  col.render
                    ? col.render(row[col.dataIndex], row, 0)
                    : row[col.dataIndex],
                ),
              ),
            ),
          ),
    ),
}));

// Modal stubs surface the deciding props as attributes and expose the
// callbacks as buttons — never `() => null`, which would let the whole
// confirm/destroy branch run with nothing to assert on.
const { modalStub } = vi.hoisted(() => ({
  modalStub: (name) => ({
    default: ({
      visible,
      deployment,
      operation,
      onCancel,
      onConfirm,
      onSuccess,
    }) =>
      React.createElement(
        'div',
        {
          'data-testid': name,
          'data-visible': String(!!visible),
          'data-deployment': deployment ? String(deployment.id) : 'none',
          'data-operation': operation ?? 'none',
        },
        // A hidden modal offers nothing to click, exactly like the real one:
        // otherwise "confirm fires the destroy" would pass even if the dialog
        // never opened.
        visible &&
          onConfirm &&
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `${name}-confirm`,
              onClick: onConfirm,
            },
            'confirm',
          ),
        visible &&
          onCancel &&
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `${name}-cancel`,
              onClick: onCancel,
            },
            'cancel',
          ),
        visible &&
          onSuccess &&
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `${name}-success`,
              onClick: () => onSuccess({ id: 'from-modal' }),
            },
            'success',
          ),
      ),
  }),
}));

vi.mock('./modals/ViewLogsModal', () => modalStub('logs-modal'));
vi.mock('./modals/ExtendDurationModal', () => modalStub('extend-modal'));
vi.mock('./modals/ViewDetailsModal', () => modalStub('details-modal'));
vi.mock('./modals/UpdateConfigModal', () => modalStub('config-modal'));
vi.mock('./modals/ConfirmationDialog', () => modalStub('confirm-dialog'));

// Pass-through spy: the REAL column definitions run, but the handler bundle
// the table hands them is captured so the operations the row menu cannot
// reach directly (e.g. operation === 'destroy') can still be exercised.
vi.mock('./DeploymentsColumnDefs', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    getDeploymentsColumns: (args) => {
      H.captured.args = args;
      return actual.getDeploymentsColumns(args);
    },
  };
});

import DeploymentsTable from './DeploymentsTable';

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

const allVisible = {
  container_name: true,
  status: true,
  provider: true,
  time_remaining: true,
  hardware_info: true,
  created_at: true,
  actions: true,
};

// Three rows, all running, all with distinct ids: "destroys the row that was
// confirmed" and "always destroys the first row" cannot both pass.
const rows = [
  {
    id: 'dep-aaa',
    container_name: 'alpha',
    status: 'running',
    provider: 'ionet',
    time_remaining: '1小时',
    completed_percent: 10,
    hardware_name: 'A100',
    hardware_quantity: 1,
    created_at: 1704412089,
  },
  {
    id: 'dep-bbb',
    container_name: 'bravo',
    status: 'running',
    provider: 'ionet',
    time_remaining: '2小时',
    completed_percent: 20,
    hardware_name: 'A100',
    hardware_quantity: 2,
    created_at: 1704412089,
  },
  {
    id: 'dep-ccc',
    container_name: 'charlie',
    status: 'running',
    provider: 'ionet',
    time_remaining: '3小时',
    completed_percent: 30,
    hardware_name: 'A100',
    hardware_quantity: 3,
    created_at: 1704412089,
  },
];

const makeProps = (overrides = {}) => ({
  deployments: rows,
  loading: false,
  searching: false,
  activePage: 1,
  pageSize: 10,
  deploymentCount: 3,
  compactMode: false,
  visibleColumns: { ...allVisible },
  handlePageChange: vi.fn(),
  handlePageSizeChange: vi.fn(),
  handleRow: vi.fn(),
  t,
  COLUMN_KEYS,
  deleteDeployment: vi.fn(),
  syncDeploymentToChannel: vi.fn(),
  setEditingDeployment: vi.fn(),
  setShowEdit: vi.fn(),
  refresh: vi.fn(),
  ...overrides,
});

const renderTable = (overrides = {}) => {
  const props = makeProps(overrides);
  const view = render(<DeploymentsTable {...props} />);
  return { props, view };
};

const table = () => screen.getByTestId('card-table');
const confirmDialog = () => screen.getByTestId('confirm-dialog');

const destroyItemIn = (rowId) =>
  Array.from(
    screen
      .getByTestId(`cell-actions-${rowId}`)
      .querySelectorAll('[data-testid="menu-item"]'),
  ).find((n) => n.textContent.trim() === '销毁容器');

beforeEach(() => {
  vi.clearAllMocks();
  H.captured.args = null;
});

describe('column visibility', () => {
  it('renders exactly the columns the operator left switched on', () => {
    renderTable({
      visibleColumns: { ...allVisible, provider: false, created_at: false },
    });
    const keys = table().dataset.columnKeys.split(',');
    expect(keys).toEqual([
      'container_name',
      'status',
      'time_remaining',
      'hardware_info',
      'actions',
    ]);
    expect(keys).not.toContain('provider');
  });

  it('renders every column when nothing is hidden', () => {
    renderTable();
    expect(table().dataset.columnKeys.split(',')).toHaveLength(7);
  });

  it('hides everything the operator switched off, even the required ones', () => {
    // The table itself applies no floor; the hook is what keeps name/actions
    // on. Documenting that split matters: a future "required" check must be
    // added deliberately, not assumed to already exist here.
    renderTable({ visibleColumns: {} });
    expect(table().dataset.columnKeys).toBe('');
  });
});

describe('compact mode', () => {
  it('keeps full widths and the pinned actions column by default', () => {
    renderTable({ compactMode: false });
    expect(table().dataset.columnWidths).toBe('300,140,140,200,220,150,120');
    expect(table().dataset.columnFixed.split(',')).toContain('right');
    expect(table().dataset.scrollX).toBe('1200');
  });

  it('shrinks every column by a fifth and unpins the actions column', () => {
    renderTable({ compactMode: true });
    // 300*0.8=240 … 120*0.8=96: a proportional shrink, not a constant.
    expect(table().dataset.columnWidths).toBe('240,112,112,160,176,120,96');
    expect(table().dataset.columnFixed.split(',')).not.toContain('right');
    expect(table().dataset.scrollX).toBe('800');
  });

  // GAP (not locked): the Math.max(width * 0.8, 80) floor cannot be reached by
  // any column this file defines (the narrowest is 120 => 96). It is dead
  // defensive code today; asserting it would require a fixture the production
  // code cannot produce.
});

describe('row selection and loading', () => {
  // Row selection / batch delete were removed: the "批量删除" button posted
  // to POST /api/deployments/batch_delete, a route nothing in the tree
  // registers, and the page above had already hard-disabled the feature
  // (batchOperationsEnabled = false) so it never rendered anyway. The table
  // no longer accepts a toggle for it at all.
  it('never offers row selection', () => {
    renderTable();
    expect(table().dataset.rowSelection).toBe('off');
  });

  it.each([
    [{ loading: true, searching: false }, 'true'],
    [{ loading: false, searching: true }, 'true'],
    [{ loading: false, searching: false }, 'false'],
  ])('spinner state for %o is %s', (flags, expected) => {
    renderTable(flags);
    expect(table().dataset.loading).toBe(expected);
  });

  it('explains an empty result set instead of showing a blank table', () => {
    renderTable({ deployments: [], deploymentCount: 0 });
    expect(screen.getByTestId('empty')).toHaveTextContent('搜索无结果');
  });
});

describe('destroy route', () => {
  it('sends the chosen row to the typed confirmation, not straight to the API', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    expect(confirmDialog().dataset.visible).toBe('false');

    await user.click(destroyItemIn('dep-bbb'));

    expect(confirmDialog().dataset.visible).toBe('true');
    expect(confirmDialog().dataset.deployment).toBe('dep-bbb');
    expect(confirmDialog().dataset.operation).toBe('delete');
    // Nothing may be destroyed before the operator confirms.
    expect(props.deleteDeployment).not.toHaveBeenCalled();
    // …and the destroy must not be mistaken for a config edit.
    expect(screen.getByTestId('config-modal').dataset.visible).toBe('false');
  });

  it('destroys the confirmed row, once, by id', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    await user.click(destroyItemIn('dep-ccc'));
    await user.click(screen.getByTestId('confirm-dialog-confirm'));

    expect(props.deleteDeployment).toHaveBeenCalledTimes(1);
    expect(props.deleteDeployment).toHaveBeenCalledWith('dep-ccc');
  });

  it('closes the dialog and forgets the row after confirming', async () => {
    const user = userEvent.setup();
    renderTable();

    await user.click(destroyItemIn('dep-aaa'));
    await user.click(screen.getByTestId('confirm-dialog-confirm'));

    expect(confirmDialog().dataset.visible).toBe('false');
    // A stale selection here would aim the NEXT confirmation at the old row.
    expect(confirmDialog().dataset.deployment).toBe('none');
  });

  it('destroys nothing when the operator backs out', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    await user.click(destroyItemIn('dep-bbb'));
    await user.click(screen.getByTestId('confirm-dialog-cancel'));

    expect(confirmDialog().dataset.visible).toBe('false');
    expect(props.deleteDeployment).not.toHaveBeenCalled();
  });

  it('treats the destroy operation the same as delete', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    // Reached through the captured handler bundle: no row menu emits
    // 'destroy', but the table branches on it.
    H.captured.args.onUpdateConfig(rows[1], 'destroy');
    await Promise.resolve();

    await user.click(screen.getByTestId('confirm-dialog-confirm'));
    expect(props.deleteDeployment).toHaveBeenCalledWith('dep-bbb');
  });

  it('routes a plain config update to the editor and never to the destroy path', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    H.captured.args.onUpdateConfig(rows[0]);
    await Promise.resolve();

    expect(screen.getByTestId('config-modal').dataset.visible).toBe('true');
    expect(screen.getByTestId('config-modal').dataset.deployment).toBe(
      'dep-aaa',
    );
    expect(confirmDialog().dataset.visible).toBe('false');
    expect(props.deleteDeployment).not.toHaveBeenCalled();
  });

  // DEFECT (correctness): handleConfirmAction calls deleteDeployment(id)
  // without awaiting or catching it, and closes the dialog unconditionally.
  // useEnhancedDeploymentActions.deleteDeployment RETHROWS on a failed
  // request, so a refused destroy leaves an unhandled promise rejection and
  // the operator sees the dialog simply disappear — indistinguishable from a
  // success. NOT locked with a test: attaching a catch in the test to keep the
  // runner clean would itself mark the rejection handled, so the missing catch
  // cannot be observed from here without making the suite flaky. Recorded
  // instead of faked.
});

describe('row modals', () => {
  it('opens the log viewer for the row whose menu was used', async () => {
    const user = userEvent.setup();
    renderTable();

    const logs = Array.from(
      screen
        .getByTestId('cell-actions-dep-ccc')
        .querySelectorAll('[data-testid="menu-item"]'),
    ).find((n) => n.textContent.trim() === '查看日志');
    await user.click(logs);

    expect(screen.getByTestId('logs-modal').dataset.visible).toBe('true');
    expect(screen.getByTestId('logs-modal').dataset.deployment).toBe('dep-ccc');
    expect(screen.getByTestId('details-modal').dataset.visible).toBe('false');
  });

  it('opens the extend-duration dialog for the row whose menu was used', async () => {
    const user = userEvent.setup();
    renderTable();

    const extend = Array.from(
      screen
        .getByTestId('cell-actions-dep-aaa')
        .querySelectorAll('[data-testid="menu-item"]'),
    ).find((n) => n.textContent.trim() === '延长时长');
    await user.click(extend);

    expect(screen.getByTestId('extend-modal').dataset.visible).toBe('true');
    expect(screen.getByTestId('extend-modal').dataset.deployment).toBe(
      'dep-aaa',
    );
  });

  it('reloads the list after a modal reports success', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    const extend = Array.from(
      screen
        .getByTestId('cell-actions-dep-aaa')
        .querySelectorAll('[data-testid="menu-item"]'),
    ).find((n) => n.textContent.trim() === '延长时长');
    await user.click(extend);

    expect(props.refresh).not.toHaveBeenCalled();
    await user.click(screen.getByTestId('extend-modal-success'));
    expect(props.refresh).toHaveBeenCalledTimes(1);
  });

  it('survives a page that wired no refresh callback', async () => {
    const user = userEvent.setup();
    renderTable({ refresh: undefined });

    H.captured.args.onUpdateConfig(rows[0]);
    await Promise.resolve();

    // Optional-call guard: a throw here would blank the whole table.
    await user.click(screen.getByTestId('config-modal-success'));
    expect(screen.getByTestId('card-table')).toBeInTheDocument();
  });

  it('passes the channel sync straight through to the page handler', async () => {
    const user = userEvent.setup();
    const { props } = renderTable();

    const sync = Array.from(
      screen
        .getByTestId('cell-actions-dep-bbb')
        .querySelectorAll('[data-testid="menu-item"]'),
    ).find((n) => n.textContent.trim() === '同步到渠道');
    await user.click(sync);

    expect(props.syncDeploymentToChannel).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'dep-bbb' }),
    );
  });
});
