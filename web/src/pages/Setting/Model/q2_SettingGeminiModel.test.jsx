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

// Gemini model settings. This is the only page in Setting/Model that gates its
// save behind refForm.validate(), so the validate-passes / validate-fails pair
// below is the point of the file: without the negative case a test cannot tell
// "checks before saving" from "saves regardless". The safety-settings and
// version maps it writes decide which Gemini API version a model is dispatched
// to and what content gets blocked, so a malformed save is customer-visible.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  rules: new Map(),
  validate: null,
}));

vi.mock('@douyinfe/semi-ui', () => {
  const makeField = (kind) => {
    const Field = ({ field, label, onChange, disabled, rules, extraText }) => {
      H.handlers.set(field, onChange);
      H.rules.set(field, rules);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-disabled': String(!!disabled),
        'data-extra': typeof extraText === 'string' ? extraText : '',
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
        validate: (...args) => H.validate(...args),
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
  Form.TextArea = makeField('textarea');
  Form.Switch = makeField('switch');
  Form.InputNumber = makeField('number');
  Form.Input = makeField('input');

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

vi.mock('@douyinfe/semi-ui/lib/es/typography/text', () => ({
  default: ({ children }) => React.createElement('p', null, children),
}));

vi.mock('../../../helpers', () => ({
  compareObjects: vi.fn(),
  API: { put: vi.fn(), post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showWarning: vi.fn(),
  verifyJSON: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import SettingGeminiModel from './SettingGeminiModel';
import {
  API,
  showError,
  showSuccess,
  showWarning,
  verifyJSON,
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

const realVerifyJSON = (value) => {
  try {
    JSON.parse(value);
    return true;
  } catch {
    return false;
  }
};

const DEFAULTS = {
  'gemini.safety_settings': '',
  'gemini.version_settings': '',
  'gemini.supported_imagine_models': '',
  'gemini.thinking_adapter_enabled': false,
  'gemini.thinking_adapter_budget_tokens_percentage': 0.6,
  'gemini.function_call_thought_signature_enabled': true,
  'gemini.remove_function_response_id_enabled': true,
};

const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });

const putBodies = () => API.put.mock.calls.map(([, body]) => body);
const putKeys = () => putBodies().map((b) => b.key);
const applied = () => JSON.parse(screen.getByTestId('form').dataset.applied);

const renderPage = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingGeminiModel options={options} refresh={refresh} />);
  return { refresh };
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存').click();
  });
  // onSubmit does not return the inner save promise, so flush a couple of
  // microtask turns before asserting on what reached the network.
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.rules.clear();
  H.validate = vi.fn(() => Promise.resolve({}));
  compareObjects.mockImplementation(realCompareObjects);
  verifyJSON.mockImplementation(realVerifyJSON);
  API.put.mockResolvedValue({ data: { success: true } });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('hydration', () => {
  it('seeds every gemini key from the shipped defaults when nothing is stored', () => {
    renderPage({});
    expect(applied()).toEqual(DEFAULTS);
  });

  it('overlays the stored values on top of the defaults', () => {
    renderPage({
      'gemini.safety_settings': '{"default":"BLOCK_NONE"}',
      'gemini.thinking_adapter_enabled': true,
    });
    expect(applied()).toEqual({
      ...DEFAULTS,
      'gemini.safety_settings': '{"default":"BLOCK_NONE"}',
      'gemini.thinking_adapter_enabled': true,
    });
  });

  it('ignores option keys belonging to other setting pages', () => {
    renderPage({ 'claude.default_max_tokens': '{"default":8192}' });
    expect(applied()).not.toHaveProperty('claude.default_max_tokens');
    expect(applied()).toEqual(DEFAULTS);
  });

  it('re-applies when the parent hands down refreshed options', () => {
    const refresh = vi.fn();
    const { rerender } = render(
      <SettingGeminiModel options={{}} refresh={refresh} />,
    );
    rerender(
      <SettingGeminiModel
        options={{ 'gemini.version_settings': '{"default":"v1beta"}' }}
        refresh={refresh}
      />,
    );
    expect(applied()['gemini.version_settings']).toBe('{"default":"v1beta"}');
  });
});

describe('the validate gate', () => {
  it('saves once the form validates', async () => {
    const { refresh } = renderPage({});
    emit('gemini.version_settings', '{"default":"v1"}');
    await save();

    expect(H.validate).toHaveBeenCalledTimes(1);
    expect(putBodies()).toEqual([
      { key: 'gemini.version_settings', value: '{"default":"v1"}' },
    ]);
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('sends nothing at all when the form does not validate', async () => {
    const { refresh } = renderPage({});
    H.validate = vi.fn(() =>
      Promise.reject({ 'gemini.version_settings': 'bad json' }),
    );
    emit('gemini.version_settings', '{oops');
    await save();

    // The negative half of the pair: without it, a page that never validates
    // would look identical to this one.
    expect(API.put).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('请检查输入');
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('leaves the page usable after a failed validation', async () => {
    renderPage({});
    H.validate = vi.fn(() => Promise.reject(new Error('nope')));
    emit('gemini.safety_settings', '{bad');
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('false');
  });
});

describe('saving', () => {
  it('refuses an empty save and does not touch the option endpoint', async () => {
    renderPage({ 'gemini.safety_settings': '{"default":"OFF"}' });
    await save();
    expect(showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(API.put).not.toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('sends only the field that changed', async () => {
    renderPage({
      'gemini.safety_settings': '{"default":"OFF"}',
      'gemini.version_settings': '{"default":"v1beta"}',
    });
    // Change the SECOND field only: a save path that always submits field #1,
    // or submits the whole object, produces a different key list.
    emit('gemini.version_settings', '{"default":"v1"}');
    await save();
    expect(putKeys()).toEqual(['gemini.version_settings']);
  });

  it('saves a first-ever value for a key the server has never stored', async () => {
    renderPage({});
    emit('gemini.supported_imagine_models', '["gemini-2.0-flash-exp"]');
    await save();
    expect(putBodies()).toEqual([
      {
        key: 'gemini.supported_imagine_models',
        value: '["gemini-2.0-flash-exp"]',
      },
    ]);
  });

  it('stringifies a switch into the "false" the option store expects', async () => {
    renderPage({ 'gemini.remove_function_response_id_enabled': true });
    emit('gemini.remove_function_response_id_enabled', false);
    await save();
    expect(putBodies()[0]).toEqual({
      key: 'gemini.remove_function_response_id_enabled',
      value: 'false',
    });
    expect(typeof putBodies()[0].value).toBe('string');
  });

  it('keeps a legitimate zero budget percentage instead of blanking it', async () => {
    renderPage({});
    emit('gemini.thinking_adapter_budget_tokens_percentage', 0);
    await save();
    expect(putBodies()).toEqual([
      { key: 'gemini.thinking_adapter_budget_tokens_percentage', value: '0' },
    ]);
  });

  it('sends every changed field and no unchanged ones', async () => {
    renderPage({});
    emit('gemini.thinking_adapter_enabled', true);
    emit('gemini.function_call_thought_signature_enabled', false);
    await save();
    expect(putKeys().sort()).toEqual([
      'gemini.function_call_thought_signature_enabled',
      'gemini.thinking_adapter_enabled',
    ]);
    expect(putKeys()).not.toContain(
      'gemini.remove_function_response_id_enabled',
    );
  });

  it('reports a partial failure and does not claim success', async () => {
    const { refresh } = renderPage({});
    API.put
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce(undefined);
    emit('gemini.thinking_adapter_enabled', true);
    emit('gemini.safety_settings', '{"default":"OFF"}');
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('reports a rejected save and does not claim success', async () => {
    const { refresh } = renderPage({});
    API.put.mockRejectedValue(new Error('offline'));
    emit('gemini.safety_settings', '{"default":"OFF"}');
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('never reports success for a single request the server did not accept', async () => {
    const { refresh } = renderPage({});
    API.put.mockResolvedValue(undefined);
    emit('gemini.safety_settings', '{"default":"OFF"}');
    await save();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('drops the busy spinner once the save settles', async () => {
    renderPage({});
    emit('gemini.safety_settings', '{"default":"OFF"}');
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('JSON validation rules', () => {
  it.each([
    'gemini.safety_settings',
    'gemini.version_settings',
    'gemini.supported_imagine_models',
  ])('rejects a non-JSON value for %s', (field) => {
    renderPage();
    const rule = H.rules.get(field)[0];
    expect(rule.validator({}, '{not json')).toBe(false);
    expect(rule.message).toBe('不是合法的 JSON 字符串');
  });

  it.each([
    ['gemini.safety_settings', '{"default":"OFF"}'],
    ['gemini.version_settings', '{"default":"v1beta"}'],
    ['gemini.supported_imagine_models', '["gemini-2.0-flash-exp"]'],
  ])('accepts a JSON value for %s', (field, value) => {
    renderPage();
    const rule = H.rules.get(field)[0];
    expect(rule.validator({}, value)).toBe(true);
  });

  it('does not put a JSON rule on the boolean switches', () => {
    renderPage();
    expect(H.rules.get('gemini.thinking_adapter_enabled')).toBeUndefined();
    expect(
      H.rules.get('gemini.function_call_thought_signature_enabled'),
    ).toBeUndefined();
  });
});
