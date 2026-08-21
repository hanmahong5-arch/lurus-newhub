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

// io.net deployment settings: one credential and one enable switch, plus a
// "test connection" round trip whose whole job is turning a backend error
// string into something an operator can act on. Most of the branches here are
// in that error funnel, so every localisation case is paired with the raw
// pass-through case — otherwise "translates the message" and "always shows the
// same message" look identical.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

const H = vi.hoisted(() => ({
  handlers: new Map(),
}));

vi.mock('@douyinfe/semi-ui', () => {
  const makeField = (kind) => {
    const Field = ({ field, label, onChange, disabled, placeholder, mode }) => {
      H.handlers.set(field, onChange);
      return React.createElement('div', {
        'data-testid': `field-${field}`,
        'data-kind': kind,
        'data-label': typeof label === 'string' ? label : '',
        'data-disabled': String(!!disabled),
        'data-mode': mode ?? '',
        'data-placeholder': typeof placeholder === 'string' ? placeholder : '',
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
      React.createElement('div', null, text),
      children,
    );
  Form.Switch = makeField('switch');
  Form.Input = makeField('input');
  Form.TextArea = makeField('textarea');
  Form.InputNumber = makeField('number');

  // `loading` and `disabled` are surfaced rather than swallowed: they are the
  // only signal that the test-connection button is inert while a probe is in
  // flight or while the integration is switched off.
  const Button = ({ children, onClick, disabled, loading, icon: _icon }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        disabled: !!disabled,
        'data-loading': String(!!loading),
      },
      children,
    );
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Card = ({ title, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'card' },
      React.createElement('div', null, title),
      children,
    );
  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);
  const Typography = {
    Text: ({ children }) => React.createElement('span', null, children),
  };

  return { Form, Button, Spin, Card, Row, Col, Typography };
});

vi.mock('lucide-react', () => ({
  Server: () => React.createElement('i', { 'data-testid': 'icon-server' }),
  Cloud: () => React.createElement('i', { 'data-testid': 'icon-cloud' }),
  Zap: () => React.createElement('i', { 'data-testid': 'icon-zap' }),
  ArrowUpRight: () => React.createElement('i', { 'data-testid': 'icon-arrow' }),
}));

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

import SettingModelDeployment from './SettingModelDeployment';
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

const KEY_FIELD = 'model_deployment.ionet.api_key';
const ENABLED_FIELD = 'model_deployment.ionet.enabled';

const emit = (field, value) =>
  act(() => {
    H.handlers.get(field)(value);
  });

const putBodies = () => API.put.mock.calls.map(([, body]) => body);
const applied = () => JSON.parse(screen.getByTestId('form').dataset.applied);

const renderPage = (options = {}) => {
  const refresh = vi.fn();
  render(<SettingModelDeployment options={options} refresh={refresh} />);
  return { refresh };
};

const save = async () => {
  await act(async () => {
    screen.getByText('保存设置').click();
  });
};

const testConnection = async () => {
  await act(async () => {
    screen.getByText('测试连接').click();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.handlers.clear();
  compareObjects.mockImplementation(realCompareObjects);
  API.put.mockResolvedValue({ data: { success: true } });
  API.post.mockResolvedValue({ data: { success: true } });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('hydration', () => {
  it('falls back to the shipped defaults when nothing is stored', () => {
    renderPage({});
    expect(applied()).toEqual({
      [KEY_FIELD]: '',
      [ENABLED_FIELD]: false,
    });
  });

  it('takes the stored values when they exist', () => {
    renderPage({ [KEY_FIELD]: 'io-abc', [ENABLED_FIELD]: true });
    expect(applied()).toEqual({ [KEY_FIELD]: 'io-abc', [ENABLED_FIELD]: true });
  });

  it('ignores option keys belonging to other setting pages', () => {
    renderPage({ [ENABLED_FIELD]: true, 'claude.default_max_tokens': '{}' });
    expect(applied()).not.toHaveProperty('claude.default_max_tokens');
  });

  it('does nothing at all when the parent has not loaded options yet', () => {
    // The `if (props.options)` guard: rendering before the parent's fetch
    // resolves must not blank the form out with defaults.
    render(<SettingModelDeployment options={undefined} refresh={vi.fn()} />);
    expect(screen.getByTestId('form').dataset.applied).toBe('null');
  });

  it('masks the credential field', () => {
    renderPage({ [ENABLED_FIELD]: true });
    expect(screen.getByTestId(`field-${KEY_FIELD}`).dataset.mode).toBe(
      'password',
    );
  });
});

describe('the enable switch gates the credential controls', () => {
  it('locks the key box and the probe button while io.net is off', () => {
    renderPage({ [ENABLED_FIELD]: false });
    expect(screen.getByTestId(`field-${KEY_FIELD}`).dataset.disabled).toBe(
      'true',
    );
    expect(screen.getByText('测试连接')).toBeDisabled();
  });

  it('unlocks them while io.net is on', () => {
    renderPage({ [ENABLED_FIELD]: true });
    expect(screen.getByTestId(`field-${KEY_FIELD}`).dataset.disabled).toBe(
      'false',
    );
    expect(screen.getByText('测试连接')).not.toBeDisabled();
  });

  it('follows the switch without waiting for a save', () => {
    renderPage({ [ENABLED_FIELD]: false });
    emit(ENABLED_FIELD, true);
    expect(screen.getByTestId(`field-${KEY_FIELD}`).dataset.disabled).toBe(
      'false',
    );
  });
});

describe('test connection request', () => {
  it('sends the trimmed key and asks the client not to swallow the error', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    emit(KEY_FIELD, '  io-secret  ');
    await testConnection();

    expect(API.post).toHaveBeenCalledWith(
      '/api/deployments/settings/test-connection',
      { api_key: 'io-secret' },
      { skipErrorHandler: true },
    );
  });

  it('sends an empty body when no key is typed, so the stored one is probed', async () => {
    renderPage({ [ENABLED_FIELD]: true, [KEY_FIELD]: '' });
    await testConnection();
    expect(API.post.mock.calls[0][1]).toEqual({});
  });

  it('treats a whitespace-only key as no key at all', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    emit(KEY_FIELD, '   ');
    await testConnection();
    expect(API.post.mock.calls[0][1]).toEqual({});
  });

  it('reports a successful probe', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockResolvedValue({ data: { success: true } });
    await testConnection();
    expect(showSuccess).toHaveBeenCalledWith(
      'API Key 验证成功！连接到 io.net 服务正常',
    );
    expect(showError).not.toHaveBeenCalled();
  });

  it('shows the button busy while the probe is in flight and idle after', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    let release;
    API.post.mockReturnValue(
      new Promise((resolve) => {
        release = () => resolve({ data: { success: true } });
      }),
    );

    await act(async () => {
      screen.getByText('测试连接').click();
    });
    expect(screen.getByText('连接测试中...').dataset.loading).toBe('true');
    expect(screen.queryByText('测试连接')).toBeNull();

    await act(async () => {
      release();
    });
    await waitFor(() =>
      expect(screen.getByText('测试连接').dataset.loading).toBe('false'),
    );
  });
});

