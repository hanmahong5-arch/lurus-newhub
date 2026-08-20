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

// System announcements. This is the only panel of the five whose table order is
// not its storage order — getCurrentPageData re-sorts by publishDate, newest
// first, while 保存设置 serialises announcementsList in stored order. So screen
// row 0 is NOT list index 0, and every fixture below is arranged so that the
// two disagree: if an edit or a delete were keyed off the row position instead
// of the record, it would hit the wrong announcement and these tests would say
// so.
//
// Ids are non-sequential (7, 2, 9) for the same reason.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  render,
  screen,
  act,
  waitFor,
  within,
  fireEvent,
} from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  rowSelection: null,
  pagination: null,
  formApiCalls: [],
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

  // Three modals can be mounted at once here, so each is keyed by its title
  // rather than a shared testid.
  const Modal = ({
    visible,
    title,
    children,
    onOk,
    onCancel,
    confirmLoading,
  }) => {
    const name = typeof title === 'string' ? title : 'untitled';
    return visible
      ? React.createElement(
          'div',
          {
            'data-testid': `modal:${name}`,
            'data-confirm-loading': String(!!confirmLoading),
          },
          children,
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `modal-ok:${name}`,
              onClick: onOk,
            },
            'ok',
          ),
          React.createElement(
            'button',
            {
              type: 'button',
              'data-testid': `modal-cancel:${name}`,
              onClick: onCancel,
            },
            'cancel',
          ),
        )
      : null;
  };

  // Hands back a real form api so the "放大编辑" sync path has something to
  // call and we can see what it pushed.
  const Form = ({ children, initValues, getFormApi }) => {
    const apiRef = React.useRef(null);
    if (!apiRef.current) {
      apiRef.current = {
        setValue: (field, value) => H.formApiCalls.push([field, value]),
        setValues: (values) => H.formApiCalls.push(['*', values]),
      };
      getFormApi?.(apiRef.current);
    }
    return React.createElement(
      'div',
      {
        'data-testid': 'modal-form',
        'data-init': JSON.stringify(initValues ?? null),
      },
      children,
    );
  };
  Form.Section = ({ text, children }) =>
    React.createElement(
      'section',
      { 'data-testid': 'section' },
      text,
      children,
    );

  const makeField = (kind) => {
    const Field = ({ field, label, onChange, placeholder, optionList }) => {
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
  Form.TextArea = makeField('textarea');
  Form.Select = makeField('select');
  Form.DatePicker = makeField('datepicker');

  // Standalone TextArea used by the enlarge-editor modal. Rendered as a real
  // textarea so its value round-trips and fireEvent.change drives it.
  const TextArea = ({ value, onChange, placeholder }) =>
    React.createElement('textarea', {
      'data-testid': 'big-textarea',
      value: value ?? '',
      placeholder: placeholder ?? '',
      onChange: (e) => onChange?.(e.target.value, e),
    });

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

  const Switch = ({ checked, onChange }) =>
    React.createElement('button', {
      type: 'button',
      'data-testid': 'panel-switch',
      'data-checked': String(!!checked),
      onClick: () => onChange?.(!checked),
    });

  const Tooltip = ({ content, children }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tooltip',
        'data-content': typeof content === 'string' ? content : '',
      },
      children,
    );

  const Tag = ({ children, color }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color ?? '' },
      children,
    );

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
    TextArea,
    Button,
    Switch,
    Tooltip,
    Tag,
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
  Bell: () => null,
  Maximize2: () => null,
}));

