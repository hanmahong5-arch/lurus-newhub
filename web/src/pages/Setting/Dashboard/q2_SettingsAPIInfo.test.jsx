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

// API-info panel: an in-memory list edited across several modals and written
// back as one JSON blob under console_setting.api_info. Two things matter and
// both are about the collection rather than any single field — the blob that
// goes out must be the WHOLE list (not the visible page), and a delete must
// remove exactly the row that was asked for. Fixtures below deliberately use
// non-sequential ids (7 before 2) so that "acts on the right row" cannot be
// confused with "acts on row 0".

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor, within } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  rowSelection: null,
  pagination: null,
}));

vi.mock('@douyinfe/semi-ui', () => {
  const cellKey = (col, idx) => String(col.key ?? col.dataIndex ?? idx);

  const Table = ({
    columns = [],
    dataSource = [],
    rowSelection,
    pagination,
    loading,
    empty,
  }) => {
    H.rowSelection = rowSelection;
    H.pagination = pagination;
    return React.createElement(
      'div',
      {
        'data-testid': 'table',
        'data-loading': String(!!loading),
        'data-total': String(pagination?.total ?? 0),
        'data-page': String(pagination?.currentPage ?? 0),
        'data-pagesize': String(pagination?.pageSize ?? 0),
        'data-rowcount': String(dataSource.length),
      },
      dataSource.length === 0 ? empty : null,
      dataSource.map((record, rowIdx) =>
        React.createElement(
          'div',
          {
            key: rowIdx,
            'data-testid': `row-${rowIdx}`,
            'data-row-id': String(record.id),
          },
          columns.map((col, i) =>
            React.createElement(
              'span',
              { key: i, 'data-testid': `cell-${rowIdx}-${cellKey(col, i)}` },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
        ),
      ),
    );
  };

  const Modal = ({
    visible,
    title,
    children,
    onOk,
    onCancel,
    confirmLoading,
  }) =>
    visible
      ? React.createElement(
          'div',
          {
            'data-testid': 'modal',
            'data-title': typeof title === 'string' ? title : '',
            'data-confirm-loading': String(!!confirmLoading),
          },
          children,
          React.createElement(
            'button',
            { type: 'button', 'data-testid': 'modal-ok', onClick: onOk },
            'ok',
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': 'modal-cancel',
              onClick: onCancel,
            },
            'cancel',
          ),
        )
      : null;

  const Form = ({ children, initValues }) =>
    React.createElement(
      'div',
      {
        'data-testid': 'modal-form',
        'data-init': JSON.stringify(initValues ?? null),
      },
      children,
    );
  Form.Section = ({ text, children }) =>
    React.createElement(
      'section',
      { 'data-testid': 'section' },
      text,
      children,
    );
  const makeField = (kind) => {
    const Field = ({ field, label, onChange, optionList, placeholder }) => {
      H.handlers.set(field, onChange);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-placeholder': typeof placeholder === 'string' ? placeholder : '',
        'data-options': optionList ? JSON.stringify(optionList) : '',
      });
    };
    return Field;
  };
  Form.Input = makeField('input');
  Form.Select = makeField('select');
  Form.TextArea = makeField('textarea');

  const Button = ({ children, onClick, disabled, loading, icon: _icon }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        disabled: !!disabled,
        'data-loading': String(!!loading),
      },
      children,
    );

  // Renders the real checked state and calls back with the flipped value, so
  // "turns the panel off" and "turns the panel on" are distinguishable.
  const Switch = ({ checked, onChange }) =>
    React.createElement('button', {
      type: 'button',
      'data-testid': 'panel-switch',
      'data-checked': String(!!checked),
      onClick: () => onChange?.(!checked),
    });

  const Space = ({ children }) => React.createElement('span', null, children);
  const Divider = () => React.createElement('hr', null);
  const Empty = ({ description }) =>
    React.createElement('div', { 'data-testid': 'empty' }, description);
  const Tag = ({ children, color }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color ?? '' },
      children,
    );
  const Avatar = ({ color }) =>
    React.createElement('span', {
      'data-testid': 'avatar',
      'data-color': color ?? '',
    });
  const Typography = {
    Text: ({ children }) => React.createElement('span', null, children),
  };

  return {
    Table,
    Modal,
    Form,
    Button,
    Switch,
    Space,
    Divider,
    Empty,
    Tag,
    Avatar,
    Typography,
  };
});

vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationNoResult: () => null,
  IllustrationNoResultDark: () => null,
}));

vi.mock('lucide-react', () => ({
  Plus: () => null,
  Edit: () => null,
  Trash2: () => null,
  Save: () => null,
  Settings: () => null,
}));

vi.mock('../../../helpers', () => ({
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

// Interpolating stub: the delete toast now passes its count as an option
// instead of building the key with a template literal, and the assertion below
// is on the count the operator actually sees.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, opts) =>
      String(key).replace(/{{(\w+)}}/g, (m, name) =>
        opts && name in opts ? String(opts[name]) : m,
      ),
  }),
}));

import SettingsAPIInfo from './SettingsAPIInfo';
import { API, showError, showSuccess } from '../../../helpers';

const KEY = 'console_setting.api_info';
const ENABLED_KEY = 'console_setting.api_info_enabled';

// Non-sequential ids on purpose: id 7 sits at index 0.
const TWO_ROWS = [
  {
    id: 7,
    url: 'https://a.example.com',
    route: '香港',
    description: '大带宽',
    color: 'blue',
  },
  {
    id: 2,
    url: 'https://b.example.com',
    route: '东京',
    description: '低延迟',
    color: 'red',
  },
];

const renderPanel = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingsAPIInfo options={options} refresh={refresh} />);
  return { refresh };
};

const withList = (list, extra = {}) =>
  renderPanel({ [KEY]: JSON.stringify(list), ...extra });

const rowIds = () =>
  screen.queryAllByTestId(/^row-\d+$/).map((el) => el.dataset.rowId);

const rowCount = () => Number(screen.getByTestId('table').dataset.rowcount);
const row = (i) => screen.getByTestId(`row-${i}`);
const click = (el) => act(() => el.click());
const clickAsync = async (el) =>
  act(async () => {
    el.click();
  });
const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });
const savedList = () => JSON.parse(API.put.mock.calls.at(-1)[1].value);
const saveButton = () => screen.getByText('保存设置');
// The batch button's label grows a "(n)" suffix once rows are selected, so
// match on the accessible name rather than an exact text node.
const batchButton = () => screen.getByRole('button', { name: /批量删除/ });

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.rowSelection = null;
  H.pagination = null;
  API.put.mockResolvedValue({ data: { success: true } });
  vi.spyOn(console, 'error').mockImplementation(() => {});
  vi.spyOn(console, 'log').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('parsing the stored blob', () => {
  it('renders one row per stored entry', () => {
    withList(TWO_ROWS);
    expect(rowIds()).toEqual(['7', '2']);
    expect(screen.getByTestId('table').dataset.total).toBe('2');
  });

  it('falls back to the legacy ApiInfo key', () => {
    renderPanel({ ApiInfo: JSON.stringify(TWO_ROWS) });
    expect(rowIds()).toEqual(['7', '2']);
  });

  it('prefers the namespaced key over the legacy one', () => {
    renderPanel({
      [KEY]: JSON.stringify([TWO_ROWS[1]]),
      ApiInfo: JSON.stringify(TWO_ROWS),
    });
    expect(rowIds()).toEqual(['2']);
  });

  it('shows an empty table for an empty stored value', () => {
    renderPanel({ [KEY]: '' });
    expect(rowCount()).toBe(0);
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无API信息');
  });

  it('shows an empty table when the blob is not JSON', () => {
    renderPanel({ [KEY]: '[not json' });
    expect(rowCount()).toBe(0);
    expect(console.error).toHaveBeenCalled();
  });

  it('shows an empty table when the blob is JSON but not a list', () => {
    renderPanel({ [KEY]: '{"url":"https://a.example.com"}' });
    expect(rowCount()).toBe(0);
  });

  it('renders the route, description and colour of each entry', () => {
    withList(TWO_ROWS);
    expect(within(row(0)).getByTestId('cell-0-url')).toHaveTextContent(
      'https://a.example.com',
    );
    expect(within(row(0)).getByTestId('cell-0-route')).toHaveTextContent(
      '香港',
    );
    expect(within(row(0)).getByTestId('cell-0-description')).toHaveTextContent(
      '大带宽',
    );
    expect(
      within(row(0))
        .getByTestId('cell-0-color')
        .querySelector('[data-testid="avatar"]').dataset.color,
    ).toBe('blue');
  });

  it('shows a dash for an entry with no description', () => {
    withList([{ id: 3, url: 'u', route: 'r', description: '', color: 'blue' }]);
    expect(within(row(0)).getByTestId('cell-0-description')).toHaveTextContent(
      '-',
    );
  });
});

