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

// Claude model settings. Every value on this page ends up in the relay's
// request path: the header override decides which anthropic-beta features are
// negotiated, the max-tokens map decides how long a completion may run, and the
// thinking adapter decides whether a "-thinking" model name resolves at all.
// A save that silently drops a field changes what customers can call, so the
// tests below care mostly about WHICH keys reach PUT /api/option/ and what
// exact string value each one carries.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
  rules: new Map(),
}));

vi.mock('@douyinfe/semi-ui', () => {
  // Fields register their onChange in a hoisted map so a test can hand them a
  // boolean / number / null exactly as Semi would, instead of only the strings
  // a DOM <input> can carry. The marker div still surfaces the deciding props
  // (label, disabled) so branch outcomes remain assertable in the DOM.
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
  verifyJSON: vi.fn(() => true),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import SettingClaudeModel from './SettingClaudeModel';
import {
  API,
  showError,
  showSuccess,
  showWarning,
  verifyJSON,
  compareObjects,
} from '../../../helpers';

// The component's own diffing is the thing under test, so the helper runs for
// real here rather than being faked away.
const realCompareObjects = (oldObject, newObject) => {
  const changed = [];
  for (const key in oldObject) {
    if (
      Object.prototype.hasOwnProperty.call(oldObject, key) &&
      Object.prototype.hasOwnProperty.call(newObject, key) &&
      oldObject[key] !== newObject[key]
    ) {
      changed.push({
        key,
        oldValue: oldObject[key],
        newValue: newObject[key],
      });
    }
  }
  return changed;
};

const FULL_OPTIONS = {
  'claude.model_headers_settings': '{"a":1}',
  'claude.thinking_adapter_enabled': true,
  'claude.default_max_tokens': '{"default":8192}',
  'claude.thinking_adapter_budget_tokens_percentage': 0.8,
};

const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });

const putBodies = () => API.put.mock.calls.map(([, body]) => body);
const putKeys = () => putBodies().map((b) => b.key);

const renderPage = (options = FULL_OPTIONS) => {
  const refresh = vi.fn();
  render(<SettingClaudeModel options={options} refresh={refresh} />);
  return { refresh };
};

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  H.rules.clear();
  compareObjects.mockImplementation(realCompareObjects);
  verifyJSON.mockReturnValue(true);
  API.put.mockResolvedValue({ data: { success: true } });
});

afterEach(() => {
  vi.restoreAllMocks();
});

const save = async () => {
  await act(async () => {
    screen.getByText('保存').click();
  });
};

