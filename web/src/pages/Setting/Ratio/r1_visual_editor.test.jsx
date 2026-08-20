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

// Semi-UI is stubbed for the same reason as the Flows page test: its barrel
// drags in semi-illustrations -> lottie-web -> canvas, which jsdom lacks.
// The stubs preserve the contracts this editor relies on — Table runs every
// column render(), RadioGroup hands back {target:{value}}, Form exposes a
// setValues-shaped form api through getFormApi.
vi.mock('@douyinfe/semi-ui', () => {
  const cellKey = (col, idx) => String(col.key ?? col.dataIndex ?? idx);

  const Table = ({ columns = [], dataSource = [], pagination }) =>
    React.createElement(
      'div',
      { 'data-testid': 'table' },
      dataSource.map((record, rowIdx) =>
        React.createElement(
          'div',
          { key: record.name ?? rowIdx, 'data-testid': `row-${record.name}` },
          columns.map((col, i) =>
            React.createElement(
              'span',
              {
                key: i,
                'data-testid': `cell-${record.name}-${cellKey(col, i)}`,
              },
              col.render
                ? col.render(record[col.dataIndex], record, rowIdx)
                : record[col.dataIndex],
            ),
          ),
        ),
      ),
      pagination
        ? React.createElement(
            'div',
            null,
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
            React.createElement(
              'button',
              {
                'data-testid': 'pg-next',
                onClick: () =>
                  pagination.onPageChange(pagination.currentPage + 1),
              },
              'next',
            ),
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
    showClear: _s,
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
          { 'data-testid': 'modal' },
          React.createElement('span', { 'data-testid': 'modal-title' }, title),
          children,
          React.createElement(
            'button',
            { 'data-testid': 'modal-ok', onClick: onOk },
            'ok',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'modal-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null;

  const Form = ({ children, getFormApi }) => {
    if (getFormApi) getFormApi({ setValues: () => {} });
    return React.createElement('div', null, children);
  };
  Form.Input = ({ field, onChange, placeholder, disabled }) =>
    React.createElement('input', {
      type: 'text',
      'data-testid': `form-${field}`,
      placeholder,
      disabled: !!disabled,
      onChange: (e) => onChange && onChange(e.target.value),
    });
  Form.Section = ({ text, children }) =>
    React.createElement('div', null, text, children);

  const Space = ({ children }) => React.createElement('div', null, children);

  // Semi's RadioGroup emits {target:{value}} taken from the chosen Radio child.
  const RadioGroup = ({ value, onChange, children }) =>
    React.createElement(
      'div',
      { 'data-testid': `radiogroup-${value}` },
      React.Children.map(children, (child) =>
        React.createElement(
          'button',
          {
            'data-testid': `radio-${child.props.value}`,
            onClick: () =>
              onChange && onChange({ target: { value: child.props.value } }),
          },
          child.props.children,
        ),
      ),
    );
  const Radio = ({ children }) => React.createElement('span', null, children);

  const Checkbox = ({ checked, onChange, children }) =>
    React.createElement(
      'label',
      null,
      React.createElement('input', {
        type: 'checkbox',
        'data-testid': `checkbox-${String(children)}`,
        checked: !!checked,
        onChange: (e) =>
          onChange && onChange({ target: { checked: e.target.checked } }),
      }),
      children,
    );

  const Tag = ({ children }) =>
    React.createElement('span', { 'data-testid': 'tag' }, children);

  return {
    Table,
    Button,
    Input,
    Modal,
    Form,
    Space,
    RadioGroup,
    Radio,
    Checkbox,
    Tag,
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconDelete: () => null,
  IconPlus: () => null,
  IconSearch: () => null,
  IconSave: () => null,
  IconEdit: () => null,
}));

vi.mock('../../../helpers', () => ({
  API: { put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getQuotaPerUnit: vi.fn(() => 500000),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import ModelSettingsVisualEditor from './ModelSettingsVisualEditor';
import { API, showError, showSuccess, getQuotaPerUnit } from '../../../helpers';

const putOk = () => Promise.resolve({ data: { success: true } });

const putPayload = (key) => {
  const call = API.put.mock.calls.find((c) => c[1].key === key);
  if (!call) return undefined;
  return JSON.parse(call[1].value);
};

const mount = (options, refresh = vi.fn()) => {
  const utils = render(
    <ModelSettingsVisualEditor options={options} refresh={refresh} />,
  );
  return { ...utils, refresh };
};

const cellInput = (model, col) =>
  within(screen.getByTestId(`cell-${model}-${col}`)).getByRole('textbox');

beforeEach(() => {
  API.put.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  getQuotaPerUnit.mockClear();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

// ─── Building the table from the three stored JSON blobs ───────────────────

describe('ModelSettingsVisualEditor table build', () => {
  it('unions the three ratio maps into one row per model', () => {
    mount({
      ModelPrice: '{"fixed-a":0.02}',
      ModelRatio: '{"ratio-b":2}',
      CompletionRatio: '{"comp-c":3}',
    });

    expect(screen.getByTestId('row-fixed-a')).toBeTruthy();
    expect(screen.getByTestId('row-ratio-b')).toBeTruthy();
    expect(screen.getByTestId('row-comp-c')).toBeTruthy();
    expect(screen.getByTestId('pg-total').textContent).toBe('3');
  });

  it('keeps a stored ratio of 0 visible instead of blanking it', () => {
    // A free model is legitimately ratio 0; the row builder must not confuse
    // it with "unset", otherwise the operator cannot see what is configured.
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"free":0}',
      CompletionRatio: '{}',
    });

    expect(cellInput('free', 'ratio').value).toBe('0');
  });

  it('marks a model that carries both a price and a ratio as conflicting', () => {
    mount({
      ModelPrice: '{"both":0.01}',
      ModelRatio: '{"both":2}',
      CompletionRatio: '{}',
    });

    const nameCell = screen.getByTestId('cell-both-name');
    expect(within(nameCell).getByTestId('tag')).toHaveTextContent('矛盾');
  });

  it('does not mark a price-only or ratio-only model as conflicting', () => {
    mount({
      ModelPrice: '{"p":0.01}',
      ModelRatio: '{"r":2}',
      CompletionRatio: '{}',
    });

    expect(
      within(screen.getByTestId('cell-p-name')).queryByTestId('tag'),
    ).toBeNull();
    expect(
      within(screen.getByTestId('cell-r-name')).queryByTestId('tag'),
    ).toBeNull();
  });

  it('currently renders an empty table, silently, when the stored ratio JSON is malformed', () => {
    // DEFECT: ModelSettingsVisualEditor.jsx:88-90 catches the parse failure with
    // only a console.error — no showError. An operator whose stored ratio JSON
    // is corrupt sees an ordinary empty table and no indication anything is
    // wrong, and from there the empty model list feeds straight into the
    // clear-everything PUT path. Same shape as the parse handling flagged in
    // SubmitData/applySync, which sits outside its own try.
    mount({ ModelPrice: '{}', ModelRatio: '{"broken"', CompletionRatio: '{}' });

    expect(screen.getByTestId('pg-total').textContent).toBe('0');
    expect(screen.queryByTestId('row-broken')).toBeNull();
    // Pin the silence itself, so a future showError shows up as a deliberate
    // change here rather than passing unnoticed.
    expect(showError).not.toHaveBeenCalled();
  });

  // Paired contract lock for the defect above. Un-skip with the fix.
  it.skip('should tell the operator when the stored ratio JSON cannot be parsed', () => {
    mount({ ModelPrice: '{}', ModelRatio: '{"broken"', CompletionRatio: '{}' });

    expect(showError).toHaveBeenCalled();
  });

  it('disables the ratio cells for a fixed-price model', () => {
    mount({
      ModelPrice: '{"p":0.01}',
      ModelRatio: '{}',
      CompletionRatio: '{}',
    });

    expect(cellInput('p', 'ratio').disabled).toBe(true);
    expect(cellInput('p', 'completionRatio').disabled).toBe(true);
  });
});

// ─── Filtering ─────────────────────────────────────────────────────────────

describe('ModelSettingsVisualEditor filtering', () => {
  const opts = {
    ModelPrice: '{"both":0.01}',
    ModelRatio: '{"both":2,"alpha-1":3}',
    CompletionRatio: '{}',
  };

  it('filters by model-name substring', () => {
    mount(opts);
    fireEvent.change(screen.getByPlaceholderText('搜索模型名称'), {
      target: { value: 'alpha' },
    });

    expect(screen.getByTestId('row-alpha-1')).toBeTruthy();
    expect(screen.queryByTestId('row-both')).toBeNull();
  });

  it('narrows the table to conflicting rows only', () => {
    mount(opts);
    fireEvent.click(screen.getByTestId('checkbox-仅显示矛盾倍率'));

    expect(screen.getByTestId('row-both')).toBeTruthy();
    expect(screen.queryByTestId('row-alpha-1')).toBeNull();
    expect(screen.getByTestId('pg-total').textContent).toBe('1');
  });

  it('combines the keyword and the conflict filter', () => {
    mount(opts);
    fireEvent.click(screen.getByTestId('checkbox-仅显示矛盾倍率'));
    fireEvent.change(screen.getByPlaceholderText('搜索模型名称'), {
      target: { value: 'alpha' },
    });

    // alpha-1 matches the keyword but is not conflicting -> nothing left
    expect(screen.getByTestId('pg-total').textContent).toBe('0');
  });

  it('pages ten models at a time', () => {
    const ratio = {};
    for (let i = 0; i < 12; i += 1) ratio[`m${String(i).padStart(2, '0')}`] = 1;
    mount({
      ModelPrice: '{}',
      ModelRatio: JSON.stringify(ratio),
      CompletionRatio: '{}',
    });

    expect(screen.getByTestId('pg-total').textContent).toBe('12');
    expect(screen.queryByTestId('row-m10')).toBeNull();

    fireEvent.click(screen.getByTestId('pg-next'));
    expect(screen.getByTestId('row-m10')).toBeTruthy();
    expect(screen.queryByTestId('row-m00')).toBeNull();
  });
});

// ─── Inline cell edits ─────────────────────────────────────────────────────

describe('ModelSettingsVisualEditor inline editing', () => {
  it('rejects a non-numeric ratio and leaves the cell untouched', () => {
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"alpha-1":2}',
      CompletionRatio: '{}',
    });

    fireEvent.change(cellInput('alpha-1', 'ratio'), {
      target: { value: 'abc' },
    });

    expect(showError).toHaveBeenCalledWith('请输入数字');
    expect(cellInput('alpha-1', 'ratio').value).toBe('2');
  });

  it('recomputes the conflict flag as soon as a price is typed in', () => {
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"alpha-1":2}',
      CompletionRatio: '{}',
    });
    expect(
      within(screen.getByTestId('cell-alpha-1-name')).queryByTestId('tag'),
    ).toBeNull();

    fireEvent.change(cellInput('alpha-1', 'price'), {
      target: { value: '0.5' },
    });

    expect(
      within(screen.getByTestId('cell-alpha-1-name')).getByTestId('tag'),
    ).toHaveTextContent('矛盾');
  });

  it('drops the conflict flag again when the price is cleared', () => {
    mount({
      ModelPrice: '{"both":0.01}',
      ModelRatio: '{"both":2}',
      CompletionRatio: '{}',
    });

    fireEvent.change(cellInput('both', 'price'), { target: { value: '' } });

    expect(
      within(screen.getByTestId('cell-both-name')).queryByTestId('tag'),
    ).toBeNull();
  });

  it('removes a row and leaves it out of the next save', async () => {
    API.put.mockImplementation(putOk);
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"keep":1,"drop":2}',
      CompletionRatio: '{}',
    });

    const deleteBtn = within(
      screen.getByTestId('cell-drop-action'),
    ).getAllByRole('button')[1];
    fireEvent.click(deleteBtn);
    expect(screen.queryByTestId('row-drop')).toBeNull();

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ keep: 1 });
  });
});

