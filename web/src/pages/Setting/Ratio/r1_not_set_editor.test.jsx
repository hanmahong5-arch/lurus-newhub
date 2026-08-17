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
import {
  render,
  screen,
  fireEvent,
  waitFor,
  within,
} from '@testing-library/react';

// Semi-UI cannot be imported for real under jsdom: the barrel pulls in
// semi-illustrations -> lottie-web -> HTMLCanvasElement.getContext, which jsdom
// does not implement. Same reason the Flows page test stubs it. The stubs below
// keep the parts this editor actually depends on: Table invokes every column
// render(), Modal exposes its onOk/onCancel, Input forwards the raw value the
// way Semi does (onChange(value), not onChange(event)).
vi.mock('@douyinfe/semi-ui', () => {
  const cellKey = (col, idx) => String(col.key ?? col.dataIndex ?? idx);
  const rowKeyOf = (record, rowKey, idx) =>
    typeof rowKey === 'function' ? rowKey(record) : (record?.[rowKey] ?? idx);

  const Table = ({
    columns = [],
    dataSource = [],
    rowSelection,
    rowKey,
    pagination,
    empty,
  }) =>
    React.createElement(
      'div',
      { 'data-testid': 'table' },
      React.createElement(
        'div',
        { 'data-testid': 'thead' },
        columns.map((col, i) =>
          React.createElement(
            'span',
            { key: i, 'data-testid': `th-${cellKey(col, i)}` },
            col.title,
          ),
        ),
      ),
      dataSource.length === 0 && empty
        ? React.createElement('div', { 'data-testid': 'table-empty' }, empty)
        : null,
      dataSource.map((record, rowIdx) => {
        const rk = rowKeyOf(record, rowKey, rowIdx);
        return React.createElement(
          'div',
          { key: rk, 'data-testid': `row-${rk}` },
          rowSelection
            ? React.createElement('input', {
                type: 'checkbox',
                'data-testid': `row-select-${rk}`,
                checked: (rowSelection.selectedRowKeys || []).includes(rk),
                onChange: (e) => {
                  const cur = rowSelection.selectedRowKeys || [];
                  rowSelection.onChange(
                    e.target.checked
                      ? [...cur, rk]
                      : cur.filter((k) => k !== rk),
                  );
                },
              })
            : null,
          columns.map((col, i) =>
            React.createElement(
              'span',
              { key: i, 'data-testid': `cell-${rk}-${cellKey(col, i)}` },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
        );
      }),
      pagination
        ? React.createElement(
            'div',
            { 'data-testid': 'pagination' },
            React.createElement(
              'span',
              { 'data-testid': 'pg-total' },
              String(pagination.total),
            ),
            React.createElement(
              'span',
              { 'data-testid': 'pg-current' },
              String(pagination.currentPage),
            ),
            pagination.onPageChange
              ? React.createElement(
                  'button',
                  {
                    'data-testid': 'pg-next',
                    onClick: () =>
                      pagination.onPageChange(pagination.currentPage + 1),
                  },
                  'next',
                )
              : null,
            pagination.onPageSizeChange
              ? React.createElement(
                  'button',
                  {
                    'data-testid': 'pg-size-20',
                    onClick: () => pagination.onPageSizeChange(20),
                  },
                  'size20',
                )
              : null,
          )
        : null,
    );

  const Button = ({
    children,
    onClick,
    disabled,
    loading,
    icon: _i,
    ...rest
  }) =>
    React.createElement(
      'button',
      { onClick, disabled: !!(disabled || loading), ...rest },
      children,
    );

  const Input = ({
    value,
    onChange,
    placeholder,
    disabled,
    prefix: _p,
    ...rest
  }) =>
    React.createElement('input', {
      type: 'text',
      value: value ?? '',
      placeholder,
      disabled: !!disabled,
      onChange: (e) => onChange && onChange(e.target.value),
      ...rest,
    });

  const Modal = ({ visible, title, children, onOk, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': `modal-${title}` },
          children,
          React.createElement(
            'button',
            { 'data-testid': `ok-${title}`, onClick: onOk },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': `cancel-${title}`, onClick: onCancel },
            'cancel',
          ),
        )
      : null;

  const Form = ({ children }) => React.createElement('div', null, children);
  Form.Input = ({ field, onChange, value, placeholder, label: _l }) =>
    React.createElement('input', {
      type: 'text',
      'data-testid': `form-${field}`,
      placeholder,
      defaultValue: value ?? '',
      onChange: (e) => onChange && onChange(e.target.value),
    });
  Form.Switch = ({ field, onChange, label }) =>
    React.createElement(
      'label',
      null,
      React.createElement('input', {
        type: 'checkbox',
        'data-testid': `form-${field}`,
        onChange: (e) => onChange && onChange(e.target.checked),
      }),
      label,
    );
  Form.Section = ({ text, children }) =>
    React.createElement('div', null, text, children);

  const Space = ({ children }) => React.createElement('div', null, children);
  const Typography = {
    Text: ({ children }) => React.createElement('span', null, children),
  };
  const Radio = ({ checked, onChange, children }) =>
    React.createElement(
      'label',
      null,
      React.createElement('input', {
        type: 'radio',
        'data-testid': `radio-${String(children)}`,
        checked: !!checked,
        onChange: () => onChange && onChange(),
      }),
      children,
    );
  const Notification = { success: vi.fn(), error: vi.fn() };

  return {
    Table,
    Button,
    Input,
    Modal,
    Form,
    Space,
    Typography,
    Radio,
    Notification,
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconDelete: () => null,
  IconPlus: () => null,
  IconSearch: () => null,
  IconSave: () => null,
  IconBolt: () => null,
}));

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

// i18n keys in this file are the Chinese source strings; identity `t` keeps the
// assertions readable and mirrors the i18next zh behaviour.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, vars) =>
      vars
        ? Object.entries(vars).reduce(
            (s, [k, v]) => s.split(`{{${k}}}`).join(String(v)),
            key,
          )
        : key,
  }),
}));

