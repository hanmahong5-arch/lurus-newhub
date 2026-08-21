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

// Data-dashboard settings. Three fields, but the hydration effect also mirrors
// the chosen granularity into localStorage, where helpers/dashboard.jsx
// getDefaultTime() reads it back as `localStorage.getItem(...) || 'hour'`. That
// side effect is the interesting part of the file and it reads the wrong
// variable — see the DEFECT block at the bottom.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  options: new Map(),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const makeField = (kind) => {
    const Field = ({
      field,
      label,
      onChange,
      disabled,
      optionList,
      placeholder,
      extraText,
      suffix,
    }) => {
      H.handlers.set(field, onChange);
      H.options.set(field, optionList);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-disabled': String(!!disabled),
        'data-placeholder': typeof placeholder === 'string' ? placeholder : '',
        'data-extra': typeof extraText === 'string' ? extraText : '',
        'data-suffix': typeof suffix === 'string' ? suffix : '',
      });
    };
    return Field;
  };

  const Form = ({ children, getFormApi, values }) => {
    const [applied, setApplied] = React.useState(null);
    const apiRef = React.useRef(null);
    if (!apiRef.current) {
      apiRef.current = {
        setValues: (v) => setApplied(v),
        setValue: (k, v) => setApplied((prev) => ({ ...(prev ?? {}), [k]: v })),
      };
      getFormApi?.(apiRef.current);
    }
    return React.createElement(
      'form',
      {
        'data-testid': 'form',
        'data-values': JSON.stringify(values ?? null),
        'data-applied': JSON.stringify(applied),
      },
      children,
    );
  };
  Form.Section = ({ text, children }) =>
    React.createElement(
      'section',
      { 'data-testid': 'section' },
      React.createElement('h3', null, text),
      children,
    );
  Form.Switch = makeField('switch');
  Form.InputNumber = makeField('number');
  Form.Select = makeField('select');
  Form.Input = makeField('input');
  Form.TextArea = makeField('textarea');

  const Button = ({ children, onClick, disabled }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, disabled: !!disabled },
      children,
    );
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);

  return { Form, Button, Spin, Row, Col };
});

