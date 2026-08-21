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

// Uptime Kuma monitor categories. Like its sibling panels this is an in-memory
// list edited through modals and flushed to the server as one JSON blob under
// console_setting.uptime_kuma_groups — so the two properties that matter are
// (a) the blob is the WHOLE list, never just the page on screen, and (b) an
// edit or a delete lands on the row that was asked for and no other. It is the
// only panel of the five with real input validation, so the reject/accept pairs
// below are written as pairs: a rule that always rejects is as broken as one
// that never does.
//
// Fixtures use non-sequential ids (7 sits at index 0) so "acted on the right
// row" cannot be mistaken for "acted on row 0".

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
    const Field = ({ field, label, onChange, placeholder, maxLength }) => {
      H.handlers.set(field, onChange);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-placeholder': typeof placeholder === 'string' ? placeholder : '',
        'data-maxlength': maxLength == null ? '' : String(maxLength),
      });
    };
    return Field;
  };
  Form.Input = makeField('input');
  Form.TextArea = makeField('textarea');
  Form.Select = makeField('select');

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
  // "turns the panel off" and "turns the panel on" stay distinguishable.
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
  Activity: () => null,
}));

vi.mock('../../../helpers', () => ({
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import SettingsUptimeKuma from './SettingsUptimeKuma';
import { API, showError, showSuccess } from '../../../helpers';

const GROUPS_KEY = 'console_setting.uptime_kuma_groups';
const ENABLED_KEY = 'console_setting.uptime_kuma_enabled';

const TWO_ROWS = [
  {
    id: 7,
    categoryName: 'OpenAI',
    url: 'https://status.a.example.com',
    slug: 'openai-status',
  },
  {
    id: 2,
    categoryName: 'Claude',
    url: 'https://status.b.example.com',
    slug: 'claude_status',
  },
];

// Six entries so a five-row page can leave one off screen.
const SIX_ROWS = [7, 2, 9, 4, 11, 3].map((id) => ({
  id,
  categoryName: `cat-${id}`,
  url: `https://s${id}.example.com`,
  slug: `slug-${id}`,
}));

const renderPanel = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingsUptimeKuma options={options} refresh={refresh} />);
  return { refresh };
};

const withList = (list, extra = {}) =>
  renderPanel({ [GROUPS_KEY]: JSON.stringify(list), ...extra });

const rowIds = () =>
  screen.queryAllByTestId(/^row-\d+$/).map((el) => el.dataset.rowId);
const rowCount = () => Number(screen.getByTestId('table').dataset.rowcount);
const total = () => Number(screen.getByTestId('table').dataset.total);
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
const lastPut = () => API.put.mock.calls.at(-1)[1];
const savedList = () => JSON.parse(lastPut().value);
const savedIds = () => savedList().map((g) => g.id);
const saveButton = () => screen.getByText('保存设置');
// The batch button grows a "(n)" suffix once rows are selected.
const batchButton = () => screen.getByRole('button', { name: /批量删除/ });
const panelSwitch = () => screen.getByTestId('panel-switch');

// Fills the add/edit modal. Each emit re-renders and re-registers the field
// handlers, so the three writes accumulate rather than clobbering each other.
const fillGroup = ({ categoryName, url, slug }) => {
  if (categoryName !== undefined) emit('categoryName', categoryName);
  if (url !== undefined) emit('url', url);
  if (slug !== undefined) emit('slug', slug);
};

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

describe('SettingsUptimeKuma hydration', () => {
  it('renders one row per stored category, keeping stored order and ids', () => {
    withList(TWO_ROWS);
    expect(rowIds()).toEqual(['7', '2']);
    expect(total()).toBe(2);
  });

  it('shows each stored field in its own column', () => {
    withList(TWO_ROWS);
    expect(screen.getByTestId('cell-0-categoryName')).toHaveTextContent(
      'OpenAI',
    );
    expect(screen.getByTestId('cell-0-url')).toHaveTextContent(
      'https://status.a.example.com',
    );
    expect(screen.getByTestId('cell-0-slug')).toHaveTextContent(
      'openai-status',
    );
    expect(screen.getByTestId('cell-1-categoryName')).toHaveTextContent(
      'Claude',
    );
  });

  it('shows the empty illustration when the stored blob is an empty list', () => {
    withList([]);
    expect(rowCount()).toBe(0);
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无监控数据');
  });

  it('falls back to an empty list when the stored blob is not valid JSON', () => {
    renderPanel({ [GROUPS_KEY]: '{not json' });
    expect(rowCount()).toBe(0);
    expect(console.error).toHaveBeenCalled();
  });

  it('falls back to an empty list when the stored blob is a JSON object', () => {
    // Array.isArray guard: an object must not be spread into rows.
    renderPanel({ [GROUPS_KEY]: JSON.stringify({ categoryName: 'OpenAI' }) });
    expect(rowCount()).toBe(0);
  });

  it('re-hydrates when the stored blob changes underneath it', () => {
    const { rerender } = render(
      <SettingsUptimeKuma
        options={{ [GROUPS_KEY]: JSON.stringify(TWO_ROWS) }}
        refresh={vi.fn()}
      />,
    );
    expect(rowIds()).toEqual(['7', '2']);
    rerender(
      <SettingsUptimeKuma
        options={{ [GROUPS_KEY]: JSON.stringify([TWO_ROWS[1]]) }}
        refresh={vi.fn()}
      />,
    );
    expect(rowIds()).toEqual(['2']);
  });

  it('synthesises an id for an entry stored without one', () => {
    // parseUptimeGroups exists to guarantee every row is addressable; without
    // an id, delete/edit would match every row at once.
    renderPanel({
      [GROUPS_KEY]: JSON.stringify([
        { categoryName: 'A', url: 'https://a.example.com', slug: 'a' },
        { categoryName: 'B', url: 'https://b.example.com', slug: 'b' },
      ]),
    });
    const ids = rowIds();
    expect(ids).toHaveLength(2);
    ids.forEach((id) => expect(id).not.toBe('undefined'));
    expect(new Set(ids).size).toBe(2);
  });
});

describe('SettingsUptimeKuma enable switch', () => {
  it('defaults to enabled when the option was never written', () => {
    renderPanel({});
    expect(panelSwitch().dataset.checked).toBe('true');
    expect(screen.getByText('已启用')).toBeInTheDocument();
  });

  it('reads a stored "false" as disabled', () => {
    renderPanel({ [ENABLED_KEY]: 'false' });
    expect(panelSwitch().dataset.checked).toBe('false');
    expect(screen.getByText('已禁用')).toBeInTheDocument();
  });

  it('reads a stored "true" as enabled', () => {
    renderPanel({ [ENABLED_KEY]: 'true' });
    expect(panelSwitch().dataset.checked).toBe('true');
  });

  it('writes the flipped value and adopts it once the server accepts', async () => {
    const { refresh } = renderPanel({ [ENABLED_KEY]: 'true' });
    await clickAsync(panelSwitch());

    expect(API.put).toHaveBeenCalledWith('/api/option/', {
      key: ENABLED_KEY,
      value: 'false',
    });
    await waitFor(() => expect(panelSwitch().dataset.checked).toBe('false'));
    expect(showSuccess).toHaveBeenCalledWith('设置已保存');
    expect(refresh).toHaveBeenCalled();
  });

  it('writes "true" when switching a disabled panel back on', async () => {
    // Negative half of the pair: the value sent must follow the switch, not be
    // a constant.
    renderPanel({ [ENABLED_KEY]: 'false' });
    await clickAsync(panelSwitch());
    expect(API.put).toHaveBeenCalledWith('/api/option/', {
      key: ENABLED_KEY,
      value: 'true',
    });
  });

  it('leaves the switch where it was when the server refuses', async () => {
    const { refresh } = renderPanel({ [ENABLED_KEY]: 'true' });
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    await clickAsync(panelSwitch());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    expect(panelSwitch().dataset.checked).toBe('true');
    expect(refresh).not.toHaveBeenCalled();
  });

  it('leaves the switch where it was when the request throws', async () => {
    renderPanel({ [ENABLED_KEY]: 'true' });
    API.put.mockRejectedValue(new Error('network down'));
    await clickAsync(panelSwitch());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('network down'));
    expect(panelSwitch().dataset.checked).toBe('true');
  });

  it('does not touch the categories blob when only the switch is flipped', async () => {
    withList(TWO_ROWS, { [ENABLED_KEY]: 'true' });
    await clickAsync(panelSwitch());
    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(ENABLED_KEY);
  });
});