import ModelRatioNotSetEditor from './ModelRationNotSetEditor';
import { API, showError, showSuccess } from '../../../helpers';
import { Notification } from '@douyinfe/semi-ui';

const enabled = (models) =>
  Promise.resolve({ data: { success: true, data: models } });
const putOk = () => Promise.resolve({ data: { success: true } });

/** Parse the JSON body the component PUT to /api/option/ for a given key. */
const putPayload = (key) => {
  const call = API.put.mock.calls.find((c) => c[1].key === key);
  if (!call) return undefined;
  return JSON.parse(call[1].value);
};

const renderEditor = async (
  options = {},
  models = ['alpha-1', 'alpha-2', 'beta-1'],
) => {
  API.get.mockImplementation(() => enabled(models));
  const refresh = vi.fn();
  const utils = render(
    <ModelRatioNotSetEditor options={options} refresh={refresh} />,
  );
  await waitFor(() => expect(API.get).toHaveBeenCalled());
  return { ...utils, refresh };
};

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  Notification.success.mockReset();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

// ─── Row derivation: which models count as "not set" ────────────────────────

describe('ModelRatioNotSetEditor row derivation', () => {
  it('lists only models that have neither a price nor a ratio', async () => {
    await renderEditor({
      ModelPrice: '{"beta-1":0.02}',
      ModelRatio: '{"alpha-1":2}',
      CompletionRatio: '{}',
    });

    await waitFor(() => expect(screen.getByTestId('row-alpha-2')).toBeTruthy());
    expect(screen.queryByTestId('row-alpha-1')).toBeNull();
    expect(screen.queryByTestId('row-beta-1')).toBeNull();
    expect(screen.getByTestId('pg-total').textContent).toBe('1');
  });

  it('treats a model that only has a completion ratio as still unset', async () => {
    // CompletionRatio alone is not consulted by the filter, so the model stays
    // in the "needs pricing" list — that is the intended contract because a
    // completion ratio without a model ratio still bills at the default.
    await renderEditor({
      ModelPrice: '{}',
      ModelRatio: '{}',
      CompletionRatio: '{"alpha-1":3}',
    });

    await waitFor(() => expect(screen.getByTestId('row-alpha-1')).toBeTruthy());
    const cell = screen.getByTestId('cell-alpha-1-completionRatio');
    expect(within(cell).getByRole('textbox').value).toBe('3');
  });

  it('renders the empty slot when every enabled model is already priced', async () => {
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{"only":1}', CompletionRatio: '{}' },
      ['only'],
    );

    await waitFor(() =>
      expect(screen.getByTestId('table-empty')).toHaveTextContent(
        '没有未设置的模型',
      ),
    );
  });

  it('surfaces a backend failure on the enabled-model fetch', async () => {
    API.get.mockImplementation(() =>
      Promise.resolve({ data: { success: false, message: 'boom' } }),
    );
    render(
      <ModelRatioNotSetEditor
        options={{ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' }}
        refresh={vi.fn()}
      />,
    );

    await waitFor(() => expect(showError).toHaveBeenCalledWith('boom'));
    expect(screen.queryByTestId('row-alpha-1')).toBeNull();
  });

  it('reports a thrown enabled-model fetch without rendering rows', async () => {
    API.get.mockImplementation(() => Promise.reject(new Error('offline')));
    render(
      <ModelRatioNotSetEditor
        options={{ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' }}
        refresh={vi.fn()}
      />,
    );

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('获取启用模型失败'),
    );
  });
});

// ─── Cell editing + validation ─────────────────────────────────────────────

describe('ModelRatioNotSetEditor cell validation', () => {
  const opts = { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' };

  it('rejects a non-numeric ratio and keeps the old value', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    const input = within(screen.getByTestId('cell-alpha-1-ratio')).getByRole(
      'textbox',
    );
    fireEvent.change(input, { target: { value: 'abc' } });

    expect(showError).toHaveBeenCalledWith('请输入数字');
    expect(input.value).toBe('');
  });

  it('accepts a numeric ratio and shows it in the cell', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    const input = within(screen.getByTestId('cell-alpha-1-ratio')).getByRole(
      'textbox',
    );
    fireEvent.change(input, { target: { value: '2.5' } });

    expect(showError).not.toHaveBeenCalled();
    await waitFor(() => expect(input.value).toBe('2.5'));
  });

  it('disables the two ratio cells once a fixed price is entered', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    const price = within(screen.getByTestId('cell-alpha-1-price')).getByRole(
      'textbox',
    );
    fireEvent.change(price, { target: { value: '0.05' } });

    await waitFor(() =>
      expect(
        within(screen.getByTestId('cell-alpha-1-ratio')).getByRole('textbox')
          .disabled,
      ).toBe(true),
    );
    expect(
      within(screen.getByTestId('cell-alpha-1-completionRatio')).getByRole(
        'textbox',
      ).disabled,
    ).toBe(true);
  });

  it.skip('should reject a whitespace-only ratio instead of writing null (DEFECT)', async () => {
    // updateModel guards with `isNaN(value)`, and Number(' ') === 0, so a
    // whitespace-only entry passes validation. SubmitData then does
    // parseFloat(' ') -> NaN, and JSON.stringify turns NaN into literal null,
    // so `{"alpha-1": null}` is persisted as the model's ratio.
    // Un-skip once the editor validates with a real numeric parse.
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));
    API.put.mockImplementation(putOk);

    const input = within(screen.getByTestId('cell-alpha-1-ratio')).getByRole(
      'textbox',
    );
    fireEvent.change(input, { target: { value: ' ' } });
    expect(showError).toHaveBeenCalledWith('请输入数字');

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({});
  });

  it.skip('should reject a negative ratio (DEFECT)', async () => {
    // There is no lower bound anywhere between the cell and the PUT, so an
    // operator can persist ModelRatio {"alpha-1": -5}. A negative ratio makes a
    // request refund quota instead of consuming it.
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    const input = within(screen.getByTestId('cell-alpha-1-ratio')).getByRole(
      'textbox',
    );
    fireEvent.change(input, { target: { value: '-5' } });
    expect(showError).toHaveBeenCalledWith('请输入数字');
  });
});