vi.mock('../../../helpers', () => ({
  compareObjects: vi.fn(),
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import DataDashboard from './SettingsDataDashboard';
import {
  API,
  showError,
  showSuccess,
  showWarning,
  compareObjects,
} from '../../../helpers';

const realCompareObjects = (oldObject, newObject) => {
  const changed = [];
  for (const key in oldObject) {
    if (
      Object.prototype.hasOwnProperty.call(oldObject, key) &&
      Object.prototype.hasOwnProperty.call(newObject, key) &&
      oldObject[key] !== newObject[key]
    ) {
      changed.push({ key, oldValue: oldObject[key], newValue: newObject[key] });
    }
  }
  return changed;
};

const FULL_OPTIONS = {
  DataExportEnabled: true,
  DataExportInterval: '10',
  DataExportDefaultTime: 'day',
};

// The granularities helpers/dashboard.jsx knows how to ask the backend for.
// getDefaultTime() only rescues an empty string, so anything else here is a
// value the dashboard will forward verbatim.
const USABLE_GRANULARITIES = ['', 'hour', 'day', 'week'];

const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });

const putBodies = () => API.put.mock.calls.map(([, body]) => body);
const applied = () => JSON.parse(screen.getByTestId('form').dataset.applied);

const renderPage = (options = FULL_OPTIONS) => {
  const refresh = vi.fn();
  render(<DataDashboard options={options} refresh={refresh} />);
  return { refresh };
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存数据看板设置').click();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.options.clear();
  localStorage.clear();
  compareObjects.mockImplementation(realCompareObjects);
  API.put.mockResolvedValue({ data: { success: true } });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('hydration', () => {
  it('pushes the three dashboard keys into the form', () => {
    renderPage();
    expect(applied()).toEqual(FULL_OPTIONS);
  });

  it('ignores option keys belonging to other setting pages', () => {
    renderPage({ ...FULL_OPTIONS, 'claude.default_max_tokens': '{}' });
    expect(applied()).not.toHaveProperty('claude.default_max_tokens');
    expect(Object.keys(applied()).sort()).toEqual(
      Object.keys(FULL_OPTIONS).sort(),
    );
  });

  it('offers exactly the three granularities the dashboard supports', () => {
    renderPage();
    expect(H.options.get('DataExportDefaultTime')).toEqual([
      { key: 'hour', label: '小时', value: 'hour' },
      { key: 'day', label: '天', value: 'day' },
      { key: 'week', label: '周', value: 'week' },
    ]);
  });

  it('writes the granularity mirror that the dashboard reads back', () => {
    renderPage();
    // The key must exist after hydration; getDefaultTime() depends on it.
    expect(localStorage.getItem('data_export_default_time')).not.toBeNull();
  });
});

describe('saving', () => {
  it('refuses an empty save and does not touch the option endpoint', async () => {
    renderPage();
    await save();
    expect(showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(API.put).not.toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('sends only the field that changed', async () => {
    const { refresh } = renderPage();
    // The interval is the SECOND field: a save that always submits field #1,
    // or the whole object, would produce a different body list.
    emit('DataExportInterval', 45);
    await save();

    expect(putBodies()).toEqual([{ key: 'DataExportInterval', value: '45' }]);
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('stringifies the enable switch rather than posting a raw boolean', async () => {
    renderPage();
    emit('DataExportEnabled', false);
    await save();
    expect(putBodies()[0]).toEqual({
      key: 'DataExportEnabled',
      value: 'false',
    });
    expect(typeof putBodies()[0].value).toBe('string');
  });

  it('sends a switched-on flag as the string "true"', async () => {
    renderPage({ ...FULL_OPTIONS, DataExportEnabled: false });
    emit('DataExportEnabled', true);
    await save();
    expect(putBodies()[0]).toEqual({ key: 'DataExportEnabled', value: 'true' });
  });

  it('sends the chosen granularity verbatim', async () => {
    renderPage();
    emit('DataExportDefaultTime', 'week');
    await save();
    expect(putBodies()).toEqual([
      { key: 'DataExportDefaultTime', value: 'week' },
    ]);
  });

  it('sends every changed field and no unchanged ones', async () => {
    renderPage();
    emit('DataExportEnabled', false);
    emit('DataExportDefaultTime', 'hour');
    await save();
    expect(
      putBodies()
        .map((b) => b.key)
        .sort(),
    ).toEqual(['DataExportDefaultTime', 'DataExportEnabled']);
    expect(putBodies().map((b) => b.key)).not.toContain('DataExportInterval');
  });

  it('reports a partial failure and does not claim success', async () => {
    const { refresh } = renderPage();
    API.put
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce(undefined);
    emit('DataExportEnabled', false);
    emit('DataExportDefaultTime', 'hour');
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('reports a rejected save and does not claim success', async () => {
    const { refresh } = renderPage();
    API.put.mockRejectedValue(new Error('offline'));
    emit('DataExportInterval', 45);
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('never reports success for a single request the server did not accept', async () => {
    const { refresh } = renderPage();
    API.put.mockResolvedValue(undefined);
    emit('DataExportInterval', 45);
    await save();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('drops the busy spinner once the save settles', async () => {
    renderPage();
    emit('DataExportInterval', 45);
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('DEFECT: the granularity mirror is written from the pre-hydration state', () => {
  // The effect builds `currentInputs`, calls setInputs(currentInputs), then
  // writes localStorage from `inputs` — the state variable captured by THIS
  // render, i.e. the value from before hydration. The mirror is therefore
  // always one options-load behind. When the option has never been stored the
  // captured value is `undefined` and String() turns it into the literal
  // "undefined", which getDefaultTime() happily returns because it only
  // rescues the empty string.

  it('writes the mirror on every options load', () => {
    const refresh = vi.fn();
    const { rerender } = render(
      <DataDashboard options={FULL_OPTIONS} refresh={refresh} />,
    );
    localStorage.removeItem('data_export_default_time');
    rerender(
      <DataDashboard
        options={{ ...FULL_OPTIONS, DataExportDefaultTime: 'week' }}
        refresh={refresh}
      />,
    );
    // Invariant either way: the mirror is refreshed whenever options change.
    expect(localStorage.getItem('data_export_default_time')).not.toBeNull();
  });

  // Verified red on 2026-08-20: un-skipped, the mirror held "day" after the
  // options moved to "week" (`expected 'day' to be 'week'`).
  it('mirrors the granularity that was just loaded', () => {
    const refresh = vi.fn();
    const { rerender } = render(
      <DataDashboard options={FULL_OPTIONS} refresh={refresh} />,
    );
    rerender(
      <DataDashboard
        options={{ ...FULL_OPTIONS, DataExportDefaultTime: 'week' }}
        refresh={refresh}
      />,
    );
    expect(localStorage.getItem('data_export_default_time')).toBe('week');
  });

  // Verified red on 2026-08-20: un-skipped, the mirror held the literal
  // "undefined" (`expected [ '', 'hour', 'day', 'week' ] to include
  // 'undefined'`).
  it('never mirrors a granularity the dashboard cannot use', () => {
    const refresh = vi.fn();
    const { rerender } = render(
      <DataDashboard options={{ DataExportEnabled: true }} refresh={refresh} />,
    );
    rerender(
      <DataDashboard
        options={{ DataExportEnabled: false }}
        refresh={refresh}
      />,
    );
    expect(USABLE_GRANULARITIES).toContain(
      localStorage.getItem('data_export_default_time'),
    );
  });
});

describe('DEFECT: options the server has never stored are dropped from state', () => {
  // Same hydration shape as SettingClaudeModel: `currentInputs` starts empty
  // and only picks up keys already present in props.options, so on a fresh
  // install the missing keys disappear from both `inputs` and `inputsRow`.
  // compareObjects only reports keys present in both, so the first value an
  // operator picks for such a field is diffed away and the save is refused.

  it('never reports a save it did not send for a not-yet-stored key', async () => {
    const { refresh } = renderPage({ DataExportEnabled: true });
    emit('DataExportDefaultTime', 'week');
    await save();

    const sentTheKey = putBodies().some(
      (b) => b.key === 'DataExportDefaultTime',
    );
    // Invariant that survives the fix: success may only be claimed if the
    // edited key actually went out on the wire.
    expect(showSuccess.mock.calls.length > 0 && !sentTheKey).toBe(false);
    expect(refresh.mock.calls.length > 0 && !sentTheKey).toBe(false);
  });

  // Verified red on 2026-08-20: un-skipped, the save issued zero PUTs
  // (`expected [] to deeply equal [ { key: 'DataExportDefaultTime' … } ]`).
  it('saves the first granularity picked when none was stored', async () => {
    renderPage({ DataExportEnabled: true });
    emit('DataExportDefaultTime', 'week');
    await save();

    expect(putBodies()).toEqual([
      { key: 'DataExportDefaultTime', value: 'week' },
    ]);
    expect(showWarning).not.toHaveBeenCalled();
  });
});