// Tagged returns so the column can be asserted on what it PASSED, not merely
// that it rendered something.
vi.mock('../../../helpers', () => ({
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getRelativeTime: vi.fn((v) => `rel:${String(v)}`),
  formatDateTimeString: vi.fn((d) =>
    d instanceof Date && !Number.isNaN(d.getTime())
      ? `fmt:${d.toISOString()}`
      : `fmt:invalid`,
  ),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import SettingsAnnouncements from './SettingsAnnouncements';
import { API, showError, showSuccess, getRelativeTime } from '../../../helpers';

const ANN_KEY = 'console_setting.announcements';
const ENABLED_KEY = 'console_setting.announcements_enabled';

// Stored oldest-first on purpose; the table must show 2, 9, 7.
const THREE_DATED = [
  {
    id: 7,
    content: 'oldest notice',
    publishDate: '2024-01-01T00:00:00.000Z',
    type: 'default',
    extra: '',
  },
  {
    id: 2,
    content: 'newest notice',
    publishDate: '2026-06-01T00:00:00.000Z',
    type: 'warning',
    extra: 'note-2',
  },
  {
    id: 9,
    content: 'middle notice',
    publishDate: '2025-03-01T00:00:00.000Z',
    type: 'success',
    extra: '',
  },
];

const renderPanel = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingsAnnouncements options={options} refresh={refresh} />);
  return { refresh };
};

const withList = (list, extra = {}) =>
  renderPanel({ [ANN_KEY]: JSON.stringify(list), ...extra });

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
const modalOk = (name) => screen.getByTestId(`modal-ok:${name}`);
const modalCancel = (name) => screen.getByTestId(`modal-cancel:${name}`);
const modalOpen = (name) => screen.queryByTestId(`modal:${name}`) !== null;
const lastPut = () => API.put.mock.calls.at(-1)[1];
const savedList = () => JSON.parse(lastPut().value);
const savedIds = () => savedList().map((a) => a.id);
const saveButton = () => screen.getByText('保存设置');
const batchButton = () => screen.getByRole('button', { name: /批量删除/ });
const panelSwitch = () => screen.getByTestId('panel-switch');

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.rowSelection = null;
  H.pagination = null;
  H.formApiCalls = [];
  API.put.mockResolvedValue({ data: { success: true } });
  getRelativeTime.mockImplementation((v) => `rel:${String(v)}`);
  vi.spyOn(console, 'error').mockImplementation(() => {});
  vi.spyOn(console, 'log').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('SettingsAnnouncements hydration', () => {
  it('shows the newest announcement first even though it is stored second', () => {
    withList(THREE_DATED);
    expect(rowIds()).toEqual(['2', '9', '7']);
    expect(screen.getByTestId('cell-0-content')).toHaveTextContent(
      'newest notice',
    );
    expect(screen.getByTestId('cell-2-content')).toHaveTextContent(
      'oldest notice',
    );
    expect(total()).toBe(3);
  });

  it('does not reorder the stored list itself', async () => {
    // The sort is for display. If it mutated announcementsList, the next save
    // would silently rewrite the operator's stored order.
    withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([7, 9]);
  });

  it('hands the column the stored publishDate, and formats a real Date', () => {
    withList(THREE_DATED);
    const cell = screen.getByTestId('cell-0-publishDate');
    expect(cell).toHaveTextContent('rel:2026-06-01T00:00:00.000Z');
    expect(cell).toHaveTextContent('fmt:2026-06-01T00:00:00.000Z');
  });

  it('falls back to a dash instead of formatting a missing publishDate', () => {
    withList([{ id: 7, content: 'no date', type: 'default', extra: '' }]);
    const cell = screen.getByTestId('cell-0-publishDate');
    expect(cell.textContent).toContain('-');
    expect(cell.textContent).not.toContain('fmt:');
  });

  it('still lists an announcement that has no publishDate', () => {
    // The comparator yields NaN for a missing date; whatever position it ends
    // up in, the row must not disappear from the table.
    withList([
      ...THREE_DATED,
      { id: 11, content: 'undated', type: 'default', extra: '' },
    ]);
    expect(rowCount()).toBe(4);
    expect(rowIds()).toContain('11');
  });

  it('shows the empty illustration when the stored blob is an empty list', () => {
    withList([]);
    expect(rowCount()).toBe(0);
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无系统公告');
  });

  it('falls back to an empty list when the stored blob is not valid JSON', () => {
    renderPanel({ [ANN_KEY]: '<<<' });
    expect(rowCount()).toBe(0);
    expect(console.error).toHaveBeenCalled();
  });

  it('falls back to an empty list when the stored blob is a JSON object', () => {
    renderPanel({ [ANN_KEY]: JSON.stringify({ content: 'x' }) });
    expect(rowCount()).toBe(0);
  });

  it('reads the legacy Announcements key when the new one is absent', () => {
    // Upgrade path: `options[ANN_KEY] ?? options.Announcements`.
    renderPanel({ Announcements: JSON.stringify(THREE_DATED) });
    expect(rowIds()).toEqual(['2', '9', '7']);
  });

  it('prefers the new key over the legacy one when both are present', () => {
    renderPanel({
      [ANN_KEY]: JSON.stringify([THREE_DATED[0]]),
      Announcements: JSON.stringify(THREE_DATED),
    });
    expect(rowIds()).toEqual(['7']);
  });

  it('synthesises distinct ids for entries stored without one', () => {
    renderPanel({
      [ANN_KEY]: JSON.stringify([
        { content: 'a', publishDate: '2024-01-01T00:00:00.000Z' },
        { content: 'b', publishDate: '2025-01-01T00:00:00.000Z' },
      ]),
    });
    const ids = rowIds();
    expect(ids).toHaveLength(2);
    ids.forEach((id) => expect(id).not.toBe('undefined'));
    expect(new Set(ids).size).toBe(2);
  });
});

describe('SettingsAnnouncements type column', () => {
  it('colours each known type distinctly and labels it', () => {
    withList(THREE_DATED);
    const warn = within(screen.getByTestId('cell-0-type')).getByTestId('tag');
    expect(warn.dataset.color).toBe('orange');
    expect(warn).toHaveTextContent('警告');

    const ok = within(screen.getByTestId('cell-1-type')).getByTestId('tag');
    expect(ok.dataset.color).toBe('green');
    expect(ok).toHaveTextContent('成功');

    const def = within(screen.getByTestId('cell-2-type')).getByTestId('tag');
    expect(def.dataset.color).toBe('grey');
    expect(def).toHaveTextContent('默认');
  });

  it('falls back to grey and the raw value for an unknown type', () => {
    // Negative half of the pair: without this, "always grey" would pass the
    // 默认 case above.
    withList([
      {
        id: 7,
        content: 'x',
        publishDate: '2024-01-01T00:00:00.000Z',
        type: 'chartreuse',
        extra: '',
      },
    ]);
    const tag = within(screen.getByTestId('cell-0-type')).getByTestId('tag');
    expect(tag.dataset.color).toBe('grey');
    expect(tag).toHaveTextContent('chartreuse');
  });

  it('shows a dash for a missing 说明 and the text when present', () => {
    withList(THREE_DATED);
    expect(screen.getByTestId('cell-0-extra')).toHaveTextContent('note-2');
    expect(screen.getByTestId('cell-1-extra')).toHaveTextContent('-');
  });
});

describe('SettingsAnnouncements enable switch', () => {
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

  it('does not touch the announcements blob when only the switch is flipped', async () => {
    withList(THREE_DATED, { [ENABLED_KEY]: 'true' });
    await clickAsync(panelSwitch());
    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(ENABLED_KEY);
  });
});

describe('SettingsAnnouncements add', () => {
  const openAdd = () => click(screen.getByText('添加公告'));

  it('appends a complete announcement with the next free id', async () => {
    withList(THREE_DATED);
    openAdd();
    emit('content', '维护通知');
    emit('type', 'ongoing');
    await clickAsync(modalOk('添加公告'));
    await clickAsync(saveButton());

    // max(7,2,9) + 1
    expect(savedIds()).toEqual([7, 2, 9, 10]);
    expect(savedList().at(-1).content).toBe('维护通知');
    expect(savedList().at(-1).type).toBe('ongoing');
    expect(showError).not.toHaveBeenCalled();
  });

  it('defaults a new announcement to the 默认 type', async () => {
    withList(THREE_DATED);
    openAdd();
    emit('content', '维护通知');
    await clickAsync(modalOk('添加公告'));
    await clickAsync(saveButton());

    expect(savedList().at(-1).type).toBe('default');
  });

  it('stores the publish date as a parseable ISO string', async () => {
    withList(THREE_DATED);
    openAdd();
    emit('content', '维护通知');
    await clickAsync(modalOk('添加公告'));
    await clickAsync(saveButton());

    const iso = savedList().at(-1).publishDate;
    expect(typeof iso).toBe('string');
    expect(Number.isNaN(Date.parse(iso))).toBe(false);
  });

  it('offers every documented announcement type in the picker', () => {
    withList(THREE_DATED);
    openAdd();
    const opts = JSON.parse(screen.getByTestId('field-type').dataset.options);
    expect(opts.map((o) => o.value)).toEqual([
      'default',
      'ongoing',
      'success',
      'warning',
      'error',
    ]);
  });

  it('rejects an announcement with no content and keeps the modal open', async () => {
    withList(THREE_DATED);
    openAdd();
    emit('type', 'warning');
    await clickAsync(modalOk('添加公告'));

    expect(showError).toHaveBeenCalledWith('请填写完整的公告信息');
    expect(rowCount()).toBe(3);
    expect(modalOpen('添加公告')).toBe(true);
    expect(API.put).not.toHaveBeenCalled();
  });

  it('rejects an announcement whose publish date was cleared', async () => {
    withList(THREE_DATED);
    openAdd();
    emit('content', '维护通知');
    emit('publishDate', null);
    await clickAsync(modalOk('添加公告'));

    expect(showError).toHaveBeenCalledWith('请填写完整的公告信息');
    expect(rowCount()).toBe(3);
  });

  it('starts the add modal blank rather than from the last edited row', () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));
    click(modalCancel('编辑公告'));
    openAdd();

    const init = JSON.parse(screen.getByTestId('modal-form').dataset.init);
    expect(init.content).toBe('');
    expect(init.type).toBe('default');
    expect(init.extra).toBe('');
  });

  it('leaves the addition unsent until 保存设置 is pressed', () => {
    withList(THREE_DATED);
    openAdd();
    emit('content', '维护通知');
    click(modalOk('添加公告'));

    expect(API.put).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('SettingsAnnouncements enlarge editor', () => {
  it('pushes the enlarged text back into the form on confirm', () => {
    withList(THREE_DATED);
    click(screen.getByText('添加公告'));
    click(screen.getByText('放大编辑'));

    fireEvent.change(screen.getByTestId('big-textarea'), {
      target: { value: '很长的公告正文' },
    });
    click(modalOk('编辑公告内容'));

    expect(H.formApiCalls).toContainEqual(['content', '很长的公告正文']);
    expect(modalOpen('编辑公告内容')).toBe(false);
  });

  it('carries the enlarged text through to the saved announcement', async () => {
    withList(THREE_DATED);
    click(screen.getByText('添加公告'));
    click(screen.getByText('放大编辑'));
    fireEvent.change(screen.getByTestId('big-textarea'), {
      target: { value: '很长的公告正文' },
    });
    click(modalOk('编辑公告内容'));
    await clickAsync(modalOk('添加公告'));
    await clickAsync(saveButton());

    expect(savedList().at(-1).content).toBe('很长的公告正文');
  });

  it('seeds the enlarge editor with what is already in the form', () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));
    click(screen.getByText('放大编辑'));
    expect(screen.getByTestId('big-textarea')).toHaveValue('newest notice');
  });

  // DEFECT (not locked, see below): cancelling the enlarge editor closes it but
  // does not undo the edits it already made — the standalone TextArea writes
  // straight into announcementForm on every keystroke, so 取消 keeps the text.
  // It is pinned here only as far as is fix-safe: the enclosing add/edit modal
  // must stay open either way, so no work is lost by whichever behaviour wins.
  it('keeps the underlying announcement modal open when the enlarge editor is cancelled', () => {
    withList(THREE_DATED);
    click(screen.getByText('添加公告'));
    click(screen.getByText('放大编辑'));
    fireEvent.change(screen.getByTestId('big-textarea'), {
      target: { value: 'typed' },
    });
    click(modalCancel('编辑公告内容'));

    expect(modalOpen('编辑公告内容')).toBe(false);
    expect(modalOpen('添加公告')).toBe(true);
  });
});