describe('the enable switch', () => {
  it('defaults to enabled when the option was never stored', () => {
    withList(TWO_ROWS);
    expect(screen.getByTestId('panel-switch').dataset.checked).toBe('true');
    expect(screen.getByText('已启用')).toBeInTheDocument();
  });

  it.each([
    ['true', 'true'],
    [true, 'true'],
    ['false', 'false'],
    [false, 'false'],
    ['1', 'false'],
  ])('reads a stored value of %s as %s', (stored, expected) => {
    withList(TWO_ROWS, { [ENABLED_KEY]: stored });
    expect(screen.getByTestId('panel-switch').dataset.checked).toBe(expected);
  });

  it('persists a switch-off immediately and reports it', async () => {
    const { refresh } = withList(TWO_ROWS, { [ENABLED_KEY]: 'true' });
    await clickAsync(screen.getByTestId('panel-switch'));

    expect(API.put).toHaveBeenCalledWith('/api/option/', {
      key: ENABLED_KEY,
      value: 'false',
    });
    await waitFor(() =>
      expect(screen.getByTestId('panel-switch').dataset.checked).toBe('false'),
    );
    expect(showSuccess).toHaveBeenCalledWith('设置已保存');
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('persists a switch-on with the opposite value', async () => {
    withList(TWO_ROWS, { [ENABLED_KEY]: 'false' });
    await clickAsync(screen.getByTestId('panel-switch'));
    expect(API.put.mock.calls[0][1]).toEqual({
      key: ENABLED_KEY,
      value: 'true',
    });
  });

  it('leaves the switch where it was when the server refuses', async () => {
    const { refresh } = withList(TWO_ROWS, { [ENABLED_KEY]: 'true' });
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    await clickAsync(screen.getByTestId('panel-switch'));

    expect(showError).toHaveBeenCalledWith('没有权限');
    expect(screen.getByTestId('panel-switch').dataset.checked).toBe('true');
    expect(refresh).not.toHaveBeenCalled();
  });

  it('leaves the switch where it was when the request throws', async () => {
    withList(TWO_ROWS, { [ENABLED_KEY]: 'true' });
    API.put.mockRejectedValue(new Error('offline'));
    await clickAsync(screen.getByTestId('panel-switch'));

    expect(showError).toHaveBeenCalledWith('offline');
    expect(screen.getByTestId('panel-switch').dataset.checked).toBe('true');
  });
});

describe('adding an entry', () => {
  it('opens a blank editor', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加API'));
    expect(screen.getByTestId('modal').dataset.title).toBe('添加API');
    expect(JSON.parse(screen.getByTestId('modal-form').dataset.init)).toEqual({
      url: '',
      description: '',
      route: '',
      color: 'blue',
    });
  });

  it.each([
    ['url', { route: 'r', description: 'd' }],
    ['route', { url: 'u', description: 'd' }],
    ['description', { url: 'u', route: 'r' }],
  ])('refuses to save when %s is missing', (_missing, filled) => {
    withList(TWO_ROWS);
    click(screen.getByText('添加API'));
    Object.entries(filled).forEach(([field, value]) => emit(field, value));
    click(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的API信息');
    expect(rowCount()).toBe(2);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('appends the new entry with an id above every existing one', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加API'));
    emit('url', 'https://c.example.com');
    emit('route', '新加坡');
    emit('description', '备用');
    emit('color', 'green');
    click(screen.getByTestId('modal-ok'));

    // max(7, 2) + 1 — an implementation that used length + 1 would collide
    // with the existing id 2 here.
    expect(rowIds()).toEqual(['7', '2', '8']);
    expect(screen.queryByTestId('modal')).toBeNull();
    expect(showSuccess).toHaveBeenCalledWith(
      'API信息已添加，请及时点击“保存设置”进行保存',
    );
  });

  it('starts numbering at 1 for the first ever entry', () => {
    withList([]);
    click(screen.getByText('添加API'));
    emit('url', 'u');
    emit('route', 'r');
    emit('description', 'd');
    click(screen.getByTestId('modal-ok'));
    expect(rowIds()).toEqual(['1']);
  });

  it('discards the draft on cancel', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加API'));
    emit('url', 'u');
    click(screen.getByTestId('modal-cancel'));
    expect(rowCount()).toBe(2);
    expect(saveButton()).toBeDisabled();
  });
});

describe('editing an entry', () => {
  it('seeds the editor from the row that was clicked, not the first row', () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));
    expect(screen.getByTestId('modal').dataset.title).toBe('编辑API');
    expect(JSON.parse(screen.getByTestId('modal-form').dataset.init)).toEqual({
      url: 'https://b.example.com',
      description: '低延迟',
      route: '东京',
      color: 'red',
    });
  });

  it('rewrites only the edited row and keeps the order', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    emit('route', '香港二线');
    click(screen.getByTestId('modal-ok'));

    expect(rowIds()).toEqual(['7', '2']);
    expect(within(row(0)).getByTestId('cell-0-route')).toHaveTextContent(
      '香港二线',
    );
    expect(within(row(1)).getByTestId('cell-1-route')).toHaveTextContent(
      '东京',
    );
    expect(showSuccess).toHaveBeenCalledWith(
      'API信息已更新，请及时点击“保存设置”进行保存',
    );
  });

  it('keeps fields the operator did not touch', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    emit('route', '香港二线');
    click(screen.getByTestId('modal-ok'));
    expect(within(row(0)).getByTestId('cell-0-url')).toHaveTextContent(
      'https://a.example.com',
    );
  });
});

