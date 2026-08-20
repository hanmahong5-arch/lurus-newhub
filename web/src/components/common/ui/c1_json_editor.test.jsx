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
import React, { useState } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

// Mock react-i18next — t() returns the key, so every assertion below reads as
// the literal copy the component asks for.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key) => key,
    i18n: { language: 'zh' },
  }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// Semi UI cannot be imported under jsdom (its illustration bundle reaches into
// canvas). Shim only the primitives JSONEditor uses, keeping the *contract* of
// each: Semi's Input/TextArea hand the caller a raw value (not an event), and
// InputNumber hands a number. Everything asserted below is JSONEditor's own
// parse / merge / emit logic, which is what actually ships.
vi.mock('@douyinfe/semi-icons', () => ({
  IconPlus: () => React.createElement('span', null, 'icon-plus'),
  IconDelete: () => React.createElement('span', null, 'icon-delete'),
  IconAlertTriangle: () => React.createElement('span', null, 'icon-warn'),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;

  const Form = ({ children }) => h('div', null, children);
  Form.Slot = ({ label, children }) =>
    h('div', null, label ? h('div', null, label) : null, children);
  Form.Input = ({ field, value }) =>
    h('input', {
      type: 'hidden',
      'data-testid': `form-field-${field ?? 'unnamed'}`,
      value: value ?? '',
      readOnly: true,
    });

  return {
    Form,
    Card: ({ header, children }) => h('div', null, header, children),
    Tabs: ({ activeKey, onChange, children }) =>
      h(
        'div',
        { 'data-testid': 'tabs', 'data-active-tab': activeKey },
        React.Children.map(children, (child) =>
          h(
            'button',
            {
              type: 'button',
              'data-testid': `tab-${child.props.itemKey}`,
              onClick: () => onChange?.(child.props.itemKey),
            },
            child.props.tab,
          ),
        ),
      ),
    TabPane: () => null,
    Banner: ({ type, description }) =>
      h('div', { role: 'alert', 'data-banner-type': type }, description),
    Button: ({ onClick, icon, children }) =>
      h('button', { type: 'button', onClick }, icon, children),
    Input: ({ value, onChange, placeholder, status }) =>
      h('input', {
        value: value ?? '',
        placeholder,
        'data-status': status ?? '',
        onChange: (e) => onChange?.(e.target.value),
      }),
    InputNumber: ({ value, onChange, placeholder }) =>
      h('input', {
        type: 'number',
        value: value ?? '',
        placeholder,
        onChange: (e) =>
          onChange?.(e.target.value === '' ? '' : Number(e.target.value)),
      }),
    Switch: ({ checked, onChange }) =>
      h('input', {
        type: 'checkbox',
        checked: !!checked,
        onChange: (e) => onChange?.(e.target.checked),
      }),
    TextArea: ({ value, onChange, placeholder, rows }) =>
      h('textarea', {
        value: value ?? '',
        placeholder,
        rows,
        onChange: (e) => onChange?.(e.target.value),
      }),
    Row: ({ children }) => h('div', { 'data-testid': 'kv-row' }, children),
    Col: ({ children }) => h('div', null, children),
    Divider: ({ children }) => h('div', null, children),
    Tooltip: ({ content, children }) =>
      h(
        'span',
        { 'data-tooltip': typeof content === 'string' ? content : '' },
        children,
      ),
    Typography: { Text: ({ children }) => h('span', null, children) },
  };
});

import JSONEditor from './JSONEditor';

// A parent that actually stores what JSONEditor emits — this is how every real
// call site (EditChannelModal / EditModelModal) wires it up.
const Controlled = ({ initial = '', onEmit, ...rest }) => {
  const [v, setV] = useState(initial);
  return (
    <JSONEditor
      value={v}
      onChange={(next) => {
        setV(next);
        onEmit?.(next);
      }}
      {...rest}
    />
  );
};

const keyInputs = () => screen.getAllByPlaceholderText('键名');
const valueInputs = () => screen.getAllByPlaceholderText('参数值');
const addButton = () => screen.getByRole('button', { name: /添加键值对/ });
const deleteButtons = () =>
  screen.getAllByRole('button', { name: 'icon-delete' });
const manualArea = () => screen.queryByRole('textbox');
const banners = () => screen.queryAllByRole('alert');

let logSpy;
beforeEach(() => {
  // JSONEditor console.logs every parse failure; keep the run readable.
  logSpy = vi.spyOn(console, 'log').mockImplementation(() => {});
});
afterEach(() => {
  logSpy.mockRestore();
});

describe('JSONEditor — initial mode selection', () => {
  it('starts in visual mode and lists one row per key of a small object', () => {
    render(<JSONEditor value='{"alpha":"1","beta":"2"}' field='m' />);

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'visual',
    );
    expect(keyInputs().map((el) => el.value)).toEqual(['alpha', 'beta']);
    expect(valueInputs().map((el) => el.value)).toEqual(['1', '2']);
  });

  it('accepts an object (not just a JSON string) as value', () => {
    render(<JSONEditor value={{ region: 'us-central1' }} field='m' />);

    expect(keyInputs()).toHaveLength(1);
    expect(keyInputs()[0].value).toBe('region');
  });

  it('switches to manual mode when the object has more than 10 keys', () => {
    const big = {};
    for (let i = 0; i < 11; i += 1) big[`k${i}`] = String(i);
    render(<JSONEditor value={JSON.stringify(big)} field='m' />);

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'manual',
    );
    expect(manualArea()).not.toBeNull();
    expect(screen.queryAllByPlaceholderText('键名')).toHaveLength(0);
  });

  it('stays visual at exactly 10 keys — the boundary is > 10, not >= 10', () => {
    const ten = {};
    for (let i = 0; i < 10; i += 1) ten[`k${i}`] = String(i);
    render(<JSONEditor value={JSON.stringify(ten)} field='m' />);

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'visual',
    );
    expect(keyInputs()).toHaveLength(10);
  });

  it('falls back to manual mode and surfaces the parse error for broken JSON', () => {
    render(<JSONEditor value='{"a": ' field='m' />);

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'manual',
    );
    const alert = banners();
    expect(alert).toHaveLength(1);
    expect(alert[0]).toHaveAttribute('data-banner-type', 'danger');
    expect(alert[0].textContent).toMatch(/^JSON 格式错误: .+/);
    // The broken text is preserved verbatim so the user can repair it.
    expect(manualArea().value).toBe('{"a": ');
  });

  it('shows the empty-state hint when there is nothing to edit', () => {
    render(<JSONEditor value='' field='m' />);

    expect(
      screen.getByText('暂无数据，点击下方按钮添加键值对'),
    ).toBeInTheDocument();
    expect(banners()).toHaveLength(0);
  });
});