describe('SettingsUptimeKuma add-category validation', () => {
  const openAdd = () => {
    click(screen.getByText('添加分类'));
    return screen.getByTestId('modal');
  };

  it('accepts a complete, well-formed category and appends it', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({
      categoryName: 'Gemini',
      url: 'https://status.c.example.com',
      slug: 'gemini_status-1',
    });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).not.toHaveBeenCalled();
    expect(rowCount()).toBe(3);
    expect(screen.getByTestId('cell-2-categoryName')).toHaveTextContent(
      'Gemini',
    );
    // Appended, not prepended: stored order is what the dashboard renders.
    expect(rowIds().slice(0, 2)).toEqual(['7', '2']);
    // max(7,2) + 1
    expect(rowIds()[2]).toBe('8');
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('rejects a category with no name and keeps the modal open', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({ url: 'https://status.c.example.com', slug: 'ok' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的分类信息');
    expect(rowCount()).toBe(2);
    // The typed input must survive the rejection.
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('rejects a category with no slug', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({ categoryName: 'Gemini', url: 'https://status.c.example.com' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的分类信息');
    expect(rowCount()).toBe(2);
  });

  it('rejects a URL that is not a URL at all', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({
      categoryName: 'Gemini',
      url: 'status.example.com',
      slug: 'ok',
    });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请输入有效的URL地址');
    expect(rowCount()).toBe(2);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('rejects a slug containing a space', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({
      categoryName: 'Gemini',
      url: 'https://status.c.example.com',
      slug: 'my status',
    });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith(
      'Slug只能包含字母、数字、下划线和连字符',
    );
    expect(rowCount()).toBe(2);
  });

  it('rejects a slug containing a path separator', async () => {
    // A slug is interpolated into the status-page URL downstream, so "../"
    // must not survive validation.
    withList(TWO_ROWS);
    openAdd();
    fillGroup({
      categoryName: 'Gemini',
      url: 'https://status.c.example.com',
      slug: '../admin',
    });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith(
      'Slug只能包含字母、数字、下划线和连字符',
    );
    expect(rowCount()).toBe(2);
  });

  it('accepts the full documented slug alphabet', () => {
    // Paired with the rejections above: the rule must not simply reject
    // everything. Letters, digits, underscore and hyphen are all legal.
    withList(TWO_ROWS);
    openAdd();
    fillGroup({
      categoryName: 'Mixed',
      url: 'https://status.c.example.com',
      slug: 'Ab9_-z',
    });
    click(screen.getByTestId('modal-ok'));

    expect(showError).not.toHaveBeenCalled();
  });

  it('does not write anything to the server on a rejected add', async () => {
    withList(TWO_ROWS);
    openAdd();
    fillGroup({ categoryName: 'Gemini', url: 'nope', slug: 'ok' });
    await clickAsync(screen.getByTestId('modal-ok'));
    expect(API.put).not.toHaveBeenCalled();
  });

  it('starts the add modal from a blank form, not the last edited row', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    click(screen.getByTestId('modal-cancel'));
    click(screen.getByText('添加分类'));

    const init = JSON.parse(screen.getByTestId('modal-form').dataset.init);
    expect(init).toEqual({ categoryName: '', url: '', slug: '' });
    expect(screen.getByTestId('modal').dataset.title).toBe('添加分类');
  });
});

describe('SettingsUptimeKuma edit', () => {
  it('seeds the modal from the row that was clicked', () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));

    const init = JSON.parse(screen.getByTestId('modal-form').dataset.init);
    expect(init).toEqual({
      categoryName: 'Claude',
      url: 'https://status.b.example.com',
      slug: 'claude_status',
    });
    expect(screen.getByTestId('modal').dataset.title).toBe('编辑分类');
  });

  it('updates only the edited row and leaves the other one byte-identical', async () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));
    fillGroup({ categoryName: 'Claude Sonnet' });
    await clickAsync(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([7, 2]);
    expect(savedList()[0]).toEqual(TWO_ROWS[0]);
    expect(savedList()[1]).toEqual({
      ...TWO_ROWS[1],
      categoryName: 'Claude Sonnet',
    });
  });

  it('keeps fields the edit modal never showed', async () => {
    // The blob may carry keys this build does not render; an edit must merge
    // rather than replace, or the extra keys are dropped on the next save.
    const WITH_EXTRA = [
      { ...TWO_ROWS[0], note: 'keep-me', order: 3 },
      TWO_ROWS[1],
    ];
    withList(WITH_EXTRA);
    click(within(row(0)).getByText('编辑'));
    fillGroup({ slug: 'openai-status-v2' });
    await clickAsync(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedList()[0].note).toBe('keep-me');
    expect(savedList()[0].order).toBe(3);
    expect(savedList()[0].slug).toBe('openai-status-v2');
  });

  it('applies the same validation on edit as on add', async () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    fillGroup({ url: 'not-a-url' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请输入有效的URL地址');
    expect(screen.getByTestId('cell-0-url')).toHaveTextContent(
      'https://status.a.example.com',
    );
  });

  it('discards the pending edit when the modal is cancelled', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    fillGroup({ categoryName: 'Renamed' });
    click(screen.getByTestId('modal-cancel'));

    expect(screen.getByTestId('cell-0-categoryName')).toHaveTextContent(
      'OpenAI',
    );
    expect(saveButton()).toBeDisabled();
  });
});

