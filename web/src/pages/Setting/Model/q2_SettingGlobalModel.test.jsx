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

// Global relay switches. Two of these are load-bearing for what a customer can
// call: pass-through disables redirect + channel adaptation wholesale, and the
// thinking blacklist decides which model names keep their -thinking suffix. The
// blacklist round-trips through JSON on the way in and on the way out, so the
// tests below pin both the parse-on-load fallbacks and the normalise-on-save
// step, plus the enable/disable pairing on the keep-alive fields.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  rules: new Map(),
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
  // Surfaces the description text: the keep-alive warning is the only thing
  // telling an operator that a mid-stream channel error becomes unretryable.
  const Banner = ({ type, description }) =>
    React.createElement(
      'div',
      { 'data-testid': 'banner', 'data-type': type },
      description,
    );
  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);

  return { Form, Button, Spin, Banner, Row, Col };
});

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

import SettingGlobalModel from './SettingGlobalModel';
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

const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });

const putBodies = () => API.put.mock.calls.map(([, body]) => body);
const putKeys = () => putBodies().map((b) => b.key);
const applied = () => JSON.parse(screen.getByTestId('form').dataset.applied);

const renderPage = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingGlobalModel options={options} refresh={refresh} />);
  return { refresh };
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存').click();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.rules.clear();
  compareObjects.mockImplementation(realCompareObjects);
  verifyJSON.mockImplementation(realVerifyJSON);
  API.put.mockResolvedValue({ data: { success: true } });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('hydration', () => {
  it('falls back to the shipped defaults for keys the server has not stored', () => {
    renderPage({});
    expect(applied()).toEqual({
      'global.pass_through_request_enabled': false,
      'global.thinking_model_blacklist': '[]',
      'general_setting.ping_interval_enabled': false,
      'general_setting.ping_interval_seconds': 60,
    });
  });

  it('takes the stored value over the default when one exists', () => {
    renderPage({
      'general_setting.ping_interval_seconds': 15,
      'general_setting.ping_interval_enabled': true,
    });
    expect(applied()['general_setting.ping_interval_seconds']).toBe(15);
    expect(applied()['general_setting.ping_interval_enabled']).toBe(true);
    // Untouched keys still come from the defaults, not from undefined.
    expect(applied()['global.pass_through_request_enabled']).toBe(false);
  });

  it('pretty-prints a stored blacklist so it is editable as a block', () => {
    renderPage({
      'global.thinking_model_blacklist': '["kimi-k2-thinking","glm-4-air"]',
    });
    expect(applied()['global.thinking_model_blacklist']).toBe(
      '[\n  "kimi-k2-thinking",\n  "glm-4-air"\n]',
    );
  });

  it('falls back to an empty list when the stored blacklist is not JSON', () => {
    renderPage({ 'global.thinking_model_blacklist': '[oops' });
    // Anything but "[]" here would be posted straight back on the next save
    // and keep the option corrupt.
    expect(applied()['global.thinking_model_blacklist']).toBe('[]');
  });

  it('falls back to an empty list when the stored blacklist is blank', () => {
    renderPage({ 'global.thinking_model_blacklist': '   ' });
    expect(applied()['global.thinking_model_blacklist']).toBe('[]');
  });

  it('drops option keys that belong to other setting pages', () => {
    renderPage({
      'global.pass_through_request_enabled': true,
      'claude.default_max_tokens': '{"default":8192}',
    });
    expect(applied()).not.toHaveProperty('claude.default_max_tokens');
  });
});

describe('keep-alive fields', () => {
  it('locks the interval box while ping is off', () => {
    renderPage({ 'general_setting.ping_interval_enabled': false });
    expect(
      screen.getByTestId('field-general_setting.ping_interval_seconds').dataset
        .disabled,
    ).toBe('true');
  });

  it('unlocks the interval box while ping is on', () => {
    renderPage({ 'general_setting.ping_interval_enabled': true });
    expect(
      screen.getByTestId('field-general_setting.ping_interval_seconds').dataset
        .disabled,
    ).toBe('false');
  });

  it('follows the switch without waiting for a save', () => {
    renderPage({ 'general_setting.ping_interval_enabled': false });
    emit('general_setting.ping_interval_enabled', true);
    expect(
      screen.getByTestId('field-general_setting.ping_interval_seconds').dataset
        .disabled,
    ).toBe('false');
  });

  it('warns that keep-alive makes a mid-stream channel error unretryable', () => {
    renderPage();
    const banner = screen.getByTestId('banner');
    expect(banner.dataset.type).toBe('warning');
    expect(banner.textContent).toContain('保活');
    expect(banner.textContent).toContain('无法重试');
  });
});