describe('JSONEditor — visual editing emits JSON', () => {
  it('adds pairs with auto-generated non-colliding keys', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='' onChange={onChange} field='m' />);

    fireEvent.click(addButton());
    expect(onChange).toHaveBeenLastCalledWith('{\n  "field_1": ""\n}');

    fireEvent.click(addButton());
    expect(onChange).toHaveBeenLastCalledWith(
      '{\n  "field_1": "",\n  "field_2": ""\n}',
    );
  });

  it('renaming a key re-emits the object under the new name', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"old":"v"}' onChange={onChange} field='m' />);

    fireEvent.change(keyInputs()[0], { target: { value: 'renamed' } });

    expect(onChange).toHaveBeenLastCalledWith('{\n  "renamed": "v"\n}');
  });

  it('keeps a non-numeric value as a string', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"region":""}' onChange={onChange} field='m' />);

    fireEvent.change(valueInputs()[0], { target: { value: 'us-central1' } });

    expect(onChange).toHaveBeenLastCalledWith(
      '{\n  "region": "us-central1"\n}',
    );
  });

  it('emits an empty string — not "{}" — once the last pair is deleted', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"only":"1"}' onChange={onChange} field='m' />);

    fireEvent.click(deleteButtons()[0]);

    // Callers store this straight into a form field; "" means "unset" while
    // "{}" would persist an empty object.
    expect(onChange).toHaveBeenLastCalledWith('');
  });

  it('drops pairs whose key was blanked out', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor value='{"a":"1","b":"2"}' onChange={onChange} field='m' />,
    );

    fireEvent.change(keyInputs()[0], { target: { value: '' } });

    // The row is still on screen (so the user can retype) but the emitted
    // object no longer carries it.
    expect(onChange).toHaveBeenLastCalledWith('{\n  "b": "2"\n}');
    expect(keyInputs()).toHaveLength(2);
  });

  it('pushes the same JSON through formApi.setValue when a field is bound', () => {
    const formApi = { setValue: vi.fn() };
    render(
      <JSONEditor value='{"a":"1"}' field='model_mapping' formApi={formApi} />,
    );

    fireEvent.change(valueInputs()[0], { target: { value: 'x' } });

    expect(formApi.setValue).toHaveBeenCalledWith(
      'model_mapping',
      '{\n  "a": "x"\n}',
    );
  });

  it('does not touch formApi when no field name was supplied', () => {
    const formApi = { setValue: vi.fn() };
    render(<JSONEditor value='{"a":"1"}' formApi={formApi} />);

    fireEvent.change(valueInputs()[0], { target: { value: 'x' } });

    expect(formApi.setValue).not.toHaveBeenCalled();
  });
});

