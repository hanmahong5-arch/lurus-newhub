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

// Model request rate limiting: the console's only abuse brake. Everything
// here is about the ROUND TRIP — what the loaded options put into the form,
// and what comes back out of the form on save. A rate limit that renders
// correctly but PUTs the previous value, or silently PUTs nothing, leaves an
// operator believing a brake is engaged that is not.
//
// The Semi stubs below surface the deciding props (field / disabled / min /
// max / rules) as attributes and register each field's onChange so a test can
// drive a real typed value through it. Nothing is stubbed to render nothing.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => {
  // helpers/utils.jsx reads matchMedia at module scope; jsdom has no
  // implementation. Installed here so the REAL compareObjects/verifyJSON can
  // be imported below instead of being re-typed into the test.
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = () => ({
      matches: false,
      media: '',
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    });
  }
  return {
    put: vi.fn(),
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    handlers: {},
    rules: {},
    setValuesCalls: [],
  };
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
  // src/i18n bootstraps through this when the real helpers are imported below.
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// compareObjects / verifyJSON are the real implementations: they decide which
// keys get written and whether the group JSON is well formed, so re-typing
// them here would only test the copy.
vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers/utils');
  return {
    API: { put: (...a) => H.put(...a) },
    showError: (...a) => H.showError(...a),
    showSuccess: (...a) => H.showSuccess(...a),
    showWarning: (...a) => H.showWarning(...a),
    compareObjects: actual.compareObjects,
    verifyJSON: actual.verifyJSON,
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const Button = ({ children, onClick, loading, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        'data-loading': loading ? 'true' : 'false',
        ...rest,
      },
      children,
    );

  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);
  const Spin = ({ children, spinning }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': spinning ? 'true' : 'false' },
      children,
    );

  const Form = ({ children, values, getFormApi }) => {
    const apiRef = React.useRef(null);
    if (!apiRef.current) {
      apiRef.current = {
        setValues: (v) => H.setValuesCalls.push(v),
        validate: () => Promise.resolve(values),
      };
    }
    // Real Semi hands the api over synchronously during render; the component
    // calls setValues from its own effect, which runs after this.
    if (getFormApi) getFormApi(apiRef.current);
    return React.createElement(
      'form',
      { 'data-testid': 'form', 'data-values': JSON.stringify(values ?? null) },
      children,
    );
  };

  const makeField = (kind) =>
    function Field({
      field,
      label,
      extraText,
      placeholder,
      onChange,
      disabled,
      rules,
      min,
      max,
    }) {
      H.handlers[field] = onChange;
      H.rules[field] = rules;
      return React.createElement(
        'div',
        {
          'data-testid': `field-${field}`,
          'data-kind': kind,
          'data-disabled': disabled ? 'true' : 'false',
          'data-min': min === undefined ? '' : String(min),
          'data-max': max === undefined ? '' : String(max),
          'data-placeholder':
            typeof placeholder === 'string' ? placeholder : '',
        },
        label,
        extraText,
      );
    };

  Form.Section = ({ text, children }) =>
    React.createElement('section', { 'data-section': text }, text, children);
  Form.Switch = makeField('switch');
  Form.InputNumber = makeField('number');
  Form.TextArea = makeField('textarea');
  Form.Input = makeField('text');

  return {
    Button,
    Col,
    Form,
    Row,
    Spin,
    // utils.jsx (imported for the real compareObjects) pulls these in.
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

import RequestRateLimit from './SettingsRequestRateLimit';

// Deliberately distinct values: no two fields share a number, so a handler
// wired to the wrong key produces a visibly wrong payload rather than a
// coincidentally identical one. Enabled starts TRUE so that toggling it off
// cannot be confused with the component's `false` default.
const OPTIONS = () => ({
  ModelRequestRateLimitEnabled: true,
  ModelRequestRateLimitCount: '60',
  ModelRequestRateLimitSuccessCount: '30',
  ModelRequestRateLimitDurationMinutes: '3',
  ModelRequestRateLimitGroup: '{\n  "vip": [0, 1000]\n}',
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderRL = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<RequestRateLimit options={options} refresh={refresh} />);
  return { refresh, options };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存模型速率限制').click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');

beforeEach(() => {
  H.put.mockReset();
  H.put.mockResolvedValue(okResponse());
  H.showError.mockReset();
  H.showSuccess.mockReset();
  H.showWarning.mockReset();
  H.handlers = {};
  H.rules = {};
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds the form with the stored value of every rate-limit field', () => {
    renderRL();
    expect(formValues()).toEqual({
      ModelRequestRateLimitEnabled: true,
      ModelRequestRateLimitCount: '60',
      ModelRequestRateLimitSuccessCount: '30',
      ModelRequestRateLimitDurationMinutes: '3',
      ModelRequestRateLimitGroup: '{\n  "vip": [0, 1000]\n}',
    });
  });

  it('pushes the same values into the Semi form api, not just into state', () => {
    renderRL();
    expect(H.setValuesCalls).toHaveLength(1);
    expect(H.setValuesCalls[0].ModelRequestRateLimitCount).toBe('60');
    expect(H.setValuesCalls[0].ModelRequestRateLimitEnabled).toBe(true);
  });

  it('ignores option keys that do not belong to this page', () => {
    renderRL({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });

  it('keeps the two request counters on their documented floors', () => {
    renderRL();
    // "最多请求次数 >= 0, 最多请求完成次数 >= 1" is stated in the field help.
    // If these floors drift the UI stops matching what the operator is told.
    expect(
      screen.getByTestId('field-ModelRequestRateLimitCount').dataset.min,
    ).toBe('0');
    expect(
      screen.getByTestId('field-ModelRequestRateLimitSuccessCount').dataset.min,
    ).toBe('1');
    expect(
      screen.getByTestId('field-ModelRequestRateLimitCount').dataset.max,
    ).toBe('100000000');
  });
});

describe('saving', () => {
  it('writes nothing and warns when the operator changed nothing', async () => {
    renderRL();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('turning the limiter OFF writes the string "false", not a dropped key', async () => {
    const { refresh } = renderRL();
    await change('ModelRequestRateLimitEnabled', false);
    await save();
    await waitFor(() => expect(refresh).toHaveBeenCalled());

    expect(putBodies()).toEqual([
      { key: 'ModelRequestRateLimitEnabled', value: 'false' },
    ]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('turning the limiter ON writes the string "true"', async () => {
    renderRL({ ...OPTIONS(), ModelRequestRateLimitEnabled: false });
    await change('ModelRequestRateLimitEnabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'ModelRequestRateLimitEnabled', value: 'true' },
    ]);
  });

  it('writes the newly typed period, not the one that was loaded', async () => {
    renderRL();
    await change('ModelRequestRateLimitDurationMinutes', 15);
    await save();
    const bodies = putBodies();
    expect(bodies).toHaveLength(1);
    expect(bodies[0].key).toBe('ModelRequestRateLimitDurationMinutes');
    // Loaded value was '3'. Asserted WITHOUT String(): the wire type is
    // load-bearing here, not incidental. Semi's InputNumber hands onChange a
    // number, so the handler's String() is the only thing keeping a number out
    // of the body. A number arrives at the backend as JSON float64 and is
    // stringified there with %f — "15.000000" — which
    // repo.updateOptionMap passes to strconv.Atoi. That returns 0 on error and
    // the error is discarded, so the limiter period silently becomes 0 while
    // the console keeps showing 15. Coercing here would hide exactly that.
    expect(bodies[0].value).toBe('15');
  });

  it('writes only the fields that actually changed', async () => {
    renderRL();
    await change('ModelRequestRateLimitCount', 500);
    await change('ModelRequestRateLimitGroup', '{"vip": [0, 42]}');
    await save();

    const keys = putBodies()
      .map((b) => b.key)
      .sort();
    expect(keys).toEqual([
      'ModelRequestRateLimitCount',
      'ModelRequestRateLimitGroup',
    ]);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderRL();
    await change('ModelRequestRateLimitCount', 500);
    await change('ModelRequestRateLimitSuccessCount', 250);
    await save();

    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(String(body.value)).toBe(String(shown[body.key]));
    }
  });

  it('reports the server refusal instead of success', async () => {
    const { refresh } = renderRL();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('ModelRequestRateLimitCount', 500);
    await save();
    await waitFor(() => expect(H.showError).toHaveBeenCalled());

    expect(H.showError).toHaveBeenCalledWith('option is read-only');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderRL();
    H.put.mockRejectedValue(new Error('network down'));
    await change('ModelRequestRateLimitCount', 500);
    await save();
    await waitFor(() => expect(H.showError).toHaveBeenCalled());

    expect(H.showError).toHaveBeenCalledWith('保存失败，请重试');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('reports success and reloads only when every write succeeded', async () => {
    const { refresh } = renderRL();
    await change('ModelRequestRateLimitCount', 500);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('shows the page busy while the writes are in flight and idle after', async () => {
    let release;
    H.put.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve(okResponse());
        }),
    );
    renderRL();
    await change('ModelRequestRateLimitCount', 500);
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');

    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('per-group rate limits', () => {
  it('declares a JSON validator that accepts good JSON and rejects bad', () => {
    renderRL();
    const rules = H.rules['ModelRequestRateLimitGroup'];
    expect(rules).toHaveLength(1);
    expect(rules[0].validator(null, '{"vip": [0, 1000]}')).toBe(true);
    expect(rules[0].validator(null, '{"vip": [0, 1000]')).toBe(false);
  });

  // DEFECT (correctness): the validator above is only wired to the field's
  // `trigger='blur'`. The save button calls onSubmit() directly and never
  // consults the form's validation state, so malformed JSON is written to
  // ModelRequestRateLimitGroup verbatim. The backend cannot parse it, so the
  // per-group brake the operator just configured is not in force — and the UI
  // said 保存成功. Compare SettingsChats, which does await validate() first.
  it.skip('refuses to save a malformed per-group rate limit', async () => {
    renderRL();
    await change('ModelRequestRateLimitGroup', '{"vip": [0, 1000]');
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  // Positive control for the lock above: well-formed JSON must still save, so
  // a green lock cannot be achieved by refusing to save anything at all.
  it('saves a well-formed per-group rate limit', async () => {
    renderRL();
    await change('ModelRequestRateLimitGroup', '{"vip": [0, 42]}');
    await save();
    expect(putBodies()).toEqual([
      { key: 'ModelRequestRateLimitGroup', value: '{"vip": [0, 42]}' },
    ]);
  });
});

describe('fields the server has never stored', () => {
  // Positive control: when the key came back from /api/option/, editing it
  // saves. This is what makes the skipped lock below specific to absence.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderRL();
    await change('ModelRequestRateLimitGroup', '{"team": [10, 5]}');
    await save();
    expect(putBodies().map((b) => b.key)).toContain(
      'ModelRequestRateLimitGroup',
    );
  });

  // DEFECT (correctness): the load effect rebuilds `inputs` from ONLY the keys
  // present in props.options, and snapshots that same reduced object into
  // `inputsRow`. compareObjects skips any key missing from either side, so a
  // field the server has never stored (fresh install: ModelRequestRateLimitGroup
  // has no row) can be typed into but can never be written. With no other edit
  // pending the operator gets "你似乎并没有修改什么"; alongside another edit the
  // page reports 保存成功 while dropping this one. Either way the group brake
  // silently stays off.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    const partial = OPTIONS();
    delete partial.ModelRequestRateLimitGroup;
    renderRL(partial);
    await change('ModelRequestRateLimitGroup', '{"team": [10, 5]}');
    await save();
    expect(putBodies()).toEqual([
      { key: 'ModelRequestRateLimitGroup', value: '{"team": [10, 5]}' },
    ]);
  });

  // DEFECT (same root cause): the dropped edit is not merely unsaved, it is
  // unsaved *and* the page still reports success because a sibling edit went
  // through. This lock states the invariant that survives either fix — a save
  // that reports success must have carried every pending edit.
  it.skip('does not report success while dropping an unsaved edit', async () => {
    const partial = OPTIONS();
    delete partial.ModelRequestRateLimitGroup;
    renderRL(partial);
    await change('ModelRequestRateLimitGroup', '{"team": [10, 5]}');
    await change('ModelRequestRateLimitCount', 500);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalled());
    expect(putBodies().map((b) => b.key)).toContain(
      'ModelRequestRateLimitGroup',
    );
  });
});