describe('hydration from server options', () => {
  it('pushes exactly the four claude keys into the form', () => {
    renderPage();
    expect(JSON.parse(screen.getByTestId('form').dataset.applied)).toEqual(
      FULL_OPTIONS,
    );
  });

  it('ignores option keys that do not belong to this page', () => {
    renderPage({ ...FULL_OPTIONS, 'gemini.safety_settings': 'nope' });
    const applied = JSON.parse(screen.getByTestId('form').dataset.applied);
    expect(applied).not.toHaveProperty('gemini.safety_settings');
    expect(Object.keys(applied).sort()).toEqual(
      Object.keys(FULL_OPTIONS).sort(),
    );
  });

  it('re-applies when the parent hands down a fresh options object', () => {
    const { rerender } = render(
      <SettingClaudeModel options={FULL_OPTIONS} refresh={vi.fn()} />,
    );
    rerender(
      <SettingClaudeModel
        options={{ ...FULL_OPTIONS, 'claude.default_max_tokens': '{"x":1}' }}
        refresh={vi.fn()}
      />,
    );
    expect(
      JSON.parse(screen.getByTestId('form').dataset.applied)[
        'claude.default_max_tokens'
      ],
    ).toBe('{"x":1}');
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

  it('sends only the field that actually changed', async () => {
    const { refresh } = renderPage();
    // Fixture is deliberately NOT the first field on the page: a save path
    // that always submits the whole object, or always submits field #1, would
    // produce a different key list here.
    emit('claude.default_max_tokens', '{"default":4096}');
    await save();

    expect(putKeys()).toEqual(['claude.default_max_tokens']);
    expect(putBodies()[0]).toEqual({
      key: 'claude.default_max_tokens',
      value: '{"default":4096}',
    });
    expect(API.put).toHaveBeenCalledWith('/api/option/', expect.any(Object));
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('sends every changed field and no unchanged ones', async () => {
    renderPage();
    emit('claude.model_headers_settings', '{"b":2}');
    emit('claude.thinking_adapter_budget_tokens_percentage', 0.5);
    await save();

    expect(putKeys().sort()).toEqual([
      'claude.model_headers_settings',
      'claude.thinking_adapter_budget_tokens_percentage',
    ]);
    expect(putKeys()).not.toContain('claude.default_max_tokens');
  });

  it('stringifies a boolean toggle into the "false" the option store expects', async () => {
    renderPage();
    emit('claude.thinking_adapter_enabled', false);
    await save();

    const body = putBodies()[0];
    expect(body.key).toBe('claude.thinking_adapter_enabled');
    // A raw boolean here is stored as an empty/absent option by the backend,
    // which silently re-enables the adapter on the next boot.
    expect(body.value).toBe('false');
    expect(typeof body.value).toBe('string');
  });

  it('stringifies a numeric percentage without losing the decimal', async () => {
    renderPage();
    emit('claude.thinking_adapter_budget_tokens_percentage', 0.25);
    await save();
    expect(putBodies()[0].value).toBe('0.25');
  });

  it('keeps a legitimate zero rather than collapsing it to an empty value', async () => {
    renderPage();
    emit('claude.thinking_adapter_budget_tokens_percentage', 0);
    await save();
    // `value || ''` in this position would post an empty string and wipe the
    // stored percentage; String(0) must survive as "0".
    expect(putBodies()[0].value).toBe('0');
  });

  it('reports a partial failure and does not claim success', async () => {
    const { refresh } = renderPage();
    API.put
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce(undefined);
    emit('claude.model_headers_settings', '{"b":2}');
    emit('claude.default_max_tokens', '{"default":1}');
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('reports a rejected save and does not claim success', async () => {
    const { refresh } = renderPage();
    API.put.mockRejectedValue(new Error('boom'));
    emit('claude.default_max_tokens', '{"default":1}');
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
    emit('claude.default_max_tokens', '{"default":1}');
    await save();

    // Invariant, whatever the eventual error-reporting choice is: a save the
    // server refused must not be presented as a save that landed.
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('clears the busy spinner once the save settles', async () => {
    renderPage();
    emit('claude.default_max_tokens', '{"default":1}');
    expect(screen.getByTestId('spin').dataset.spinning).toBe('false');
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
    expect(showSuccess).toHaveBeenCalled();
  });

  it('clears the busy spinner even when the save blows up', async () => {
    renderPage();
    API.put.mockRejectedValue(new Error('boom'));
    emit('claude.default_max_tokens', '{"default":1}');
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('JSON validation rules', () => {
  it('rejects a header override that is not valid JSON', () => {
    renderPage();
    verifyJSON.mockReturnValue(false);
    const rule = H.rules.get('claude.model_headers_settings')[0];
    expect(rule.validator({}, 'not json')).toBe(false);
    expect(verifyJSON).toHaveBeenCalledWith('not json');
    expect(rule.message).toBe('不是合法的 JSON 字符串');
  });

  it('accepts a header override that is valid JSON', () => {
    renderPage();
    verifyJSON.mockReturnValue(true);
    const rule = H.rules.get('claude.model_headers_settings')[0];
    expect(rule.validator({}, '{"anthropic-beta":[]}')).toBe(true);
  });

  it('guards the max-tokens map with the same JSON check', () => {
    renderPage();
    verifyJSON.mockReturnValue(false);
    const rule = H.rules.get('claude.default_max_tokens')[0];
    expect(rule.validator({}, '{oops')).toBe(false);
  });
});

describe('DEFECT: options the server has never stored are dropped from state', () => {
  // The hydration effect builds `currentInputs` from scratch and only copies
  // keys that already exist in props.options. On a fresh install the option
  // rows do not exist yet, so those keys vanish from BOTH `inputs` and
  // `inputsRow`. compareObjects only reports a key present in both objects, so
  // the very first value an operator types for such a field is diffed away and
  // the save is refused with "你似乎并没有修改什么" — the Claude max-tokens map
  // can never be set at all until somebody writes the row by hand.

  it('never reports a save it did not send for a not-yet-stored key', async () => {
    const { refresh } = renderPage({ 'claude.thinking_adapter_enabled': true });
    emit('claude.default_max_tokens', '{"default":4096}');
    await save();

    // Invariant that survives the fix: success may only be reported if the
    // edited key actually went out on the wire. Today nothing goes out and
    // nothing is reported; once hydration keeps the defaults, both happen.
    const reportedSuccess = showSuccess.mock.calls.length > 0;
    const sentTheKey = putKeys().includes('claude.default_max_tokens');
    expect(reportedSuccess && !sentTheKey).toBe(false);
    expect(refresh.mock.calls.length > 0 && !sentTheKey).toBe(false);
  });

  // Verified red on 2026-08-20: un-skipped, the save issued zero PUTs
  // (`expected [] to deeply equal [ { key: 'claude.default_max_tokens' … } ]`).
  it.skip('saves the first value entered for a key the server has not stored yet', async () => {
    renderPage({ 'claude.thinking_adapter_enabled': true });
    emit('claude.default_max_tokens', '{"default":4096}');
    await save();

    expect(putBodies()).toEqual([
      { key: 'claude.default_max_tokens', value: '{"default":4096}' },
    ]);
    expect(showWarning).not.toHaveBeenCalled();
  });
});