describe('SettingsUptimeKuma delete', () => {
  it('removes exactly the row whose delete button was clicked', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));

    expect(rowIds()).toEqual(['2']);
  });

  it('removes the second row when the second row is the one clicked', () => {
    // Pairing: proves the delete follows the clicked record rather than always
    // dropping index 0.
    withList(TWO_ROWS);
    click(within(row(1)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));

    expect(rowIds()).toEqual(['7']);
  });

  it('keeps every row when the confirmation is cancelled', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-cancel'));

    expect(rowIds()).toEqual(['7', '2']);
    expect(saveButton()).toBeDisabled();
  });

  it('does not send the deletion until 保存设置 is pressed', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));

    expect(API.put).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });

  it('deletes only the selected rows in a batch', () => {
    withList(SIX_ROWS);
    act(() => H.rowSelection.onChange([9, 3], []));
    click(batchButton());

    expect(rowIds()).toEqual(['7', '2', '4', '11']);
  });

  it('cannot start a batch delete with nothing selected', () => {
    // The empty-selection showError guard inside handleBatchDelete is not
    // reachable through the UI — the button is disabled first. The disabled
    // state is the behaviour worth holding: clicking it must change nothing.
    withList(SIX_ROWS);
    expect(batchButton()).toBeDisabled();
    click(batchButton());

    expect(rowIds()).toEqual(['7', '2', '9', '4', '11', '3']);
    expect(saveButton()).toBeDisabled();
  });

  it('arms the batch button only once rows are selected', () => {
    withList(SIX_ROWS);
    expect(batchButton()).toBeDisabled();
    act(() => H.rowSelection.onChange([9, 3], []));
    expect(batchButton()).not.toBeDisabled();
    expect(batchButton()).toHaveTextContent('(2)');
  });

  it('clears the selection after a batch delete so the next batch cannot reuse it', () => {
    // A stale selectedRowKeys would delete a second, unrelated row on the next
    // press — ids are reused as rows come and go.
    withList(SIX_ROWS);
    act(() => H.rowSelection.onChange([9], []));
    click(batchButton());
    expect(rowCount()).toBe(5);

    expect(batchButton()).toBeDisabled();
    click(batchButton());
    expect(rowCount()).toBe(5);
  });
});