describe('saving', () => {
  it('refuses an empty save and does not touch the option endpoint', async () => {
    renderPage({ 'global.pass_through_request_enabled': true });
    await save();
    expect(showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(API.put).not.toHaveBeenCalled();
  });

  it('sends only the field that changed', async () => {
    const { refresh } = renderPage({
      'general_setting.ping_interval_enabled': true,
      'general_setting.ping_interval_seconds': 60,
    });
    emit('general_setting.ping_interval_seconds', 300);
    await save();

    expect(putBodies()).toEqual([
      { key: 'general_setting.ping_interval_seconds', value: '300' },
    ]);
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('saves a first-ever value for a key the server has never stored', async () => {
    // The counterpart page (SettingClaudeModel) drops unstored keys during
    // hydration and can never save them; this page seeds them from the
    // defaults, so the first value an operator types does reach the server.
    renderPage({});
    emit('general_setting.ping_interval_seconds', 45);
    await save();
    expect(putBodies()).toEqual([
      { key: 'general_setting.ping_interval_seconds', value: '45' },
    ]);
  });

  it('stringifies the pass-through switch rather than posting a raw boolean', async () => {
    renderPage({ 'global.pass_through_request_enabled': false });
    emit('global.pass_through_request_enabled', true);
    await save();
    expect(putBodies()[0]).toEqual({
      key: 'global.pass_through_request_enabled',
      value: 'true',
    });
    expect(typeof putBodies()[0].value).toBe('string');
  });

  it('rewrites a blanked-out blacklist to an empty JSON array', async () => {
    renderPage({ 'global.thinking_model_blacklist': '["a"]' });
    emit('global.thinking_model_blacklist', '   ');
    await save();
    // Posting the whitespace verbatim leaves the backend with a value that
    // fails to parse, which silently blacklists nothing at all.
    expect(putBodies()).toEqual([
      { key: 'global.thinking_model_blacklist', value: '[]' },
    ]);
  });

  it('rewrites a non-string blacklist to an empty JSON array', async () => {
    renderPage({ 'global.thinking_model_blacklist': '["a"]' });
    emit('global.thinking_model_blacklist', null);
    await save();
    expect(putBodies()).toEqual([
      { key: 'global.thinking_model_blacklist', value: '[]' },
    ]);
  });

  it('leaves a populated blacklist untouched on the way out', async () => {
    renderPage({ 'global.thinking_model_blacklist': '["a"]' });
    emit('global.thinking_model_blacklist', '["a","b"]');
    await save();
    expect(putBodies()).toEqual([
      { key: 'global.thinking_model_blacklist', value: '["a","b"]' },
    ]);
  });

  it('only normalises the blacklist, not the other fields', async () => {
    renderPage({
      'global.thinking_model_blacklist': '["a"]',
      'general_setting.ping_interval_seconds': 60,
    });
    emit('general_setting.ping_interval_seconds', 0);
    await save();
    // A blanket "empty means []" rule applied to every key would turn this
    // into "[]"; a `value || ''` would turn it into "".
    expect(putBodies()).toEqual([
      { key: 'general_setting.ping_interval_seconds', value: '0' },
    ]);
  });

  it('sends each changed field exactly once', async () => {
    renderPage({
      'global.pass_through_request_enabled': false,
      'general_setting.ping_interval_enabled': false,
    });
    emit('global.pass_through_request_enabled', true);
    emit('general_setting.ping_interval_enabled', true);
    await save();
    expect(putKeys().sort()).toEqual([
      'general_setting.ping_interval_enabled',
      'global.pass_through_request_enabled',
    ]);
    expect(putKeys()).not.toContain('global.thinking_model_blacklist');
  });

  it('reports a partial failure and does not claim success', async () => {
    const { refresh } = renderPage({});
    API.put
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce(undefined);
    emit('global.pass_through_request_enabled', true);
    emit('general_setting.ping_interval_enabled', true);
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
    emit('global.pass_through_request_enabled', true);
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
    emit('global.pass_through_request_enabled', true);
    await save();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('drops the busy spinner once the save settles', async () => {
    renderPage({});
    emit('global.pass_through_request_enabled', true);
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('blacklist validation rule', () => {
  it('accepts a blank value', () => {
    renderPage();
    const rule = H.rules.get('global.thinking_model_blacklist')[0];
    expect(rule.validator({}, '')).toBe(true);
    expect(rule.validator({}, '   ')).toBe(true);
  });

  it('accepts a JSON array', () => {
    renderPage();
    const rule = H.rules.get('global.thinking_model_blacklist')[0];
    expect(rule.validator({}, '["kimi-k2-thinking"]')).toBe(true);
  });

  it('rejects a value that is not JSON', () => {
    renderPage();
    const rule = H.rules.get('global.thinking_model_blacklist')[0];
    expect(rule.validator({}, '[kimi')).toBe(false);
    expect(rule.message).toBe('不是合法的 JSON 字符串');
  });
});

describe('DEFECT: the blacklist field declares a JSON rule the save path never runs', () => {
  // onSubmit diffs and PUTs straight away — unlike SettingGeminiModel, it never
  // awaits refForm.current.validate(). normalizeValueBeforeSave only rescues
  // the blank case, so a value that fails the field's own `verifyJSON` rule is
  // written to the option store verbatim. The backend then fails to parse it
  // and the blacklist behaves as empty, i.e. every model silently regains the
  // -thinking / -nothinking suffix rewriting the operator meant to stop.

  it('saves a blacklist that passes its own rule', async () => {
    renderPage({ 'global.thinking_model_blacklist': '["a"]' });
    const rule = H.rules.get('global.thinking_model_blacklist')[0];
    emit('global.thinking_model_blacklist', '["a","b"]');
    expect(rule.validator({}, '["a","b"]')).toBe(true);
    await save();
    expect(putKeys()).toContain('global.thinking_model_blacklist');
  });

  // Verified red on 2026-08-20: un-skipped, the PUT went out anyway
  // (`expected [ 'global.thinking_model_blacklist' ] not to contain
  // 'global.thinking_model_blacklist'`).
  it('refuses to save a blacklist that fails its own JSON rule', async () => {
    renderPage({ 'global.thinking_model_blacklist': '["a"]' });
    const rule = H.rules.get('global.thinking_model_blacklist')[0];
    expect(rule.validator({}, '["a", ')).toBe(false);

    emit('global.thinking_model_blacklist', '["a", ');
    await save();

    expect(putKeys()).not.toContain('global.thinking_model_blacklist');
    expect(showSuccess).not.toHaveBeenCalled();
  });
});