describe('SettingsAnnouncements edit', () => {
  it('seeds the modal from the record behind the clicked row, not its position', () => {
    // Screen row 0 is stored index 1. A position-keyed edit would load
    // "oldest notice" here.
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));

    const init = JSON.parse(screen.getByTestId('modal-form').dataset.init);
    expect(init.content).toBe('newest notice');
    expect(init.type).toBe('warning');
    expect(init.extra).toBe('note-2');
  });

  it('updates only the edited announcement and leaves the others alone', async () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));
    emit('content', 'newest notice v2');
    await clickAsync(modalOk('编辑公告'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([7, 2, 9]);
    const byId = Object.fromEntries(savedList().map((a) => [a.id, a]));
    expect(byId[2].content).toBe('newest notice v2');
    expect(byId[7].content).toBe('oldest notice');
    expect(byId[9].content).toBe('middle notice');
  });

  it('keeps the edited announcement own publish date rather than stamping today', async () => {
    withList(THREE_DATED);
    click(within(row(2)).getByText('编辑'));
    emit('content', 'oldest notice v2');
    await clickAsync(modalOk('编辑公告'));
    await clickAsync(saveButton());

    const byId = Object.fromEntries(savedList().map((a) => [a.id, a]));
    expect(byId[7].publishDate).toBe('2024-01-01T00:00:00.000Z');
  });

  it('keeps fields the edit modal never showed', async () => {
    withList([{ ...THREE_DATED[0], banner: 'top', order: 5 }, THREE_DATED[1]]);
    click(within(row(1)).getByText('编辑'));
    emit('content', 'oldest notice v2');
    await clickAsync(modalOk('编辑公告'));
    await clickAsync(saveButton());

    const byId = Object.fromEntries(savedList().map((a) => [a.id, a]));
    expect(byId[7].banner).toBe('top');
    expect(byId[7].order).toBe(5);
    expect(byId[7].content).toBe('oldest notice v2');
  });

  it('refuses an edit that blanks the content, leaving the stored one intact', async () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));
    emit('content', '');
    await clickAsync(modalOk('编辑公告'));

    expect(showError).toHaveBeenCalledWith('请填写完整的公告信息');
    expect(screen.getByTestId('cell-0-content')).toHaveTextContent(
      'newest notice',
    );
  });

  it('discards the pending edit when the modal is cancelled', () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('编辑'));
    emit('content', 'changed');
    click(modalCancel('编辑公告'));

    expect(screen.getByTestId('cell-0-content')).toHaveTextContent(
      'newest notice',
    );
    expect(saveButton()).toBeDisabled();
  });
});