// ─── Submit: must MERGE into the existing table, never replace it ───────────

describe('ModelRatioNotSetEditor submit', () => {
  it('merges new ratios into the models that were already priced', async () => {
    API.put.mockImplementation(putOk);
    const { refresh } = await renderEditor(
      {
        ModelPrice: '{"beta-1":0.02}',
        ModelRatio: '{"alpha-1":2}',
        CompletionRatio: '{"alpha-1":3}',
      },
      ['alpha-1', 'alpha-2', 'beta-1'],
    );
    await waitFor(() => screen.getByTestId('row-alpha-2'));

    fireEvent.change(
      within(screen.getByTestId('cell-alpha-2-ratio')).getByRole('textbox'),
      { target: { value: '4' } },
    );
    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    // pre-existing entries survive
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 2, 'alpha-2': 4 });
    expect(putPayload('ModelPrice')).toEqual({ 'beta-1': 0.02 });
    expect(putPayload('CompletionRatio')).toEqual({ 'alpha-1': 3 });
    expect(refresh).toHaveBeenCalled();
  });

  it('lets a fixed price win and drops the ratio fields for that model', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' },
      ['alpha-2'],
    );
    await waitFor(() => screen.getByTestId('row-alpha-2'));

    fireEvent.change(
      within(screen.getByTestId('cell-alpha-2-price')).getByRole('textbox'),
      { target: { value: '0.5' } },
    );
    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    expect(putPayload('ModelPrice')).toEqual({ 'alpha-2': 0.5 });
    expect(putPayload('ModelRatio')).toEqual({});
  });

  it('writes nothing for models the operator left blank', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' },
      ['a', 'b'],
    );
    await waitFor(() => screen.getByTestId('row-a'));

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(API.put).toHaveBeenCalledTimes(3));
    expect(putPayload('ModelRatio')).toEqual({});
    expect(putPayload('ModelPrice')).toEqual({});
    expect(putPayload('CompletionRatio')).toEqual({});
  });

  it('reports a rejected option write and does not claim success', async () => {
    API.put.mockImplementation(() =>
      Promise.resolve({ data: { success: false, message: 'db down' } }),
    );
    const { refresh } = await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' },
      ['alpha-1'],
    );
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('db down'));
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('flags a partially-failed batch when one write comes back empty', async () => {
    let n = 0;
    API.put.mockImplementation(() => {
      n += 1;
      return n === 2 ? Promise.resolve(undefined) : putOk();
    });
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' },
      ['alpha-1'],
    );
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('reports a thrown option write and re-enables the button', async () => {
    API.put.mockImplementation(() => Promise.reject(new Error('net')));
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' },
      ['alpha-1'],
    );
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    const btn = screen.getByText('应用更改');
    fireEvent.click(btn);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );
    await waitFor(() => expect(btn.disabled).toBe(false));
  });

  it.skip('should report malformed stored option JSON instead of hanging (DEFECT)', async () => {
    // SubmitData runs JSON.parse(props.options.*) *before* the try block but
    // *after* setLoading(true). A malformed stored option therefore throws out
    // of the click handler: no toast, and the `finally { setLoading(false) }`
    // never runs, so the 应用更改 button stays in its loading/disabled state
    // until the page is reloaded.
    await renderEditor(
      { ModelPrice: '{}', ModelRatio: '{"alpha-1":', CompletionRatio: '{}' },
      ['alpha-1'],
    );

    const btn = screen.getByText('应用更改');
    fireEvent.click(btn);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );
    expect(btn.disabled).toBe(false);
  });
});