describe('JSONEditor — duplicate keys', () => {
  it('warns about duplicates and keeps only the last value', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor value='{"a":"1","b":"2"}' onChange={onChange} field='m' />,
    );

    fireEvent.change(keyInputs()[1], { target: { value: 'a' } });

    const warn = banners();
    expect(warn).toHaveLength(1);
    expect(warn[0]).toHaveAttribute('data-banner-type', 'warning');
    expect(warn[0].textContent).toContain('存在重复的键名：');
    expect(warn[0].textContent).toContain(
      '注意：JSON中重复的键只会保留最后一个同名键的值',
    );
    // JSON.stringify of {a:'1'} then overwritten by {a:'2'} → last wins.
    expect(onChange).toHaveBeenLastCalledWith('{\n  "a": "2"\n}');
  });

  it('flags both duplicate rows, telling the winner from the loser', () => {
    render(<JSONEditor value='{"a":"1","b":"2"}' field='m' />);
    fireEvent.change(keyInputs()[1], { target: { value: 'a' } });

    expect(keyInputs().map((el) => el.getAttribute('data-status'))).toEqual([
      'warning',
      'warning',
    ]);
    const tips = screen
      .getAllByText('icon-warn')
      .map((el) => el.parentElement.getAttribute('data-tooltip'))
      .filter((v) => v);
    expect(tips).toContain('重复的键名，此值将被后面的同名键覆盖');
    expect(tips).toContain('这是重复键中的最后一个，其值将被使用');
  });

  it('clears the warning once the collision is resolved', () => {
    render(<JSONEditor value='{"a":"1","b":"2"}' field='m' />);
    fireEvent.change(keyInputs()[1], { target: { value: 'a' } });
    expect(banners()).toHaveLength(1);

    fireEvent.change(keyInputs()[1], { target: { value: 'c' } });
    expect(banners()).toHaveLength(0);
  });
});

