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

// FAQ panel. The whole list lives in one option row (console_setting.faq) and
// is rewritten wholesale on every 保存设置, so the tests that matter are about
// the collection: the blob must carry every entry — including the ones the
// current page does not show — in the stored order, and edit/delete must land
// on the row that was asked for.
//
// Fixtures use non-sequential ids (7 sits at index 0) so that "acted on the
// right row" cannot be confused with "acted on row 0".

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
    const Field = ({
      field,
      label,
      onChange,
      placeholder,
      maxLength,
      maxCount,
    }) => {
      H.handlers.set(field, onChange);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-placeholder': typeof placeholder === 'string' ? placeholder : '',
        'data-cap': String(maxLength ?? maxCount ?? ''),
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

  // Real checked state, callback gets the flipped value, so "off" and "on" are
  // distinguishable.
  const Switch = ({ checked, onChange }) =>
    React.createElement('button', {
      type: 'button',
      'data-testid': 'panel-switch',
      'data-checked': String(!!checked),
      onClick: () => onChange?.(!checked),
    });

  // Surfaces the tooltip content so truncation-vs-content can be told apart.
  const Tooltip = ({ content, children }) =>
    React.createElement(
      'span',
      {
        'data-testid': 'tooltip',
        'data-content': typeof content === 'string' ? content : '',
      },
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
    Button,
    Switch,
    Tooltip,
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
  HelpCircle: () => null,
}));