// ─── Add-model modal ───────────────────────────────────────────────────────

describe('ModelRatioNotSetEditor add model', () => {
  const opts = { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' };

  it('prepends a manually added model and submits its ratio', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('添加模型'));
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'strawberry' },
    });
    fireEvent.change(screen.getByTestId('form-ratio'), {
      target: { value: '7' },
    });
    fireEvent.click(screen.getByTestId('ok-添加模型'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('添加成功'));
    expect(screen.getByTestId('row-strawberry')).toBeTruthy();

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ strawberry: 7 });
  });

  it('refuses a duplicate model name', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('添加模型'));
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'alpha-1' },
    });
    fireEvent.click(screen.getByTestId('ok-添加模型'));

    expect(showError).toHaveBeenCalledWith('模型名称已存在');
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('switches the add form to fixed-price fields when price mode is on', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('添加模型'));
    expect(screen.getByTestId('form-ratio')).toBeTruthy();

    fireEvent.click(screen.getByTestId('form-priceMode'));

    await waitFor(() => expect(screen.getByTestId('form-price')).toBeTruthy());
    expect(screen.queryByTestId('form-ratio')).toBeNull();
  });

  it('does nothing when the add modal is confirmed with an untouched form', async () => {
    await renderEditor(opts, ['alpha-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.click(screen.getByText('添加模型'));
    fireEvent.click(screen.getByTestId('ok-添加模型'));

    expect(showSuccess).not.toHaveBeenCalled();
    expect(showError).not.toHaveBeenCalled();
  });
});