describe('JSONEditor — typed value editors', () => {
  it('renders a switch for booleans and emits a real boolean on toggle', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor value='{"stream":false}' onChange={onChange} field='m' />,
    );

    const toggle = screen.getByRole('checkbox');
    expect(toggle.checked).toBe(false);
    expect(screen.getByText('false')).toBeInTheDocument();

    fireEvent.click(toggle);
    expect(onChange).toHaveBeenLastCalledWith('{\n  "stream": true\n}');
  });

  it('renders a number input for numbers and keeps the JSON number type', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"retries":3}' onChange={onChange} field='m' />);

    const num = screen.getByPlaceholderText('输入数字');
    expect(num.value).toBe('3');

    fireEvent.change(num, { target: { value: '7' } });
    expect(onChange).toHaveBeenLastCalledWith('{\n  "retries": 7\n}');
  });

  it('renders nested objects as pretty JSON in a textarea', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor
        value='{"openai":{"path":"/v1/chat"}}'
        onChange={onChange}
        field='m'
        editorType='object'
      />,
    );

    const nested = screen.getByPlaceholderText('输入JSON对象');
    expect(nested.value).toBe('{\n  "path": "/v1/chat"\n}');

    fireEvent.change(nested, {
      target: { value: '{"path":"/v1/responses"}' },
    });
    expect(onChange).toHaveBeenLastCalledWith(
      '{\n  "openai": {\n    "path": "/v1/responses"\n  }\n}',
    );
  });

  it('swallows a half-typed nested object instead of emitting garbage', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor
        value='{"openai":{"path":"/v1"}}'
        onChange={onChange}
        field='m'
      />,
    );

    fireEvent.change(screen.getByPlaceholderText('输入JSON对象'), {
      target: { value: '{"path":' },
    });

    expect(onChange).not.toHaveBeenCalled();
  });

  it('an emptied nested-object textarea collapses to an empty object', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor
        value='{"openai":{"path":"/v1"}}'
        onChange={onChange}
        field='m'
      />,
    );

    fireEvent.change(screen.getByPlaceholderText('输入JSON对象'), {
      target: { value: '   ' },
    });

    expect(onChange).toHaveBeenLastCalledWith('{\n  "openai": {}\n}');
  });

  // DEFECT (skipped): the string branch of renderValueInput coerces anything
  // Number() likes into a JSON number, so a user can never enter the *string*
  // "502". status_code_mapping is the live victim: the Go side unmarshals it
  // into map[string]string (internal/app/error.go ResetStatusCode), so
  // {"400": 502} fails to unmarshal and the whole mapping is silently dropped
  // at relay time. The template ships {"400":"500"} — retyping that value in
  // the default (visual) editor breaks it.
  it.skip('keeps a typed status code as a string, not a JSON number', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"400":"500"}' onChange={onChange} field='m' />);

    fireEvent.change(valueInputs()[0], { target: { value: '502' } });

    expect(onChange).toHaveBeenLastCalledWith('{\n  "400": "502"\n}');
  });

  // DEFECT (skipped): `!isNaN(newValue) && newValue !== ''` lets whitespace
  // through — Number('  ') is 0 and Number.isInteger(0) is true — so typing a
  // space into a value box rewrites it to the number 0.
  it.skip('does not turn a whitespace-only value into the number 0', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"a":"x"}' onChange={onChange} field='m' />);

    fireEvent.change(valueInputs()[0], { target: { value: '  ' } });

    expect(onChange).toHaveBeenLastCalledWith('{\n  "a": "  "\n}');
  });

  // DEFECT (skipped): the same coercion eats leading zeros, so zero-padded
  // identifiers ("007", "0755") silently lose them on their way to the server.
  it.skip('preserves leading zeros in a value', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"a":"x"}' onChange={onChange} field='m' />);

    fireEvent.change(valueInputs()[0], { target: { value: '007' } });

    expect(onChange).toHaveBeenLastCalledWith('{\n  "a": "007"\n}');
  });
});

describe('JSONEditor — manual mode', () => {
  it('emits the raw text when the manual JSON is valid', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"a": ' onChange={onChange} field='m' />);

    fireEvent.change(manualArea(), { target: { value: '{"a":1}' } });

    expect(onChange).toHaveBeenLastCalledWith('{"a":1}');
    expect(banners()).toHaveLength(0);
  });

  it('shows the parse error and withholds onChange while the JSON is broken', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"a": ' onChange={onChange} field='m' />);

    fireEvent.change(manualArea(), { target: { value: '{"a":' } });

    expect(banners()[0].textContent).toMatch(/^JSON 格式错误: /);
    // Invalid text must never reach the form value.
    expect(onChange).not.toHaveBeenCalled();
  });

  it('clearing the textarea emits "" and drops every pair', () => {
    const onChange = vi.fn();
    render(<JSONEditor value='{"a": ' onChange={onChange} field='m' />);

    fireEvent.change(manualArea(), { target: { value: '   ' } });

    expect(onChange).toHaveBeenLastCalledWith('');
    expect(banners()).toHaveLength(0);
  });

  it('refuses to leave manual mode while the JSON is unparseable', () => {
    render(<JSONEditor value='{"a": ' field='m' />);

    fireEvent.click(screen.getByTestId('tab-visual'));

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'manual',
    );
    expect(banners()[0].textContent).toMatch(/^JSON 格式错误: /);
  });

  it('switches back to visual once the JSON parses, carrying the pairs over', () => {
    render(<JSONEditor value='{"a": ' field='m' />);
    fireEvent.change(manualArea(), { target: { value: '{"x":"1","y":"2"}' } });

    fireEvent.click(screen.getByTestId('tab-visual'));

    expect(screen.getByTestId('tabs')).toHaveAttribute(
      'data-active-tab',
      'visual',
    );
    expect(keyInputs().map((el) => el.value)).toEqual(['x', 'y']);
  });

  it('carries visual edits into the textarea when the parent stores the value', () => {
    render(<Controlled initial='' field='m' />);
    fireEvent.click(addButton());

    fireEvent.click(screen.getByTestId('tab-manual'));

    expect(manualArea().value).toBe('{\n  "field_1": ""\n}');
  });

  // DEFECT (skipped): the visual→manual hop goes straight to
  // setEditMode('manual') instead of toggleEditMode(), so the manual buffer is
  // never rebuilt from the live key/value rows — it is only ever refreshed from
  // the `value` prop. With an uncontrolled parent (value left at ''), every
  // visual edit disappears the moment the user opens the manual tab. The
  // rebuild branch inside toggleEditMode (the editMode === 'visual' arm) is
  // dead code for exactly this reason.
  it.skip('carries visual edits into the textarea even when uncontrolled', () => {
    render(<JSONEditor value='' field='m' />);
    fireEvent.click(addButton());

    fireEvent.click(screen.getByTestId('tab-manual'));

    expect(manualArea().value).toBe('{\n  "field_1": ""\n}');
  });
});

