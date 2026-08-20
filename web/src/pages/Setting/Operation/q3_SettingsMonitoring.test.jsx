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

// Channel monitoring: automatic disable/enable of upstream channels, the
// keyword list that triggers a disable, and the periodic test interval.
// This page can take every channel offline, so the round trip matters more
// than the rendering: the value that leaves the form is the one that decides
// whether traffic keeps flowing.
//
// Note the interval field is the only one in the whole Setting tree that runs
// its input through parseInt() rather than String(); that asymmetry gets its
// own lock below.

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
    function Field({
      field,
      label,
      extraText,
      placeholder,
      onChange,
      disabled,
      min,
      suffix,
    }) {
      H.handlers[field] = onChange;
      return React.createElement(
        'div',
        {
          'data-testid': `field-${field}`,
          'data-kind': kind,
          'data-disabled': disabled ? 'true' : 'false',
          'data-min': min === undefined ? '' : String(min),
          'data-suffix': typeof suffix === 'string' ? suffix : '',
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

  return {
    Button,
    Col,
    Form,
    Row,
    Spin,
    Toast: {
      error: vi.fn(),
      success: vi.fn(),
      warning: vi.fn(),
      info: vi.fn(),
    },
    Pagination: () => null,
  };
});

import SettingsMonitoring from './SettingsMonitoring';

// Distinct values everywhere, and the two auto-disable/auto-enable switches
// start on OPPOSITE settings so a crossed handler is visible.
const OPTIONS = () => ({
  ChannelDisableThreshold: '55',
  QuotaRemindThreshold: '77777',
  AutomaticDisableChannelEnabled: true,
  AutomaticEnableChannelEnabled: false,
  AutomaticDisableKeywords: 'insufficient_quota\ninvalid_api_key',
  'monitor_setting.auto_test_channel_enabled': true,
  'monitor_setting.auto_test_channel_minutes': '30',
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderMon = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<SettingsMonitoring options={options} refresh={refresh} />);
  return { refresh };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存监控设置').click();
  });
};

const putBodies = () => H.put.mock.calls.map(([, body]) => body);
const formValues = () =>
  JSON.parse(screen.getByTestId('form').dataset.values || 'null');
// What the HTTP layer would actually put on the wire for a value.
const onTheWire = (value) => JSON.parse(JSON.stringify({ v: value })).v;

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
  it('seeds every monitoring field from the stored options', () => {
    renderMon();
    expect(formValues()).toEqual(OPTIONS());
  });

  it('pushes the same values into the Semi form api', () => {
    renderMon();
    expect(H.setValuesCalls).toHaveLength(1);
    expect(H.setValuesCalls[0].AutomaticDisableKeywords).toBe(
      'insufficient_quota\ninvalid_api_key',
    );
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderMon({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });

  it('will not let the periodic test interval be set below one minute', () => {
    renderMon();
    expect(
      screen.getByTestId('field-monitor_setting.auto_test_channel_minutes')
        .dataset.min,
    ).toBe('1');
  });
});