// ─── Save ──────────────────────────────────────────────────────────────────

describe('ModelSettingsVisualEditor save', () => {
  it('splits the table back into price-only and ratio-only maps', async () => {
    API.put.mockImplementation(putOk);
    const { refresh } = mount({
      ModelPrice: '{"fixed":0.02}',
      ModelRatio: '{"tok":2}',
      CompletionRatio: '{"tok":3}',
    });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(putPayload('ModelPrice')).toEqual({ fixed: 0.02 });
    expect(putPayload('ModelRatio')).toEqual({ tok: 2 });
    expect(putPayload('CompletionRatio')).toEqual({ tok: 3 });
    expect(refresh).toHaveBeenCalled();
  });

  it('lets a fixed price suppress that model ratio entries', async () => {
    API.put.mockImplementation(putOk);
    mount({
      ModelPrice: '{"both":0.01}',
      ModelRatio: '{"both":2}',
      CompletionRatio: '{"both":3}',
    });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalled());
    expect(putPayload('ModelPrice')).toEqual({ both: 0.01 });
    expect(putPayload('ModelRatio')).toEqual({});
    expect(putPayload('CompletionRatio')).toEqual({});
  });

  it('surfaces a rejected write and withholds the success toast', async () => {
    API.put.mockImplementation(() =>
      Promise.resolve({ data: { success: false, message: 'nope' } }),
    );
    const { refresh } = mount({
      ModelPrice: '{}',
      ModelRatio: '{"a":1}',
      CompletionRatio: '{}',
    });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('nope'));
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('flags a partial batch failure when one write returns nothing', async () => {
    let n = 0;
    API.put.mockImplementation(() => {
      n += 1;
      return n === 3 ? Promise.resolve(undefined) : putOk();
    });
    const { refresh } = mount({
      ModelPrice: '{}',
      ModelRatio: '{"a":1}',
      CompletionRatio: '{}',
    });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    // the two writes that already went out are not rolled back
    expect(API.put).toHaveBeenCalledTimes(3);
    expect(refresh).not.toHaveBeenCalled();
  });

  it('surfaces a thrown write', async () => {
    API.put.mockImplementation(() => Promise.reject(new Error('net')));
    mount({ ModelPrice: '{}', ModelRatio: '{"a":1}', CompletionRatio: '{}' });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it.skip('should not overwrite stored pricing with an empty table (DEFECT)', async () => {
    // RatioSetting mounts this editor with inputs = {ModelRatio:'', ...} and
    // only fills them once GET /api/option/ resolves. If that request fails,
    // or the operator clicks before it lands, `models` is [] and SubmitData
    // still PUTs `{}` for ModelPrice/ModelRatio/CompletionRatio — every model
    // ratio on the platform is erased in one click, with no confirmation and
    // no way back. Unlike ModelRationNotSetEditor, `output` here starts empty
    // instead of being seeded from props.options.
    // Un-skip once the editor refuses to save a table it never loaded.
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '', ModelRatio: '', CompletionRatio: '' });

    fireEvent.click(screen.getByText('应用更改'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(API.put).not.toHaveBeenCalled();
  });
});

// ─── Add / edit modal ──────────────────────────────────────────────────────

describe('ModelSettingsVisualEditor add and edit modal', () => {
  const openAdd = () => fireEvent.click(screen.getByText('添加模型'));

  it('adds a ratio-priced model from the modal', async () => {
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    expect(screen.getByTestId('modal-title')).toHaveTextContent('添加模型');
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'strawberry' },
    });
    fireEvent.change(screen.getByTestId('form-ratioInput'), {
      target: { value: '4' },
    });
    fireEvent.change(screen.getByTestId('form-completionRatioInput'), {
      target: { value: '6' },
    });
    fireEvent.click(screen.getByTestId('modal-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('添加成功'));
    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ strawberry: 4 });
    expect(putPayload('CompletionRatio')).toEqual({ strawberry: 6 });
  });

  it('clears the ratios when the model is saved as per-request', async () => {
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'once' },
    });
    fireEvent.change(screen.getByTestId('form-ratioInput'), {
      target: { value: '4' },
    });
    fireEvent.click(screen.getByTestId('radio-per-request'));
    fireEvent.change(screen.getByTestId('form-priceInput'), {
      target: { value: '0.3' },
    });
    fireEvent.click(screen.getByTestId('modal-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('添加成功'));
    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelPrice')).toEqual({ once: 0.3 });
    expect(putPayload('ModelRatio')).toEqual({});
  });

  it('converts $/1M token prices into ratios on save', async () => {
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'priced' },
    });
    fireEvent.click(screen.getByTestId('radio-token-price'));
    fireEvent.change(screen.getByTestId('form-modelTokenPrice'), {
      target: { value: '4' },
    });
    fireEvent.change(screen.getByTestId('form-completionTokenPrice'), {
      target: { value: '12' },
    });
    fireEvent.click(screen.getByTestId('modal-ok'));

    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('添加成功'));
    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // input 4 $/1M -> ratio 2 ; output 12 $/1M over input 4 -> completion 3
    expect(putPayload('ModelRatio')).toEqual({ priced: 2 });
    expect(putPayload('CompletionRatio')).toEqual({ priced: 3 });
  });

  it('does not derive a completion ratio before an input price exists', async () => {
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'noinput' },
    });
    fireEvent.click(screen.getByTestId('radio-token-price'));
    fireEvent.change(screen.getByTestId('form-completionTokenPrice'), {
      target: { value: '12' },
    });
    fireEvent.click(screen.getByTestId('modal-ok'));

    expect(showSuccess).toHaveBeenCalledWith('添加成功');
    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // What this test owns: an output price with no input price must not produce
    // a divide-by-nothing completion ratio.
    //
    // Asserted as "no derived ratio for this model", NOT as "the payload is
    // exactly {}". The empty-object form only holds because `values.completionRatio || ''`
    // coerces the 0 sentinel to '' — which is itself the falsy-zero defect the
    // skipped test further down locks the fix for. Pinning `toEqual({})` would
    // make that fix surface as a regression here and block it.
    expect(putPayload('CompletionRatio').noinput).toBeFalsy();
    expect(putPayload('ModelRatio')).toEqual({});
  });

  it('opens in edit mode with the name locked and round-trips the ratio', async () => {
    API.put.mockImplementation(putOk);
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"alpha-1":2}',
      CompletionRatio: '{"alpha-1":3}',
    });

    const editBtn = within(
      screen.getByTestId('cell-alpha-1-action'),
    ).getAllByRole('button')[0];
    fireEvent.click(editBtn);

    expect(screen.getByTestId('modal-title')).toHaveTextContent('编辑模型');
    expect(screen.getByTestId('form-name').disabled).toBe(true);

    fireEvent.click(screen.getByTestId('modal-ok'));
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('更新成功'));

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ 'alpha-1': 2 });
    expect(putPayload('CompletionRatio')).toEqual({ 'alpha-1': 3 });
  });

  it('opens a fixed-price model in per-request mode', () => {
    mount({
      ModelPrice: '{"fixed":0.05}',
      ModelRatio: '{}',
      CompletionRatio: '{}',
    });

    fireEvent.click(
      within(screen.getByTestId('cell-fixed-action')).getAllByRole('button')[0],
    );

    expect(screen.getByTestId('form-priceInput')).toBeTruthy();
    expect(screen.queryByTestId('form-ratioInput')).toBeNull();
  });

  it('discards the draft when the modal is cancelled', () => {
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'ghost' },
    });
    fireEvent.click(screen.getByTestId('modal-cancel'));

    expect(screen.queryByTestId('modal')).toBeNull();
    expect(screen.queryByTestId('row-ghost')).toBeNull();
  });

  it('ignores confirm on an untouched add form', () => {
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    openAdd();
    fireEvent.click(screen.getByTestId('modal-ok'));

    expect(showSuccess).not.toHaveBeenCalled();
    expect(screen.getByTestId('pg-total').textContent).toBe('0');
  });

  it.skip('should preserve a ratio of 0 through the edit modal (DEFECT)', async () => {
    // addOrUpdateModel normalises with `values.ratio || ''`. 0 is falsy, so a
    // free model (ratio 0) that is merely opened and confirmed in the edit
    // modal comes back with ratio '' and is dropped from ModelRatio entirely
    // on the next save — the model silently reverts to the default ratio and
    // starts billing. Same coercion hits price 0 and completionRatio 0.
    // Un-skip once the normalisation uses an undefined check.
    API.put.mockImplementation(putOk);
    mount({
      ModelPrice: '{}',
      ModelRatio: '{"free":0}',
      CompletionRatio: '{}',
    });

    fireEvent.click(
      within(screen.getByTestId('cell-free-action')).getAllByRole('button')[0],
    );
    fireEvent.click(screen.getByTestId('modal-ok'));
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('更新成功'));

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({ free: 0 });
  });

  it.skip('should reject a non-numeric token price instead of storing null (DEFECT)', async () => {
    // handleTokenPriceChange only guards with isNaN(value), and the modal's
    // onOk unconditionally does (parseFloat(tokenPrice)/2).toString(). A
    // whitespace entry yields the string 'NaN', SubmitData parseFloat's it back
    // to NaN, and JSON.stringify serialises NaN as literal null — the model
    // ends up with {"m": null} in ModelRatio.
    // Un-skip once the modal validates the token price.
    API.put.mockImplementation(putOk);
    mount({ ModelPrice: '{}', ModelRatio: '{}', CompletionRatio: '{}' });

    fireEvent.click(screen.getByText('添加模型'));
    fireEvent.change(screen.getByTestId('form-name'), {
      target: { value: 'm' },
    });
    fireEvent.click(screen.getByTestId('radio-token-price'));
    fireEvent.change(screen.getByTestId('form-modelTokenPrice'), {
      target: { value: ' ' },
    });
    fireEvent.click(screen.getByTestId('modal-ok'));

    fireEvent.click(screen.getByText('应用更改'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putPayload('ModelRatio')).toEqual({});
  });
});