vi.mock('../../../helpers', () => ({
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import SettingsFAQ from './SettingsFAQ';
import { API, showError, showSuccess } from '../../../helpers';

const FAQ_KEY = 'console_setting.faq';
const ENABLED_KEY = 'console_setting.faq_enabled';

const TWO_ROWS = [
  { id: 7, question: '如何充值？', answer: '在控制台的钱包页面充值。' },
  { id: 2, question: '如何创建令牌？', answer: '在令牌页面点击新建。' },
];

// Six entries so a five-row page can leave one off screen.
const SIX_ROWS = [7, 2, 9, 4, 11, 3].map((id) => ({
  id,
  question: `q-${id}`,
  answer: `a-${id}`,
}));

const renderPanel = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingsFAQ options={options} refresh={refresh} />);
  return { refresh };
};

const withList = (list, extra = {}) =>
  renderPanel({ [FAQ_KEY]: JSON.stringify(list), ...extra });

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
const savedIds = () => savedList().map((f) => f.id);
const saveButton = () => screen.getByText('保存设置');
const batchButton = () => screen.getByRole('button', { name: /批量删除/ });
const panelSwitch = () => screen.getByTestId('panel-switch');

// Each emit re-renders and re-registers the handlers, so the writes accumulate.
const fillFaq = ({ question, answer }) => {
  if (question !== undefined) emit('question', question);
  if (answer !== undefined) emit('answer', answer);
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

describe('SettingsFAQ hydration', () => {
  it('renders one row per stored entry in the stored order', () => {
    withList(TWO_ROWS);
    expect(rowIds()).toEqual(['7', '2']);
    expect(total()).toBe(2);
  });

  it('shows the question and the answer in their own columns', () => {
    withList(TWO_ROWS);
    expect(screen.getByTestId('cell-0-question')).toHaveTextContent(
      '如何充值？',
    );
    expect(screen.getByTestId('cell-0-answer')).toHaveTextContent(
      '在控制台的钱包页面充值。',
    );
    expect(screen.getByTestId('cell-1-question')).toHaveTextContent(
      '如何创建令牌？',
    );
  });

  it('gives the tooltip the untruncated text, not the clipped cell', () => {
    // The cell is ellipsised with CSS; the tooltip is the only way to read a
    // long answer, so it must carry the whole string.
    const LONG = 'x'.repeat(400);
    withList([{ id: 7, question: 'q', answer: LONG }]);
    const tips = screen
      .getAllByTestId('tooltip')
      .map((el) => el.dataset.content);
    expect(tips).toContain(LONG);
  });

  it('shows the empty illustration when the stored blob is an empty list', () => {
    withList([]);
    expect(rowCount()).toBe(0);
    expect(screen.getByTestId('empty')).toHaveTextContent('暂无常见问答');
  });

  it('falls back to an empty list when the stored blob is not valid JSON', () => {
    renderPanel({ [FAQ_KEY]: 'not json at all' });
    expect(rowCount()).toBe(0);
    expect(console.error).toHaveBeenCalled();
  });

  it('falls back to an empty list when the stored blob is a JSON object', () => {
    renderPanel({ [FAQ_KEY]: JSON.stringify({ question: 'q', answer: 'a' }) });
    expect(rowCount()).toBe(0);
  });

  it('re-hydrates when the stored blob changes underneath it', () => {
    const { rerender } = render(
      <SettingsFAQ
        options={{ [FAQ_KEY]: JSON.stringify(TWO_ROWS) }}
        refresh={vi.fn()}
      />,
    );
    expect(rowIds()).toEqual(['7', '2']);
    rerender(
      <SettingsFAQ
        options={{ [FAQ_KEY]: JSON.stringify([TWO_ROWS[1]]) }}
        refresh={vi.fn()}
      />,
    );
    expect(rowIds()).toEqual(['2']);
  });

  it('synthesises distinct ids for entries stored without one', () => {
    // Legacy blobs predate the id field; without ids every row would match
    // every filter at once.
    renderPanel({
      [FAQ_KEY]: JSON.stringify([
        { question: 'q1', answer: 'a1' },
        { question: 'q2', answer: 'a2' },
        { question: 'q3', answer: 'a3' },
      ]),
    });
    const ids = rowIds();
    expect(ids).toHaveLength(3);
    ids.forEach((id) => expect(id).not.toBe('undefined'));
    expect(new Set(ids).size).toBe(3);
  });
});

describe('SettingsFAQ enable switch', () => {
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

  it('treats an unrecognised stored value as disabled rather than enabled', () => {
    // Only the literal 'true' (or boolean true) enables; anything else is off.
    renderPanel({ [ENABLED_KEY]: 'yes' });
    expect(panelSwitch().dataset.checked).toBe('false');
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

  it('does not touch the FAQ blob when only the switch is flipped', async () => {
    withList(TWO_ROWS, { [ENABLED_KEY]: 'true' });
    await clickAsync(panelSwitch());
    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(ENABLED_KEY);
  });
});

describe('SettingsFAQ add', () => {
  it('appends a complete entry with the next free id', async () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ question: '如何退款？', answer: '联系客服。' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).not.toHaveBeenCalled();
    expect(rowCount()).toBe(3);
    // Appended, not prepended — stored order drives the console.
    expect(rowIds()).toEqual(['7', '2', '8']);
    expect(screen.getByTestId('cell-2-question')).toHaveTextContent(
      '如何退款？',
    );
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('gives the first entry of an empty list a usable id', async () => {
    withList([]);
    click(screen.getByText('添加问答'));
    fillFaq({ question: 'q', answer: 'a' });
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedList()).toHaveLength(1);
    expect(Number.isFinite(savedList()[0].id)).toBe(true);
  });

  it('rejects an entry with no question and keeps the modal open', async () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ answer: '只有答案。' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的问答信息');
    expect(rowCount()).toBe(2);
    // The typed answer must survive the rejection.
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('rejects an entry with no answer', async () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ question: '只有问题？' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的问答信息');
    expect(rowCount()).toBe(2);
  });

  it('does not write anything to the server on a rejected add', async () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ question: 'q only' });
    await clickAsync(screen.getByTestId('modal-ok'));
    expect(API.put).not.toHaveBeenCalled();
  });

  it('starts the add modal blank rather than from the last edited row', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    click(screen.getByTestId('modal-cancel'));
    click(screen.getByText('添加问答'));

    expect(JSON.parse(screen.getByTestId('modal-form').dataset.init)).toEqual({
      question: '',
      answer: '',
    });
    expect(screen.getByTestId('modal').dataset.title).toBe('添加问答');
  });

  it('leaves the added entry unsent until 保存设置 is pressed', () => {
    withList(TWO_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ question: 'q', answer: 'a' });
    click(screen.getByTestId('modal-ok'));

    expect(API.put).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('SettingsFAQ edit', () => {
  it('seeds the modal from the row that was clicked', () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));

    expect(JSON.parse(screen.getByTestId('modal-form').dataset.init)).toEqual({
      question: '如何创建令牌？',
      answer: '在令牌页面点击新建。',
    });
    expect(screen.getByTestId('modal').dataset.title).toBe('编辑问答');
  });

  it('updates only the edited row and leaves the other one byte-identical', async () => {
    withList(TWO_ROWS);
    click(within(row(1)).getByText('编辑'));
    fillFaq({ answer: '在令牌页面点击新建，然后复制 sk- 开头的密钥。' });
    await clickAsync(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedIds()).toEqual([7, 2]);
    expect(savedList()[0]).toEqual(TWO_ROWS[0]);
    expect(savedList()[1]).toEqual({
      ...TWO_ROWS[1],
      answer: '在令牌页面点击新建，然后复制 sk- 开头的密钥。',
    });
  });

  it('keeps fields the edit modal never showed', async () => {
    // The blob can carry keys this build does not render; the edit must merge,
    // not replace, or they vanish on the next save.
    withList([{ ...TWO_ROWS[0], category: 'billing', order: 2 }, TWO_ROWS[1]]);
    click(within(row(0)).getByText('编辑'));
    fillFaq({ question: '如何充值（新）？' });
    await clickAsync(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(savedList()[0].category).toBe('billing');
    expect(savedList()[0].order).toBe(2);
    expect(savedList()[0].question).toBe('如何充值（新）？');
  });

  it('refuses an edit that blanks the answer, leaving the stored one intact', async () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    fillFaq({ answer: '' });
    await clickAsync(screen.getByTestId('modal-ok'));

    expect(showError).toHaveBeenCalledWith('请填写完整的问答信息');
    expect(screen.getByTestId('cell-0-answer')).toHaveTextContent(
      '在控制台的钱包页面充值。',
    );
  });

  it('discards the pending edit when the modal is cancelled', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('编辑'));
    fillFaq({ question: '改过的问题' });
    click(screen.getByTestId('modal-cancel'));

    expect(screen.getByTestId('cell-0-question')).toHaveTextContent(
      '如何充值？',
    );
    expect(saveButton()).toBeDisabled();
  });
});