describe('saving', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderMon();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('turning automatic channel disabling OFF writes the string "false"', async () => {
    renderMon();
    await change('AutomaticDisableChannelEnabled', false);
    await save();
    expect(putBodies()).toEqual([
      { key: 'AutomaticDisableChannelEnabled', value: 'false' },
    ]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('turning automatic channel enabling ON writes only that key', async () => {
    renderMon();
    await change('AutomaticEnableChannelEnabled', true);
    await save();
    expect(putBodies()).toEqual([
      { key: 'AutomaticEnableChannelEnabled', value: 'true' },
    ]);
  });

  it('writes the newly typed response-time threshold, not the loaded one', async () => {
    renderMon();
    await change('ChannelDisableThreshold', 120);
    await save();
    const bodies = putBodies();
    expect(bodies).toHaveLength(1);
    expect(bodies[0].key).toBe('ChannelDisableThreshold');
    // Asserted WITHOUT String(): the handler's String(value) is the only thing
    // keeping Semi's numeric onChange payload off the wire, and coercing here
    // would make a dropped String() indistinguishable from the correct call.
    // ChannelDisableThreshold itself survives a number (the backend parses it
    // with ParseFloat), but the sibling pages that share this shape do not —
    // see the note on the period field in q3_SettingsRequestRateLimit.
    expect(bodies[0].value).toBe('120');
  });

  it('writes the keyword list verbatim, newlines and all', async () => {
    renderMon();
    const list = 'rate_limit\nbilling_hard_limit_reached\n  padded  ';
    await change('AutomaticDisableKeywords', list);
    await save();
    expect(putBodies()).toEqual([
      { key: 'AutomaticDisableKeywords', value: list },
    ]);
  });

  it('writes an emptied keyword list rather than treating it as no change', async () => {
    renderMon();
    await change('AutomaticDisableKeywords', '');
    await save();
    // Clearing the list stops channels being auto-disabled; dropping the edit
    // would leave the old keywords still taking channels offline.
    expect(putBodies()).toEqual([
      { key: 'AutomaticDisableKeywords', value: '' },
    ]);
  });

  it('writes one request per changed field and leaves the rest alone', async () => {
    renderMon();
    await change('QuotaRemindThreshold', 1000);
    await change('AutomaticEnableChannelEnabled', true);
    await save();
    const keys = putBodies()
      .map((b) => b.key)
      .sort();
    expect(keys).toEqual([
      'AutomaticEnableChannelEnabled',
      'QuotaRemindThreshold',
    ]);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderMon();
    await change('QuotaRemindThreshold', 1000);
    await change('ChannelDisableThreshold', 15);
    await save();
    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(String(body.value)).toBe(String(shown[body.key]));
    }
  });

  it('reports success and reloads when every write succeeded', async () => {
    const { refresh } = renderMon();
    await change('QuotaRemindThreshold', 1000);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderMon();
    H.put.mockRejectedValue(new Error('network down'));
    await change('QuotaRemindThreshold', 1000);
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
    renderMon();
    await change('QuotaRemindThreshold', 1000);
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');
    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });

  // DEFECT (correctness): res[i].data.success is never inspected. A refused
  // write is announced as 保存成功 and followed by a reload that restores the
  // old value — the operator believes automatic channel disabling is now off
  // while it is still taking upstreams offline.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderMon();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('AutomaticDisableChannelEnabled', false);
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above.
  it('does claim success when the server accepts the write', async () => {
    renderMon();
    await change('AutomaticDisableChannelEnabled', false);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('the periodic channel test interval', () => {
  // Positive control: a real number goes through as a number.
  it('writes a numeric interval the operator typed', async () => {
    renderMon();
    await change('monitor_setting.auto_test_channel_minutes', 45);
    await save();
    const bodies = putBodies();
    expect(bodies).toHaveLength(1);
    expect(bodies[0].key).toBe('monitor_setting.auto_test_channel_minutes');
    // Deliberately fix-neutral: passes whether the interval is written as 45 or
    // as '45'. The lock below is what pins which of the two is correct.
    expect(Number(bodies[0].value)).toBe(45);
  });

  // DEFECT (correctness): every other option on every page in this tree leaves
  // the form as a STRING; this one leaves as a number, because the handler is
  // the only one using parseInt(value) instead of String(value). The backend
  // takes value as `any` and stringifies a JSON number with %f, so the option
  // row is written as "45.000000" rather than "45", and that is also what the
  // console reads back on the next load. Here it survives only because
  // MonitorSetting.AutoTestChannelMinutes happens to be a float64 and
  // strconv.ParseFloat accepts it — the identical shape on an int-typed field
  // is discarded outright (see the note in q3_SettingsCheckin) or, on the flat
  // keys, resolves to 0 via a discarded strconv.Atoi error. Asserting the
  // string is the invariant that holds after the parseInt is replaced.
  it.skip('writes the interval as a string like every other option', async () => {
    renderMon();
    await change('monitor_setting.auto_test_channel_minutes', 45);
    await save();
    expect(putBodies()).toEqual([
      { key: 'monitor_setting.auto_test_channel_minutes', value: '45' },
    ]);
  });

  // DEFECT (correctness): this field alone is stored through parseInt(value).
  // Semi's InputNumber hands onChange an empty string when the operator clears
  // the box, parseInt('') is NaN, and NaN survives all the way into the request
  // body — where JSON serialisation turns it into `null`. The option row is
  // then written with a null value and the periodic channel test silently
  // stops running on the next boot. Every other numeric field on the page uses
  // String(value) and is immune. The assertion is the invariant that survives
  // any fix (refuse to save, or coerce): whatever is written must not reach the
  // wire as null.
  it.skip('never writes a null interval when the field is cleared', async () => {
    renderMon();
    await change('monitor_setting.auto_test_channel_minutes', '');
    await save();
    for (const body of putBodies()) {
      if (body.key === 'monitor_setting.auto_test_channel_minutes') {
        expect(onTheWire(body.value)).not.toBeNull();
      }
    }
  });
});

describe('fields the server has never stored', () => {
  // Positive control.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderMon();
    await change('AutomaticDisableKeywords', 'quota');
    await save();
    expect(putBodies().map((b) => b.key)).toContain('AutomaticDisableKeywords');
  });

  // DEFECT (correctness): compareObjects skips any key missing from either
  // side, and both `inputs` and `inputsRow` are rebuilt from only the keys
  // /api/option/ returned. On an install that has never stored
  // AutomaticDisableKeywords, the operator can fill the box, press save, and be
  // told 你似乎并没有修改什么 — auto-disable keywords can never be configured.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    const partial = OPTIONS();
    delete partial.AutomaticDisableKeywords;
    renderMon(partial);
    await change('AutomaticDisableKeywords', 'insufficient_quota');
    await save();
    expect(putBodies()).toEqual([
      { key: 'AutomaticDisableKeywords', value: 'insufficient_quota' },
    ]);
  });
});