describe('deleting entries', () => {
  it('asks before deleting and does nothing on cancel', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    expect(screen.getByTestId('modal').dataset.title).toBe('确认删除');
    click(screen.getByTestId('modal-cancel'));
    expect(rowIds()).toEqual(['7', '2']);
  });

  // Deleting the FIRST row cannot tell "filters on id" apart from "filters on
  // index 0" — both leave id 2. The second row is the case that actually pins
  // the contract: an index-based filter would drop id 7 and keep the very row
  // the operator asked to remove.
  it.each([
    [0, ['2']],
    [1, ['7']],
  ])(
    'removes exactly the confirmed row, deleting row %i',
    (target, survivors) => {
      withList(TWO_ROWS);
      click(within(row(target)).getByText('删除'));
      click(screen.getByTestId('modal-ok'));
      expect(rowIds()).toEqual(survivors);
      expect(showSuccess).toHaveBeenCalledWith(
        'API信息已删除，请及时点击“保存设置”进行保存',
      );
    },
  );

  it('keeps the batch button inert until rows are selected', () => {
    // handleBatchDelete's own `selectedRowKeys.length === 0` guard is
    // unreachable from the UI because the button carries the same condition as
    // `disabled` — the disabled state is the assertable contract here.
    withList(TWO_ROWS);
    expect(batchButton()).toBeDisabled();
    act(() => H.rowSelection.onChange([7]));
    expect(batchButton()).not.toBeDisabled();
  });

  it('removes every selected row and only those', () => {
    withList([
      ...TWO_ROWS,
      {
        id: 5,
        url: 'https://c.example.com',
        route: '首尔',
        description: 'd',
        color: 'cyan',
      },
    ]);
    act(() => H.rowSelection.onChange([7, 5]));
    click(batchButton());

    expect(rowIds()).toEqual(['2']);
    expect(showSuccess).toHaveBeenCalledWith(
      '已删除 2 个API信息，请及时点击“保存设置”进行保存',
    );
  });

  it('clears the selection after a batch delete', () => {
    withList(TWO_ROWS);
    act(() => H.rowSelection.onChange([7]));
    click(batchButton());
    expect(H.rowSelection.selectedRowKeys).toEqual([]);
  });
});