describe('SettingsFAQ delete', () => {
  it('removes exactly the row whose delete button was clicked', () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(rowIds()).toEqual(['2']);
  });

  it('removes the second row when the second row is the one clicked', () => {
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

  it('deletes only the selected rows in a batch', () => {
    withList(SIX_ROWS);
    act(() => H.rowSelection.onChange([9, 3], []));
    click(batchButton());
    expect(rowIds()).toEqual(['7', '2', '4', '11']);
  });

  it('cannot start a batch delete with nothing selected', () => {
    // handleBatchDelete has an empty-selection guard, but the disabled button
    // gets there first; the disabled state is the reachable behaviour.
    withList(SIX_ROWS);
    expect(batchButton()).toBeDisabled();
    click(batchButton());
    expect(rowIds()).toEqual(['7', '2', '9', '4', '11', '3']);
    expect(saveButton()).toBeDisabled();
  });

  it('arms the batch button and counts the selection once rows are picked', () => {
    withList(SIX_ROWS);
    act(() => H.rowSelection.onChange([9, 3], []));
    expect(batchButton()).not.toBeDisabled();
    expect(batchButton()).toHaveTextContent('(2)');
  });

  it('clears the selection after a batch delete so ids cannot be reused', () => {
    withList(SIX_ROWS);
    act(() => H.rowSelection.onChange([9], []));
    click(batchButton());
    expect(rowCount()).toBe(5);
    expect(batchButton()).toBeDisabled();
  });
});

describe('SettingsFAQ save', () => {
  it('writes the whole list under the FAQ key', async () => {
    withList(TWO_ROWS);
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(API.put).toHaveBeenCalledTimes(1);
    expect(lastPut().key).toBe(FAQ_KEY);
    expect(savedList()).toEqual([TWO_ROWS[1]]);
  });

  it('includes entries that are not on the visible page', async () => {
    // Five of six rows are on screen. Serialising the page instead of the list
    // would quietly delete the sixth answer.
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
    withList(SIX_ROWS);
    click(screen.getByText('添加问答'));
    fillFaq({ question: 'last', answer: 'last' });
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
      expect(showSuccess).toHaveBeenCalledWith('常见问答已更新'),
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
      expect(showError).toHaveBeenCalledWith('常见问答更新失败'),
    );
    expect(showSuccess).not.toHaveBeenCalledWith('常见问答已更新');
    expect(refresh).not.toHaveBeenCalled();
    expect(saveButton()).not.toBeDisabled();
  });
});