describe('SettingsUptimeKuma save', () => {
  it('writes the whole list under the categories key', async () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(GROUPS_KEY);
    expect(savedList()).toEqual([TWO_ROWS[1]]);
  });

  it('includes entries that are not on the visible page', async () => {
    // The page shows five of six. A save that serialises getCurrentPageData()
    // instead of the full list would silently delete the sixth category.
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(1, 5));
    expect(rowCount()).toBe(5);
    expect(total()).toBe(6);

    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([2, 9, 4, 11, 3]);
  });

  it('preserves stored order across a save', async () => {
    // Order is what the dashboard renders the tabs in; a reorder on save is a
    // visible change nobody asked for.
    withList(SIX_ROWS);
    click(screen.getByText('添加分类'));
    fillGroup({
      categoryName: 'last',
      url: 'https://z.example.com',
      slug: 'zzz',
    });
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([7, 2, 9, 4, 11, 3, 12]);
  });

  it('reports success and refreshes the parent when the server accepts', async () => {
    const { refresh } = withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith('Uptime Kuma配置已更新'),
    );
    expect(refresh).toHaveBeenCalled();
    expect(saveButton()).toBeDisabled();
  });

  it('starts disabled because there is nothing pending', () => {
    withList(TWO_ROWS);
    expect(saveButton()).toBeDisabled();
  });

  it('keeps the save armed when the request throws', async () => {
    const { refresh } = withList(TWO_ROWS);
    API.put.mockRejectedValue(new Error('network down'));
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('Uptime Kuma配置更新失败'),
    );
    expect(showSuccess).not.toHaveBeenCalledWith('Uptime Kuma配置已更新');
    expect(refresh).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: a write the server refuses still clears the pending flag', () => {
  // updateOption resolves normally when the body says success:false — it only
  // calls showError. submitUptimeGroups then falls through to
  // setHasChanges(false), which greys out 保存设置. The edits are still in local
  // state but there is now no way to send them; the operator sees an error next
  // to a dead save button. The throw path above behaves correctly, so the two
  // failure modes disagree with each other.

  it('does not report a refused write as successful', async () => {
    const { refresh } = withList(TWO_ROWS);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    // The local delete legitimately toasts "分类已删除…"; what must not appear is
    // the toast claiming the blob reached the server.
    expect(showSuccess).not.toHaveBeenCalledWith('Uptime Kuma配置已更新');
    expect(refresh).not.toHaveBeenCalled();
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expect(element).not.toBeDisabled()".
  it.skip('leaves the save button armed after a refused write', async () => {
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

describe('DEFECT: a stored id of 0 is rewritten and can collide', () => {
  // parseUptimeGroups normalises with `id: item.id || index + 1`. Zero is a
  // legitimate id and is falsy, so the entry at index 1 below is renumbered to
  // 2 — which is already the id of the entry after it. Two rows then share an
  // id, and every operation here matches on id: confirmDeleteGroup filters
  // `item.id !== deletingGroup.id`, so deleting the third category also deletes
  // the second, and editing one edits both. The next 保存设置 makes the loss
  // permanent. `??` instead of `||` would preserve the 0.

  const ZERO_ID = [
    { id: 4, categoryName: 'A', url: 'https://a.example.com', slug: 'a' },
    { id: 0, categoryName: 'B', url: 'https://b.example.com', slug: 'b' },
    { id: 2, categoryName: 'C', url: 'https://c.example.com', slug: 'c' },
  ];

  it('keeps ids distinct when no stored id is falsy', () => {
    // Control: the same normalisation path on the same shape of data. This is
    // the behaviour the zero case has to be brought in line with.
    withList([
      { id: 4, categoryName: 'A', url: 'https://a.example.com', slug: 'a' },
      { id: 1, categoryName: 'B', url: 'https://b.example.com', slug: 'b' },
      { id: 2, categoryName: 'C', url: 'https://c.example.com', slug: 'c' },
    ]);
    expect(rowIds()).toEqual(['4', '1', '2']);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 2 to be 3" — rows B and C both ended up as id 2.
  it.skip('gives every row a distinct id even when one is stored as 0', () => {
    withList(ZERO_ID);
    expect(new Set(rowIds()).size).toBe(3);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected [ '4' ] to deeply equal [ '4', '0' ]" — deleting C took B too.
  it.skip('deletes only the category that was asked for', () => {
    withList(ZERO_ID);
    click(within(row(2)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(rowIds()).toEqual(['4', '0']);
  });
});

describe('DEFECT: an unloaded panel saves an empty list over the stored one', () => {
  // DashboardSetting seeds every console_setting.* key to '' and renders this
  // panel immediately, before GET /api/option/ has answered. parseUptimeGroups
  // treats '' as "no categories" and cannot tell it apart from "not loaded
  // yet", so the panel is fully interactive over a list it never read. Adding
  // one category and pressing 保存设置 in that window PUTs a one-element blob
  // over whatever the server actually holds — every other monitor category is
  // gone, and the success toast says it worked.
  //
  // No `it('currently …')` companion here on purpose: every assertion that
  // pins the current outcome (the PUT happening, the payload length, the
  // success toast) is exactly what a fix has to change.

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 'spy' to not be called with arguments" — the PUT went out.
  it.skip('does not write a collection it never loaded', async () => {
    renderPanel({ [GROUPS_KEY]: '' });
    click(screen.getByText('添加分类'));
    fillGroup({
      categoryName: 'Gemini',
      url: 'https://status.c.example.com',
      slug: 'gemini',
    });
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(API.put).not.toHaveBeenCalledWith(
      '/api/option/',
      expect.objectContaining({ key: GROUPS_KEY }),
    );
  });
});

describe('DEFECT: the URL rule accepts any scheme', () => {
  // handleSaveGroup validates with `new URL(...)` and nothing else. new URL
  // accepts javascript:, file: and data: just as happily as https:, so those
  // land in console_setting.uptime_kuma_groups and are handed to whatever
  // fetches the status page. The check should be an http/https allowlist.
  //
  // Again no companion pin: asserting that a javascript: URL is accepted today
  // would go red the moment somebody adds the allowlist.

  it('rejects a string that is not a URL in any scheme', () => {
    // Establishes that the rule does reject — without this the skips below
    // could be satisfied by a validator that refuses everything.
    withList(TWO_ROWS);
    click(screen.getByText('添加分类'));
    fillGroup({ categoryName: 'X', url: '://///', slug: 'x' });
    click(screen.getByTestId('modal-ok'));
    expect(showError).toHaveBeenCalledWith('请输入有效的URL地址');
    expect(rowCount()).toBe(2);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 'spy' to be called with arguments" — the entry was accepted.
  it('rejects a javascript: URL', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加分类'));
    fillGroup({
      categoryName: 'X',
      url: 'javascript:alert(document.cookie)',
      slug: 'x',
    });
    click(screen.getByTestId('modal-ok'));
    expect(showError).toHaveBeenCalledWith('请输入有效的URL地址');
    expect(rowCount()).toBe(2);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 'spy' to be called with arguments" — the entry was accepted.
  it('rejects a file: URL', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加分类'));
    fillGroup({ categoryName: 'X', url: 'file:///etc/passwd', slug: 'x' });
    click(screen.getByTestId('modal-ok'));
    expect(showError).toHaveBeenCalledWith('请输入有效的URL地址');
    expect(rowCount()).toBe(2);
  });
});

describe('SettingsUptimeKuma pagination', () => {
  it('reports the full list length as the total, not the page length', () => {
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(1, 5));
    expect(rowCount()).toBe(5);
    expect(total()).toBe(6);
  });

  it('shows the remainder of the list on the second page', () => {
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(2, 5));
    expect(rowIds()).toEqual(['3']);
  });

  it('returns to page one when the page size is changed from the size picker', () => {
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(2, 5));
    expect(rowIds()).toEqual(['3']);
    act(() => H.pagination.onShowSizeChange(2, 2));
    expect(rowIds()).toEqual(['7', '2']);
  });

  // Not asserted: the row-selection callbacks onSelect/onSelectAll only
  // console.log, and getCheckboxProps returns a hard-coded {disabled:false}.
  // There is no observable behaviour behind them to check, so pinning their
  // console output would only lock in log noise.
});