describe('test connection error funnel', () => {
  it.each([
    ['invalid request payload', '请求参数无效'],
    ['api_key is required', '请先填写 API Key'],
    ['failed to validate api key', 'API Key 验证失败'],
  ])('localises the backend message %s', async (raw, localised) => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockResolvedValue({ data: { success: false, message: raw } });
    await testConnection();
    expect(showError).toHaveBeenCalledWith(localised);
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('passes an unrecognised backend message straight through', async () => {
    // The negative half: if the funnel always emitted a canned string, the
    // three cases above would still pass and this one would not.
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockResolvedValue({
      data: { success: false, message: 'quota exhausted on io.cloud project' },
    });
    await testConnection();
    expect(showError).toHaveBeenCalledWith(
      'quota exhausted on io.cloud project',
    );
  });

  it('falls back to a generic failure when the body carries no message', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockResolvedValue({ data: { success: false } });
    await testConnection();
    expect(showError).toHaveBeenCalledWith('API Key 验证失败');
  });

  it('treats a bodyless response as a failure rather than a success', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockResolvedValue(undefined);
    await testConnection();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith('API Key 验证失败');
  });

  it('calls out a network failure separately from an API rejection', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    const err = new Error('Network Error');
    err.code = 'ERR_NETWORK';
    API.post.mockRejectedValue(err);
    await testConnection();
    expect(showError).toHaveBeenCalledWith(
      '网络连接失败，请检查网络设置或稍后重试',
    );
  });

  it('localises a message carried on a thrown response body', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockRejectedValue({
      response: { data: { message: 'api_key is required' } },
    });
    await testConnection();
    expect(showError).toHaveBeenCalledWith('测试失败：请先填写 API Key');
  });

  it('prefers the response body message over the thrown error message', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockRejectedValue({
      message: 'Request failed with status code 500',
      response: { data: { message: 'upstream is down' } },
    });
    await testConnection();
    expect(showError).toHaveBeenCalledWith('测试失败：upstream is down');
  });

  it('falls back to the thrown error message when there is no body', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockRejectedValue(new Error('socket hang up'));
    await testConnection();
    expect(showError).toHaveBeenCalledWith('测试失败：socket hang up');
  });

  it('falls back to an unknown-error label when the throw carries nothing', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockRejectedValue({});
    await testConnection();
    expect(showError).toHaveBeenCalledWith('测试失败：未知错误');
  });

  it('releases the probe button after a rejection', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    API.post.mockRejectedValue(new Error('nope'));
    await testConnection();
    await waitFor(() =>
      expect(screen.getByText('测试连接').dataset.loading).toBe('false'),
    );
  });
});