describe('SettingsAnnouncements delete', () => {
  it('removes the record behind the clicked row, not the first stored entry', () => {
    // Screen row 0 is id 2, stored at index 1.
    withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));

    expect(rowIds()).toEqual(['9', '7']);
  });

  it('removes the last screen row when that is the one clicked', () => {
    withList(THREE_DATED);
    click(within(row(2)).getByText('删除'));
    click(modalOk('确认删除'));

    expect(rowIds()).toEqual(['2', '9']);
  });

  it('keeps every row when the confirmation is cancelled', () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalCancel('确认删除'));

    expect(rowIds()).toEqual(['2', '9', '7']);
    expect(saveButton()).toBeDisabled();
  });

  it('does not send the deletion until 保存设置 is pressed', () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));

    expect(API.put).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });

  it('deletes only the selected announcements in a batch', () => {
    withList(THREE_DATED);
    act(() => H.rowSelection.onChange([9], []));
    click(batchButton());

    expect(rowIds()).toEqual(['2', '7']);
  });

  it('cannot start a batch delete with nothing selected', () => {
    withList(THREE_DATED);
    expect(batchButton()).toBeDisabled();
    click(batchButton());

    expect(rowIds()).toEqual(['2', '9', '7']);
    expect(saveButton()).toBeDisabled();
  });

  it('arms the batch button and counts the selection once rows are picked', () => {
    withList(THREE_DATED);
    act(() => H.rowSelection.onChange([9, 7], []));
    expect(batchButton()).not.toBeDisabled();
    expect(batchButton()).toHaveTextContent('(2)');
  });

  it('clears the selection after a batch delete so ids cannot be reused', () => {
    withList(THREE_DATED);
    act(() => H.rowSelection.onChange([9], []));
    click(batchButton());
    expect(rowCount()).toBe(2);
    expect(batchButton()).toBeDisabled();
  });
});