// ─── Batch fill ────────────────────────────────────────────────────────────

describe('ModelRatioNotSetEditor batch fill', () => {
  const opts = { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' };

  const selectRows = (names) =>
    names.forEach((n) =>
      fireEvent.click(screen.getByTestId(`row-select-${n}`)),
    );

  it('keeps the batch button disabled until rows are selected', async () => {
    await renderEditor(opts, ['a', 'b']);
    await waitFor(() => screen.getByTestId('row-a'));

    const btn = screen.getByText(/批量设置/).closest('button');
    expect(btn.disabled).toBe(true);

    selectRows(['a']);
    await waitFor(() =>
      expect(screen.getByText(/批量设置/).closest('button').disabled).toBe(
        false,
      ),
    );
  });

  it('applies one ratio to every selected model', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(opts, ['a', 'b', 'c']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a', 'b']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.change(screen.getByTestId('form-batchFillValue'), {
      target: { value: '3' },
    });
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    await waitFor(() => expect(Notification.success).toHaveBeenCalled());
    expect(Notification.success.mock.calls[0][0].content).toBe(
      '已为 2 个模型设置模型倍率',
    );

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ a: 3, b: 3 });
  });

  it('fills model ratio and completion ratio together in bothRatio mode', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.click(screen.getByTestId('radio-模型倍率和补全倍率同时设置'));
    fireEvent.change(screen.getByTestId('form-batchRatioValue'), {
      target: { value: '2' },
    });
    fireEvent.change(screen.getByTestId('form-batchCompletionRatioValue'), {
      target: { value: '5' },
    });
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    await waitFor(() => expect(Notification.success).toHaveBeenCalled());

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ a: 2 });
    expect(putPayload('CompletionRatio')).toEqual({ a: 5 });
  });

  it('fills a fixed price and clears both ratios for the selection', async () => {
    API.put.mockImplementation(putOk);
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.click(screen.getByTestId('radio-固定价格'));
    fireEvent.change(screen.getByTestId('form-batchFillValue'), {
      target: { value: '0.9' },
    });
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    await waitFor(() => expect(Notification.success).toHaveBeenCalled());
    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelPrice')).toEqual({ a: 0.9 });
    expect(putPayload('ModelRatio')).toEqual({});
  });

  it('refuses an empty batch value', async () => {
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    expect(showError).toHaveBeenCalledWith('请输入填充值');
    expect(Notification.success).not.toHaveBeenCalled();
  });

  it('refuses a non-numeric batch value', async () => {
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.change(screen.getByTestId('form-batchFillValue'), {
      target: { value: 'zzz' },
    });
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    expect(showError).toHaveBeenCalledWith('请输入有效的数字');
    expect(Notification.success).not.toHaveBeenCalled();
  });

  it('refuses bothRatio mode when only one of the two values is filled', async () => {
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.click(screen.getByTestId('radio-模型倍率和补全倍率同时设置'));
    fireEvent.change(screen.getByTestId('form-batchRatioValue'), {
      target: { value: '2' },
    });
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    expect(showError).toHaveBeenCalledWith('请输入模型倍率和补全倍率');
  });

  it('clears the pending value when the batch type is switched', async () => {
    await renderEditor(opts, ['a']);
    await waitFor(() => screen.getByTestId('row-a'));

    selectRows(['a']);
    fireEvent.click(screen.getByText(/批量设置/));
    fireEvent.change(screen.getByTestId('form-batchFillValue'), {
      target: { value: '3' },
    });
    fireEvent.click(screen.getByTestId('radio-补全倍率'));
    // switching away from bothRatio resets batchFillValue, so confirming now
    // must be refused rather than silently reusing the stale 3
    fireEvent.click(screen.getByTestId('ok-批量设置模型参数'));

    expect(showError).toHaveBeenCalledWith('请输入填充值');
  });
});