describe('JSONEditor — template, region editor and slots', () => {
  it('fills the template and emits it on click', () => {
    const onChange = vi.fn();
    const formApi = { setValue: vi.fn() };
    render(
      <JSONEditor
        value=''
        onChange={onChange}
        field='other'
        formApi={formApi}
        template={{ default: 'global' }}
        templateLabel='填入模板'
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: '填入模板' }));

    expect(onChange).toHaveBeenLastCalledWith('{\n  "default": "global"\n}');
    expect(formApi.setValue).toHaveBeenCalledWith(
      'other',
      '{\n  "default": "global"\n}',
    );
    expect(keyInputs()[0].value).toBe('default');
  });

  it('hides the template button unless both template and label are given', () => {
    render(<JSONEditor value='' field='m' template={{ a: 1 }} />);

    expect(screen.queryByRole('button', { name: '填入模板' })).toBeNull();
  });

  it('region editor splits the default region from the per-model rows', () => {
    render(
      <JSONEditor
        value='{"default":"us-central1","claude-3":"europe-west1"}'
        field='other'
        editorType='region'
      />,
    );

    expect(screen.getByPlaceholderText('默认区域，如: us-central1').value).toBe(
      'us-central1',
    );
    const models = screen.getAllByPlaceholderText('模型名称');
    expect(models.map((el) => el.value)).toEqual(['claude-3']);
    expect(screen.getByPlaceholderText('区域').value).toBe('europe-west1');
  });

  it('region editor creates the default key when none exists yet', () => {
    const onChange = vi.fn();
    render(
      <JSONEditor
        value='{"claude-3":"europe-west1"}'
        onChange={onChange}
        field='other'
        editorType='region'
      />,
    );

    fireEvent.change(screen.getByPlaceholderText('默认区域，如: us-central1'), {
      target: { value: 'global' },
    });

    expect(onChange).toHaveBeenLastCalledWith(
      '{\n  "claude-3": "europe-west1",\n  "default": "global"\n}',
    );
  });

  it('renders the hidden form field, the label and the extra slots', () => {
    render(
      <JSONEditor
        value='{"a":"1"}'
        field='model_mapping'
        label='模型重定向'
        extraText='额外说明'
        extraFooter={<span>footer-slot</span>}
      />,
    );

    expect(screen.getByText('模型重定向')).toBeInTheDocument();
    expect(screen.getByText('额外说明')).toBeInTheDocument();
    expect(screen.getByText('footer-slot')).toBeInTheDocument();
    expect(screen.getByTestId('form-field-model_mapping')).toHaveValue(
      '{"a":"1"}',
    );
  });
});

describe('JSONEditor — external value changes', () => {
  it('re-syncs the rows when the parent swaps the value out', () => {
    const { rerender } = render(<JSONEditor value='{"a":"1"}' field='m' />);
    expect(keyInputs().map((el) => el.value)).toEqual(['a']);

    rerender(<JSONEditor value='{"b":"2","c":"3"}' field='m' />);

    expect(keyInputs().map((el) => el.value)).toEqual(['b', 'c']);
    expect(valueInputs().map((el) => el.value)).toEqual(['2', '3']);
  });

  it('surfaces an error banner when the parent pushes down broken JSON', () => {
    const { rerender } = render(<JSONEditor value='{"a":"1"}' field='m' />);
    expect(banners()).toHaveLength(0);

    rerender(<JSONEditor value='{"a"' field='m' />);

    expect(banners()[0].textContent).toMatch(/^JSON 格式错误: /);
  });
});