describe('saving', () => {
  it('refuses an empty save and does not touch the option endpoint', async () => {
    renderPage({ [ENABLED_FIELD]: true, [KEY_FIELD]: 'io-abc' });
    await save();
    expect(showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
    expect(API.put).not.toHaveBeenCalled();
  });

  it('sends only the field that changed', async () => {
    const { refresh } = renderPage({
      [ENABLED_FIELD]: true,
      [KEY_FIELD]: 'io-abc',
    });
    emit(KEY_FIELD, 'io-xyz');
    await save();

    expect(putBodies()).toEqual([{ key: KEY_FIELD, value: 'io-xyz' }]);
    await waitFor(() => expect(showSuccess).toHaveBeenCalledWith('保存成功'));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('stringifies the enable switch rather than posting a raw boolean', async () => {
    renderPage({ [ENABLED_FIELD]: true });
    emit(ENABLED_FIELD, false);
    await save();
    expect(putBodies()[0]).toEqual({ key: ENABLED_FIELD, value: 'false' });
    expect(typeof putBodies()[0].value).toBe('string');
  });

  it('does not resend an unchanged field on a second save', async () => {
    // This page refreshes its baseline (setInputsRow) after a successful save,
    // which is what makes the second click a genuine no-op.
    renderPage({ [ENABLED_FIELD]: false });
    emit(ENABLED_FIELD, true);
    await save();
    await waitFor(() => expect(showSuccess).toHaveBeenCalledTimes(1));

    API.put.mockClear();
    await save();
    expect(API.put).not.toHaveBeenCalled();
    expect(showWarning).toHaveBeenCalledWith('你似乎并没有修改什么');
  });

  it('keeps the old baseline when the save failed, so a retry still sends', async () => {
    renderPage({ [ENABLED_FIELD]: false });
    API.put.mockRejectedValue(new Error('offline'));
    emit(ENABLED_FIELD, true);
    await save();
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('保存失败，请重试'),
    );

    API.put.mockClear();
    API.put.mockResolvedValue({ data: { success: true } });
    await save();
    expect(putBodies()).toEqual([{ key: ENABLED_FIELD, value: 'true' }]);
  });

  it('reports a partial failure and does not claim success', async () => {
    const { refresh } = renderPage({
      [ENABLED_FIELD]: false,
      [KEY_FIELD]: 'io-abc',
    });
    API.put
      .mockResolvedValueOnce({ data: { success: true } })
      .mockResolvedValueOnce(undefined);
    emit(ENABLED_FIELD, true);
    emit(KEY_FIELD, 'io-xyz');
    await save();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('部分保存失败，请重试'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('never reports success for a single request the server did not accept', async () => {
    const { refresh } = renderPage({ [ENABLED_FIELD]: false });
    API.put.mockResolvedValue(undefined);
    emit(ENABLED_FIELD, true);
    await save();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it('drops the busy spinner once the save settles', async () => {
    renderPage({ [ENABLED_FIELD]: false });
    emit(ENABLED_FIELD, true);
    await save();
    await waitFor(() =>
      expect(screen.getByTestId('spin').dataset.spinning).toBe('false'),
    );
  });
});

describe('DEFECT: the io.net link is opened with a live window opener', () => {
  // window.open(url, '_blank') with no feature string leaves the new tab a
  // usable `window.opener` handle back to the console. Anything served from
  // ai.io.net — or anything that ever manages to sit on that hostname — can
  // then navigate the admin console tab to a look-alike login. The fix is a
  // third argument containing noopener (and noreferrer); nothing below asserts
  // its absence, so adding it cannot turn this file red.

  let openSpy;
  beforeEach(() => {
    openSpy = vi.spyOn(window, 'open').mockImplementation(() => null);
  });

  const clickLink = () =>
    act(() => {
      screen.getByText('前往 io.net API Keys').click();
    });

  it('sends the operator to the io.net API keys page in a new tab', () => {
    renderPage({ [ENABLED_FIELD]: true });
    clickLink();
    expect(openSpy).toHaveBeenCalledTimes(1);
    // Positional reads, so a fix that appends a third argument stays green.
    expect(openSpy.mock.calls[0][0]).toBe('https://ai.io.net/ai/api-keys');
    expect(openSpy.mock.calls[0][1]).toBe('_blank');
  });

  it('offers the link even while the integration is switched off', () => {
    renderPage({ [ENABLED_FIELD]: false });
    expect(screen.getByText('前往 io.net API Keys')).not.toBeDisabled();
  });

  // Verified red on 2026-08-20: un-skipped, the call had only two arguments
  // (`expected "open" to be called with arguments: [ …, …, StringContaining
  // "noopener" ]`).
  it('severs the opener handle when opening the io.net key page', () => {
    renderPage({ [ENABLED_FIELD]: true });
    clickLink();
    expect(openSpy).toHaveBeenCalledWith(
      'https://ai.io.net/ai/api-keys',
      '_blank',
      expect.stringContaining('noopener'),
    );
  });
});