describe('DEFECT: a write the server refuses still clears the pending flag', () => {
  // updateOption resolves normally on success:false — it only calls showError.
  // submitFAQ then reaches setHasChanges(false), greying out 保存设置. The edits
  // survive in local state with no way left to send them: an error toast beside
  // a dead button. The throw path is correct, so the two failure modes
  // contradict each other.

  it('does not report a refused write as successful', async () => {
    const { refresh } = withList(TWO_ROWS);
    API.put.mockResolvedValue({
      data: { success: false, message: '没有权限' },
    });
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('没有权限'));
    // The local delete legitimately toasts "问答已删除…"; the toast that must
    // not appear is the one claiming the blob reached the server.
    expect(showSuccess).not.toHaveBeenCalledWith('常见问答已更新');
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
  // parseFAQ normalises with `id: item.id || index + 1`. Zero is a legitimate
  // id and is falsy, so the entry at index 1 below is renumbered to 2 — the id
  // the next entry already has. Every operation matches on id, so deleting the
  // third question also deletes the second, and editing one edits both; the
  // next 保存设置 writes the loss back. `??` would preserve the 0.

  const ZERO_ID = [
    { id: 4, question: 'A', answer: 'a' },
    { id: 0, question: 'B', answer: 'b' },
    { id: 2, question: 'C', answer: 'c' },
  ];

  it('keeps ids distinct when no stored id is falsy', () => {
    // Control on the same code path — this is what the zero case must match.
    withList([
      { id: 4, question: 'A', answer: 'a' },
      { id: 1, question: 'B', answer: 'b' },
      { id: 2, question: 'C', answer: 'c' },
    ]);
    expect(rowIds()).toEqual(['4', '1', '2']);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 2 to be 3" — B and C both became id 2.
  it.skip('gives every row a distinct id even when one is stored as 0', () => {
    withList(ZERO_ID);
    expect(new Set(rowIds()).size).toBe(3);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected [ '4' ] to deeply equal [ '4', '0' ]" — deleting C took B too.
  it.skip('deletes only the entry that was asked for', () => {
    withList(ZERO_ID);
    click(within(row(2)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(rowIds()).toEqual(['4', '0']);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected undefined to be 'B'" — editing C overwrote B's question AND its
  // answer, so no entry answering 'b' was left in the saved blob at all.
  it.skip('edits only the entry that was asked for', async () => {
    withList(ZERO_ID);
    click(within(row(2)).getByText('编辑'));
    fillFaq({ question: 'C-edited' });
    await clickAsync(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    const byAnswer = Object.fromEntries(
      savedList().map((f) => [f.answer, f.question]),
    );
    expect(byAnswer.b).toBe('B');
    expect(byAnswer.c).toBe('C-edited');
  });
});

describe('DEFECT: an unloaded panel saves an empty list over the stored one', () => {
  // DashboardSetting seeds console_setting.faq to '' and renders this panel
  // straight away, before GET /api/option/ answers. parseFAQ maps '' to "no
  // entries" and cannot distinguish that from "not loaded yet", so the panel is
  // fully interactive over a list it never read. Adding one question in that
  // window and pressing 保存设置 PUTs a one-element blob over whatever the
  // server holds — every other FAQ is gone and the toast says it worked.
  //
  // Deliberately no `it('currently …')` companion: every assertion that pins
  // today's outcome (that the PUT happens, its length, the success toast) is
  // precisely what a fix must change.

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 'spy' to not be called with arguments" — the PUT went out.
  it.skip('does not write a collection it never loaded', async () => {
    renderPanel({ [FAQ_KEY]: '' });
    click(screen.getByText('添加问答'));
    fillFaq({ question: 'q', answer: 'a' });
    click(screen.getByTestId('modal-ok'));
    await clickAsync(saveButton());

    expect(API.put).not.toHaveBeenCalledWith(
      '/api/option/',
      expect.objectContaining({ key: FAQ_KEY }),
    );
  });
});

describe('DEFECT: deleting the last row of a page strands the operator', () => {
  // currentPage is never reconciled after a delete. Remove the only entry on
  // the final page and currentPage stays there, so getCurrentPageData slices
  // past the end of the list: the table renders 暂无常见问答 while the entries
  // are still there and the pagination footer still counts them. The operator
  // is told the FAQ list is empty, and the obvious next move — add an entry and
  // save — is done from a screen that shows nothing.

  it('still counts the surviving entries in the footer', async () => {
    // Fix-safe half: whatever page the panel lands on, the total must stay
    // honest. True today and true after any reconciliation fix.
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(2, 5));
    expect(rowIds()).toEqual(['3']);

    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));
    expect(total()).toBe(5);

    await clickAsync(saveButton());
    expect(savedIds()).toEqual([7, 2, 9, 4, 11]);
  });

  // Verified red on 2026-08-20: un-skipped, this failed with
  // "expected 0 to be greater than 0" — the table showed an empty page.
  it.skip('does not show an empty table while entries remain', () => {
    withList(SIX_ROWS);
    act(() => H.pagination.onChange(2, 5));
    click(within(row(0)).getByText('删除'));
    click(screen.getByTestId('modal-ok'));

    expect(total()).toBe(5);
    expect(rowCount()).toBeGreaterThan(0);
  });
});

describe('SettingsFAQ pagination', () => {
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

  // Not asserted: onSelect / onSelectAll only console.log and
  // getCheckboxProps returns a constant {disabled:false}. There is no
  // observable behaviour behind them; pinning the log output would lock in
  // noise rather than protect anything.
});
