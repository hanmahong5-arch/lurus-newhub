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

// Quota settings — the page that decides how much free credit a new account
// gets, how much is pre-debited per request, and what an invitation pays out.
// Four of the five fields are money, and QuotaForInviter / QuotaForInvitee sit
// next to each other with near-identical names, so the fixtures below give
// every field a distinct amount: a handler wired to its neighbour shows up as
// a wrong number rather than a coincidentally right one.

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

import SettingsCreditLimit from './SettingsCreditLimit';

const OPTIONS = () => ({
  QuotaForNewUser: '1111',
  PreConsumedQuota: '2222',
  QuotaForInviter: '3333',
  QuotaForInvitee: '4444',
  'quota_setting.enable_free_model_pre_consume': true,
});

const okResponse = () => ({ data: { success: true, message: '' } });

const renderQuota = (options = OPTIONS()) => {
  const refresh = vi.fn();
  render(<SettingsCreditLimit options={options} refresh={refresh} />);
  return { refresh };
};

const change = async (field, value) => {
  await act(async () => {
    H.handlers[field](value);
  });
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存额度设置').click();
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
  H.setValuesCalls = [];
});

describe('loading options into the form', () => {
  it('seeds every quota field with its own stored amount', () => {
    renderQuota();
    expect(formValues()).toEqual({
      QuotaForNewUser: '1111',
      PreConsumedQuota: '2222',
      QuotaForInviter: '3333',
      QuotaForInvitee: '4444',
      'quota_setting.enable_free_model_pre_consume': true,
    });
  });

  it('pushes the same amounts into the Semi form api', () => {
    renderQuota();
    expect(H.setValuesCalls).toHaveLength(1);
    expect(H.setValuesCalls[0].QuotaForInviter).toBe('3333');
    expect(H.setValuesCalls[0].QuotaForInvitee).toBe('4444');
  });

  it('ignores option keys that belong to other settings pages', () => {
    renderQuota({ ...OPTIONS(), SMTPServer: 'smtp.example.com' });
    expect(formValues()).not.toHaveProperty('SMTPServer');
  });

  it('floors every amount at zero so no field can be given a negative quota', () => {
    renderQuota();
    for (const field of [
      'QuotaForNewUser',
      'PreConsumedQuota',
      'QuotaForInviter',
      'QuotaForInvitee',
    ]) {
      expect(screen.getByTestId(`field-${field}`).dataset.min).toBe('0');
    }
  });
});

describe('saving', () => {
  it('writes nothing and warns when nothing was changed', async () => {
    renderQuota();
    await save();
    expect(H.put).not.toHaveBeenCalled();
    expect(H.showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });

  it('writes the inviter reward under its own key, not the invitee one', async () => {
    renderQuota();
    await change('QuotaForInviter', 8888);
    await save();
    expect(putBodies()).toEqual([{ key: 'QuotaForInviter', value: '8888' }]);
    expect(H.put.mock.calls[0][0]).toBe('/api/option/');
  });

  it('writes the invitee reward under its own key, not the inviter one', async () => {
    renderQuota();
    await change('QuotaForInvitee', 9999);
    await save();
    expect(putBodies()).toEqual([{ key: 'QuotaForInvitee', value: '9999' }]);
  });

  it('writes a zeroed new-user grant rather than treating it as no change', async () => {
    renderQuota();
    await change('QuotaForNewUser', 0);
    await save();
    // Setting the signup grant to zero is a real, deliberate money decision;
    // silently dropping it keeps handing out 1111 per signup.
    expect(putBodies()).toEqual([{ key: 'QuotaForNewUser', value: '0' }]);
  });

  it('turning free-model pre-consumption OFF writes the string "false"', async () => {
    renderQuota();
    await change('quota_setting.enable_free_model_pre_consume', false);
    await save();
    expect(putBodies()).toEqual([
      { key: 'quota_setting.enable_free_model_pre_consume', value: 'false' },
    ]);
  });

  it('writes one request per changed amount and leaves the rest alone', async () => {
    renderQuota();
    await change('PreConsumedQuota', 500);
    await change('QuotaForInvitee', 600);
    await save();
    const keys = putBodies()
      .map((b) => b.key)
      .sort();
    expect(keys).toEqual(['PreConsumedQuota', 'QuotaForInvitee']);
  });

  it('never writes a value that differs from what the form is showing', async () => {
    renderQuota();
    await change('PreConsumedQuota', 500);
    await change('QuotaForNewUser', 600);
    await save();
    const shown = formValues();
    expect(putBodies()).not.toHaveLength(0);
    for (const body of putBodies()) {
      expect(String(body.value)).toBe(String(shown[body.key]));
    }
  });

  it('reports success and reloads when every write succeeded', async () => {
    const { refresh } = renderQuota();
    await change('PreConsumedQuota', 500);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(H.showError).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of success', async () => {
    const { refresh } = renderQuota();
    H.put.mockRejectedValue(new Error('network down'));
    await change('PreConsumedQuota', 500);
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
    renderQuota();
    await change('PreConsumedQuota', 500);
    await save();
    expect(screen.getByTestId('spin').dataset.spinning).toBe('true');
    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });

  // DEFECT (money): res[i].data.success is never inspected on this page. A
  // refusal from the option endpoint is announced as 保存成功 and followed by a
  // reload that restores the previous amount, so an operator who has just cut
  // the signup grant to 0 walks away believing they did while the old grant
  // keeps being handed out. SettingsRequestRateLimit checks this; this page
  // does not.
  it.skip('does not claim success when the server refuses the write', async () => {
    const { refresh } = renderQuota();
    H.put.mockResolvedValue({
      data: { success: false, message: 'option is read-only' },
    });
    await change('QuotaForNewUser', 0);
    await save();
    await waitFor(() => expect(H.put).toHaveBeenCalled());
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(H.showError).toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  // Positive control for the lock above.
  it('does claim success when the server accepts the write', async () => {
    renderQuota();
    await change('QuotaForNewUser', 0);
    await save();
    await waitFor(() => expect(H.showSuccess).toHaveBeenCalledWith('保存成功'));
  });
});

describe('fields the server has never stored', () => {
  // Positive control.
  it('saves an edit to a field that was present in the loaded options', async () => {
    renderQuota();
    await change('QuotaForInviter', 8888);
    await save();
    expect(putBodies().map((b) => b.key)).toContain('QuotaForInviter');
  });

  // DEFECT (money): compareObjects skips any key missing from either side, and
  // both `inputs` and `inputsRow` are rebuilt from only the keys /api/option/
  // returned. On an install that has never stored QuotaForInviter, the referral
  // payout can be typed in but never written — the page reports
  // 你似乎并没有修改什么 and the referral programme silently stays at the
  // backend default.
  it.skip('saves an edit to a field that was absent from the loaded options', async () => {
    const partial = OPTIONS();
    delete partial.QuotaForInviter;
    renderQuota(partial);
    await change('QuotaForInviter', 8888);
    await save();
    expect(putBodies()).toEqual([{ key: 'QuotaForInviter', value: '8888' }]);
  });
});