// ─── Search + pagination ───────────────────────────────────────────────────

describe('ModelRatioNotSetEditor search and pagination', () => {
  const opts = { ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' };
  const many = Array.from(
    { length: 25 },
    (_, i) => `m-${String(i).padStart(2, '0')}`,
  );

  it('filters rows by substring and resets the total', async () => {
    await renderEditor(opts, ['alpha-1', 'alpha-2', 'beta-1']);
    await waitFor(() => screen.getByTestId('row-alpha-1'));

    fireEvent.change(screen.getByPlaceholderText('搜索模型名称'), {
      target: { value: 'beta' },
    });

    await waitFor(() =>
      expect(screen.getByTestId('pg-total').textContent).toBe('1'),
    );
    expect(screen.getByTestId('row-beta-1')).toBeTruthy();
    expect(screen.queryByTestId('row-alpha-1')).toBeNull();
  });

  it('pages 25 models ten at a time', async () => {
    await renderEditor(opts, many);
    await waitFor(() => screen.getByTestId('row-m-00'));

    expect(screen.getByTestId('pg-total').textContent).toBe('25');
    expect(screen.queryByTestId('row-m-10')).toBeNull();

    fireEvent.click(screen.getByTestId('pg-next'));
    await waitFor(() => expect(screen.getByTestId('row-m-10')).toBeTruthy());
    expect(screen.queryByTestId('row-m-00')).toBeNull();
  });

  it('clamps the current page when the page size grows past it', async () => {
    await renderEditor(opts, many);
    await waitFor(() => screen.getByTestId('row-m-00'));

    fireEvent.click(screen.getByTestId('pg-next'));
    fireEvent.click(screen.getByTestId('pg-next'));
    await waitFor(() =>
      expect(screen.getByTestId('pg-current').textContent).toBe('3'),
    );

    // 25 rows at 20/page is only 2 pages — page 3 must collapse to 2
    fireEvent.click(screen.getByTestId('pg-size-20'));
    await waitFor(() =>
      expect(screen.getByTestId('pg-current').textContent).toBe('2'),
    );
    expect(screen.getByTestId('row-m-20')).toBeTruthy();
  });
});