describe('persisting the list', () => {
  it('keeps the save button inert until something changes', () => {
    withList(TWO_ROWS);
    expect(saveButton()).toBeDisabled();
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(saveButton()).not.toBeDisabled();
  });

  it('writes the whole list, not just the page on screen', async () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      id: i + 1,
      url: `https://n${i + 1}.example.com`,
      route: `r${i + 1}`,
      description: `d${i + 1}`,
      color: 'blue',
    }));
    withList(many);
    // The table paginates at 10, so only 10 rows are mounted...
    expect(rowCount()).toBe(10);
    expect(screen.getByTestId('table').dataset.total).toBe('12');

    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    // ...but all 11 survivors must be written back. Serialising the page
    // slice here would silently drop the last two API endpoints.
    expect(API.put.mock.calls[0][1].key).toBe(KEY);
    expect(savedList()).toHaveLength(11);
    expect(savedList().map((a) => a.id)).not.toContain(1);
    expect(savedList().map((a) => a.id)).toContain(12);
  });

  it('serialises the edited fields, not just the ids', async () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));
    emit('description', '低延迟直连');
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedList()).toEqual([
      TWO_ROWS[0],
      { ...TWO_ROWS[1], description: '低延迟直连' },
    ]);
  });

  it('reports a successful write and re-arms the button only on a new change', async () => {
    const { refresh } = withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith('API信息已更新'),
    );
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(saveButton()).toBeDisabled();
  });

  it('shows the page paginating rather than truncating the list', () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      id: i + 1,
      url: `https://n${i + 1}.example.com`,
      route: `r${i + 1}`,
      description: `d${i + 1}`,
      color: 'blue',
    }));
    withList(many);
    act(() => H.pagination.onChange(2, 10));
    expect(rowIds()).toEqual(['11', '12']);
    act(() => H.pagination.onShowSizeChange(2, 5));
    expect(rowIds()).toEqual(['1', '2', '3', '4', '5']);
  });

  it('keeps the changes pending when the write throws', async () => {
    const { refresh } = withList(TWO_ROWS);
    API.put.mockRejectedValue(new Error('offline'));
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('API信息更新失败'),
    );
    expect(refresh).not.toHaveBeenCalled();
    // The operator must still be able to retry.
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: a write the server refuses still clears the pending flag', () => {
  // updateOption resolves normally when the response says success:false — it
  // only calls showError. submitApiInfo therefore falls through to
  // setHasChanges(false), which disables 保存设置. The edits are still in local
  // state but there is no longer any way to send them: the operator sees an
  // error next to a greyed-out save button and has to redo the work. The
  // network-failure path above behaves correctly because it throws, so the two
  // failure modes disagree.

  it('does not report the write as successful when the server refuses it', async () => {
    const { refresh } = withList(TWO_ROWS);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    // The local delete legitimately toasts "已删除…请及时点击保存设置"; what must
    // not appear is the toast claiming the blob reached the server.
    expect(showSuccess).not.toHaveBeenCalledWith('API信息已更新');
    expect(refresh).not.toHaveBeenCalled();
  });

  // Verified red on 2026-08-20: un-skipped, the button was disabled
  // (`expected element to be enabled`).
  it('leaves the save button armed after a refused write', async () => {
    withList(TWO_ROWS);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: entries stored without an id are indistinguishable from each other', () => {
  // Every sibling panel (FAQ, Announcements, Uptime Kuma) normalises ids when
  // parsing — "确保每个项目都有id" — but this one does not. console_setting.api_info
  // has a legacy twin (options.ApiInfo) whose entries predate the id field, so
  // a blob of id-less entries is exactly what an upgraded install has.
  //
  // confirmDeleteApi filters on `api.id !== deletingApi.id`. With every id
  // undefined that comparison is false for every row, so confirming the delete
  // of ONE endpoint empties the entire panel; the next 保存设置 then writes the
  // empty list back and every documented API address disappears from the
  // console. Adding an entry to such a list is broken the same way:
  // Math.max(undefined, 0) is NaN, so the new id serialises as null.

  const ID_LESS = [
    {
      url: 'https://a.example.com',
      route: '香港',
      description: '大带宽',
      color: 'blue',
    },
    {
      url: 'https://b.example.com',
      route: '东京',
      description: '低延迟',
      color: 'red',
    },
  ];

  it('deletes exactly one row when the entries do carry ids', () => {
    // Control case: with ids present the same code path removes one row. This
    // is the behaviour the id-less case must be brought in line with.
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(rowCount()).toBe(1);
  });

  // Verified red on 2026-08-20: un-skipped, the whole list was wiped
  // (`expected 0 to be 1`).
  it('leaves the other entries alone when deleting an id-less entry', () => {
    withList(ID_LESS);
    expect(rowCount()).toBe(2);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(rowCount()).toBe(1);
  });

  // Verified red on 2026-08-20: un-skipped, the appended entry serialised with
  // `"id": null` (`expected null to be a number`).
  it('gives an entry appended to an id-less list a usable id', async () => {
    withList(ID_LESS);
    click(screen.getByText('添加API'));
    emit('url', 'https://c.example.com');
    emit('route', '新加坡');
    emit('description', '备用');
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(typeof savedList().at(-1).id).toBe('number');
    expect(Number.isFinite(savedList().at(-1).id)).toBe(true);
  });
});
