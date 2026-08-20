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

// Daily check-in bonus: a switch and a quota range that hands out free credit.
// The interesting behaviour is the gate — the two quota inputs are disabled
// while the feature is off — and the save round trip, since these values are
// numbers in the UI but strings on the wire.
//
// Fixture note: /api/option/ returns every value as a string and the parent
// (components/settings/OperationSetting.jsx) only converts the keys it holds a
// boolean default for. So the enabled flag arrives as a real boolean and the
// two quotas arrive as strings; the fixtures below match that.

import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => {
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
    setValuesCalls: [],
  };
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// The real compareObjects decides which keys are written; re-typing it here
// would only test the copy.
vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers/utils');
  return {
    API: { put: (...a) => H.put(...a) },
    showError: (...a) => H.showError(...a),
    showSuccess: (...a) => H.showSuccess(...a),
    showWarning: (...a) => H.showWarning(...a),
    compareObjects: actual.compareObjects,
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
      apiRef.current = { setValues: (v) => H.setValuesCalls.push(v) };
    }
    if (getFormApi) getFormApi(apiRef.current);
    return React.createElement(
      'form',
      { 'data-testid': 'form', 'data-values': JSON.stringify(values ?? null) },
      children,
    );
  };

  const makeField = (kind) =>
    function Field({ field, label, placeholder, onChange, disabled, min }) {
      H.handlers[field] = onChange;
      return React.createElement(
        'div',
        {
          'data-testid': `field-${field}`,
          'data-kind': kind,
          'data-disabled': disabled ? 'true' : 'false',
          'data-min': min === undefined ? '' : String(min),
          'data-placeholder':
            typeof placeholder === 'string' ? placeholder : '',
        },
        label,
      );
    };

  Form.Section = ({ text, children }) =>
    React.createElement('section', { 'data-section': text }, text, children);
  Form.Switch = makeField('switch');
  Form.InputNumber = makeField('number');

  return {
    Button,
    Col,
    Form,
    Row,
    Spin,
    Typography: {
      Text: ({ children, ...rest }) =>
        React.createElement('span', rest, children),
    },
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

import SettingsCheckin from './SettingsCheckin';

// Quotas deliberately differ from the component's own defaults (1000/10000)
// so that a payload built from the defaults is distinguishable from one built
// from the loaded options.
const OPTIONS = () => ({
  'checkin_setting.enabled': true,
  'checkin_setting.min_quota': '500',
  'checkin_setting.max_quota': '7500',
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderCheckin = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<SettingsCheckin options={options} refresh={refresh} />);
  return { refresh };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存签到设置').click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');
const disabledOf = (field) =>
  screen.getByTestId(`field-${field}`).dataset.disabled;

beforeEach(() => {
  H.put.mockReset();
  H.put.mockResolvedValue(okResponse());
  H.showError.mockReset();
  H.showSuccess.mockReset();
  H.showWarning.mockReset();
  H.handlers = {};
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds the form from the stored options, not the built-in defaults', () => {
    renderCheckin();
    expect(formValues()).toEqual({
      'checkin_setting.enabled': true,
      'checkin_setting.min_quota': '500',
      'checkin_setting.max_quota': '7500',
    });
  });

  it('pushes the same values into the Semi form api', () => {
    renderCheckin();
    expect(H.setValuesCalls).toHaveLength(1);
    expect(H.setValuesCalls[0]['checkin_setting.max_quota']).toBe('7500');
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderCheckin({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });

  it('floors both quota inputs at zero', () => {
    renderCheckin();
    expect(disabledOf('checkin_setting.enabled')).toBe('false');
    expect(
      screen.getByTestId('field-checkin_setting.min_quota').dataset.min,
    ).toBe('0');
    expect(
      screen.getByTestId('field-checkin_setting.max_quota').dataset.min,
    ).toBe('0');
  });
});

describe('the quota inputs follow the feature switch', () => {
  it('leaves them editable while check-in is enabled', () => {
    renderCheckin();
    expect(disabledOf('checkin_setting.min_quota')).toBe('false');
    expect(disabledOf('checkin_setting.max_quota')).toBe('false');
  });

  it('disables them when check-in is off in the stored options', () => {
    renderCheckin({ ...OPTIONS(), 'checkin_setting.enabled': false });
    expect(disabledOf('checkin_setting.min_quota')).toBe('true');
    expect(disabledOf('checkin_setting.max_quota')).toBe('true');
  });

  it('re-enables them the moment the operator switches check-in on', async () => {
    renderCheckin({ ...OPTIONS(), 'checkin_setting.enabled': false });
    expect(disabledOf('checkin_setting.min_quota')).toBe('true');
    await change('checkin_setting.enabled', true);
    expect(disabledOf('checkin_setting.min_quota')).toBe('false');
    expect(disabledOf('checkin_setting.max_quota')).toBe('false');
  });
});

describe('saving', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderCheckin();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('turning check-in OFF writes the string "false"', async () => {
    renderCheckin();
    await change('checkin_setting.enabled', false);
    await save();
    expect(putBodies()).toEqual([
      { key: 'checkin_setting.enabled', value: 'false' },
    ]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('writes the newly typed quota, not the one that was loaded', async () => {
    renderCheckin();
    await change('checkin_setting.max_quota', 9000);
    await save();
    const bodies = putBodies();
    expect(bodies).toHaveLength(1);
    expect(bodies[0].key).toBe('checkin_setting.max_quota');
    // Asserted WITHOUT String() on purpose. Semi's InputNumber hands onChange a
    // number and handleFieldChange stores it raw, so onSubmit's String() is the
    // only thing keeping a number off the wire. A number reaches the backend as
    // JSON float64 and is stringified there with %f — "9000.000000" — which
    // repo.updateOptionMap feeds to strconv.ParseInt for the int-typed
    // checkin_setting.max_quota field. That parse fails and the branch does
    // `continue`, so the new quota is stored as a row nobody can parse and the
    // running range never changes. Coercing here would make that payload
    // indistinguishable from the correct one.
    expect(bodies[0].value).toBe('9000');
  });

  it('writes one request per changed field and leaves the rest alone', async () => {
    renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await change('checkin_setting.max_quota', 200);
    await save();
    const keys = putBodies()
      .map((b) => b.key)
      .sort();
    expect(keys).toEqual([
      'checkin_setting.max_quota',
      'checkin_setting.min_quota',
    ]);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await change('checkin_setting.enabled', false);
    await save();
    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(String(body.value)).toBe(String(shown[body.key]));
    }
  });

  it('reports success and reloads when every write succeeded', async () => {
    const { refresh } = renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderCheckin();
    H.put.mockRejectedValue(new Error('network down'));
    await change('checkin_setting.min_quota', 100);
    await save();
    await waitFor(() => expect(H.showError).toHaveBeenCalled());
    expect(H.showError).toHaveBeenCalledWith('保存失败，请重试');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('shows the page busy while the writes are in flight and idle after', async () => {
    let release;
    H.put.mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve(okResponse());
        }),
    );
    renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');
    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });

  // DEFECT (correctness): this component never inspects res[i].data.success.
  // SettingsRequestRateLimit in the same console does, so the divergence is not
  // a house convention — a 200 carrying {success:false} is announced as
  // 保存成功 here and the page reloads the unchanged value underneath the
  // operator. Assertion is the fix-neutral invariant: a refused write must not
  // be reported as an accepted one.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderCheckin();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('checkin_setting.min_quota', 100);
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above.
  it('does claim success when the server accepts the write', async () => {
    renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('fields the server has never stored', () => {
  // Positive control: a key present in the load saves normally.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderCheckin();
    await change('checkin_setting.max_quota', 9000);
    await save();
    expect(putBodies().map((b) => b.key)).toContain(
      'checkin_setting.max_quota',
    );
  });

  // DEFECT (correctness): the parent builds its options object from only the
  // rows /api/option/ returned, and this component then rebuilds `inputs` and
  // `inputsRow` from only those keys. compareObjects skips a key missing from
  // either side, so on an installation where check-in has never been
  // configured the operator can type a quota, press save, and be told
  // 你似乎并没有修改什么 — the feature can never be switched on from this page.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    renderCheckin({ 'checkin_setting.min_quota': '500' });
    await change('checkin_setting.enabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'checkin_setting.enabled', value: 'true' },
    ]);
  });
});

describe('the quota range', () => {
  // Positive control: an ordered range saves.
  it('saves a range whose maximum is above its minimum', async () => {
    renderCheckin();
    await change('checkin_setting.min_quota', 100);
    await change('checkin_setting.max_quota', 200);
    await save();
    expect(putBodies()).toHaveLength(2);
  });

  // DEFECT (correctness, validation gap): nothing here — no rule, no check in
  // onSubmit — stops an inverted range being written. min 9000 / max 100 is
  // accepted and reported as 保存成功, and the stored range can never yield a
  // reward. Note the backend's GetCheckinQuotaRange currently has no caller in
  // this repo, so today the damage stops at a stored-but-unusable setting.
  it.skip('refuses a range whose maximum is below its minimum', async () => {
    renderCheckin();
    await change('checkin_setting.min_quota', 9000);
    await change('checkin_setting.max_quota', 100);
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showSuccess).not.toHaveBeenCalled();
  });
});