describe('SettingsAnnouncements save', () => {
  it('writes the whole list under the announcements key', async () => {
    withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(ANN_KEY);
    expect(savedIds()).toEqual([7, 9]);
  });

  it('writes back to the new key even when the data came from the legacy one', async () => {
    // Migration must not write back into Announcements.
    renderPanel({ Announcements: JSON.stringify(THREE_DATED) });
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    expect(lastPut().key).toBe(ANN_KEY);
  });

  it('includes announcements that are not on the visible page', async () => {
    const SIX = [7, 2, 9, 4, 11, 3].map((id, i) => ({
      id,
      content: `c-${id}`,
      publishDate: `2020-0${i + 1}-01T00:00:00.000Z`,
      type: 'default',
      extra: '',
    }));
    withList(SIX);
    act(() => H.pagination.onChange(1, 5));
    expect(rowCount()).toBe(5);
    expect(total()).toBe(6);

    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    // Newest is 2020-06 (id 3); deleting it must leave the other five,
    // including the one that was off-page.
    expect(savedIds().sort((a, b) => a - b)).toEqual([2, 4, 7, 9, 11]);
  });

  it('reports success and refreshes the parent when the server accepts', async () => {
    const { refresh } = withList(THREE_DATED);
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith('系统公告已更新'),
    );
    expect(refresh).toHaveBeenCalled();
    expect(saveButton()).toBeDisabled();
  });

  it('starts disabled because there is nothing pending', () => {
    withList(THREE_DATED);
    expect(saveButton()).toBeDisabled();
  });

  it('keeps the save armed when the request throws', async () => {
    const { refresh } = withList(THREE_DATED);
    API.put.mockRejectedValue(new Error('network down'));
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('系统公告更新失败'),
    );
    expect(showSuccess).not.toHaveBeenCalledWith('系统公告已更新');
    expect(refresh).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: a write the server refuses still clears the pending flag', () => {
  // updateOption resolves normally on success:false — it only calls showError.
  // submitAnnouncements then reaches setHasChanges(false) and greys out
  // 保存设置. The edits stay in local state with no way left to send them. The
  // throw path is correct, so the two failure modes contradict each other.

  it('does not report a refused write as successful', async () => {
    const { refresh } = withList(THREE_DATED);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    // The local delete legitimately toasts "公告已删除…"; the toast that must
    // not appear is the one claiming the blob reached the server.
    expect(showSuccess).not.toHaveBeenCalledWith('系统公告已更新');
    expect(refresh).not.toHaveBeenCalled();
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expect(element).not.toBeDisabled()".
  it.skip('leaves the save button armed after a refused write', async () => {
    withList(THREE_DATED);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: a stored id of 0 is rewritten and can collide', () => {
  // parseAnnouncements normalises with `id: item.id || index + 1`. Zero is a
  // legitimate id and is falsy, so the entry at index 1 below is renumbered to
  // 2 — the id the following entry already carries. Delete and edit both match
  // on id, so acting on one of the pair acts on both, and the next 保存设置
  // makes it permanent. `??` would preserve the 0.

  const ZERO_ID = [
    {
      id: 4,
      content: 'A',
      publishDate: '2024-01-01T00:00:00.000Z',
      type: 'default',
      extra: '',
    },
    {
      id: 0,
      content: 'B',
      publishDate: '2025-01-01T00:00:00.000Z',
      type: 'default',
      extra: '',
    },
    {
      id: 2,
      content: 'C',
      publishDate: '2026-01-01T00:00:00.000Z',
      type: 'default',
      extra: '',
    },
  ];

  it('keeps ids distinct when no stored id is falsy', () => {
    // Control on the same normalisation path.
    withList([
      { ...ZERO_ID[0], id: 4 },
      { ...ZERO_ID[1], id: 1 },
      { ...ZERO_ID[2], id: 2 },
    ]);
    // Sorted newest-first: C(2026), B(2025), A(2024).
    expect(rowIds()).toEqual(['2', '1', '4']);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 2 to be 3" — B and C both became id 2.
  it.skip('gives every row a distinct id even when one is stored as 0', () => {
    withList(ZERO_ID);
    expect(new Set(rowIds()).size).toBe(3);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected [ '4' ] to deeply equal [ '0', '4' ]" — deleting C took B too.
  it.skip('deletes only the announcement that was asked for', () => {
    withList(ZERO_ID);
    // Screen row 0 is C (newest).
    click(within(row(0)).getByText('删除'));
    click(modalOk('确认删除'));
    expect(rowIds()).toEqual(['0', '4']);
  });
});

describe('DEFECT: an unloaded panel saves an empty list over the stored one', () => {
  // DashboardSetting seeds console_setting.announcements to '' and renders this
  // panel immediately, before GET /api/option/ answers. parseAnnouncements maps
  // '' to "no announcements" and cannot tell that apart from "not loaded yet",
  // so the panel is fully interactive over a list it never read. Publishing one
  // notice in that window and pressing 保存设置 PUTs a one-element blob over
  // whatever the server holds — every existing announcement is gone and the
  // toast says it worked.
  //
  // No `it('currently …')` companion on purpose: every assertion that pins
  // today's outcome (the PUT happening, its length, the success toast) is
  // exactly what a fix has to change.

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 'spy' to not be called with arguments" — the PUT went out.
  it.skip('does not write a collection it never loaded', async () => {
    renderPanel({ [ANN_KEY]: '' });
    click(screen.getByText('添加公告'));
    emit('content', '维护通知');
    click(modalOk('添加公告'));
    await clickAsync(saveButton());

    expect(API.put).not.toHaveBeenCalledWith(
      '/api/option/',
      expect.objectContaining({ key: ANN_KEY }),
    );
  });
});

describe('DEFECT: a malformed stored publishDate makes an announcement uneditable', () => {
  // handleEditAnnouncement does `new Date(announcement.publishDate)` without
  // checking the result, and handleSaveAnnouncement then calls .toISOString()
  // on it. A stored date the browser cannot parse yields an Invalid Date, so
  // toISOString throws RangeError, the catch turns it into a generic
  // "操作失败: …" toast, and the announcement can never be corrected through
  // the console — including correcting the very date that is broken.

  const BAD_DATE = [
    {
      id: 7,
      content: 'broken date',
      publishDate: '2024-13-45 not a date',
      type: 'default',
      extra: '',
    },
  ];

  it('edits an announcement whose stored date is parseable', async () => {
    // Control: identical flow, valid date. This is what the broken-date case
    // must be brought in line with.
    withList([{ ...BAD_DATE[0], publishDate: '2024-01-01T00:00:00.000Z' }]);
    click(within(row(0)).getByText('编辑'));
    emit('content', 'fixed');
    await clickAsync(modalOk('编辑公告'));

    expect(screen.getByTestId('cell-0-content')).toHaveTextContent('fixed');
    await clickAsync(saveButton());
    expect(savedList()[0].content).toBe('fixed');
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expect(element).toHaveTextContent()" — the row still read "broken date",
  // because handleSaveAnnouncement threw before it ever reached
  // setAnnouncementsList. (Asserted on the rendered row rather than on the
  // saved blob: nothing is ever sent in this state, so reading the request
  // would fail with an unrelated TypeError instead of naming the defect.)
  it.skip('edits an announcement whose stored date is not parseable', async () => {
    withList(BAD_DATE);
    click(within(row(0)).getByText('编辑'));
    emit('content', 'fixed');
    await clickAsync(modalOk('编辑公告'));

    expect(screen.getByTestId('cell-0-content')).toHaveTextContent('fixed');
  });
});

describe('SettingsAnnouncements pagination', () => {
  const SIX = [7, 2, 9, 4, 11, 3].map((id, i) => ({
    id,
    content: `c-${id}`,
    publishDate: `2020-0${i + 1}-01T00:00:00.000Z`,
    type: 'default',
    extra: '',
  }));

  it('reports the full list length as the total, not the page length', () => {
    withList(SIX);
    act(() => H.pagination.onChange(1, 5));
    expect(rowCount()).toBe(5);
    expect(total()).toBe(6);
  });

  it('pages through the date-sorted order, not the stored order', () => {
    // Stored order is 7,2,9,4,11,3 with ascending dates, so newest-first is the
    // exact reverse; the last page must hold the OLDEST announcement.
    withList(SIX);
    act(() => H.pagination.onChange(1, 5));
    expect(rowIds()).toEqual(['3', '11', '4', '9', '2']);
    act(() => H.pagination.onChange(2, 5));
    expect(rowIds()).toEqual(['7']);
  });

  it('returns to page one when the page size is changed from the size picker', () => {
    withList(SIX);
    act(() => H.pagination.onChange(2, 5));
    expect(rowIds()).toEqual(['7']);
    act(() => H.pagination.onShowSizeChange(2, 2));
    expect(rowIds()).toEqual(['3', '11']);
  });

  // Not asserted: onSelect / onSelectAll only console.log and
  // getCheckboxProps returns a constant {disabled:false}. There is no
  // observable behaviour to check, so pinning the log output would lock in
  // noise rather than protect anything.
});
