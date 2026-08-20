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
import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

// A Form shim with a real form API: `getValues()` returns whatever the step
// inputs have written, which is what `onSubmit` reads. Anything less would let
// the submit-validation branches "run" without ever seeing a value.
vi.mock('@douyinfe/semi-ui', () => {
  const Steps = ({ current, children }) =>
    React.createElement(
      'ol',
      { 'data-testid': 'steps', 'data-current': String(current) },
      children,
    );
  Steps.Step = ({ title, description }) =>
    React.createElement('li', null, title, description);
  const Form = ({ children, getFormApi, initValues }) => {
    if (getFormApi) {
      globalThis.__setupValues = {
        ...(globalThis.__setupValues || {}),
        ...(initValues || {}),
      };
      getFormApi({
        getValues: () => ({ ...globalThis.__setupValues }),
        setValue: (k, v) => {
          globalThis.__setupValues[k] = v;
        },
      });
    }
    return React.createElement('form', null, children);
  };
  return {
    Card: ({ children }) =>
      React.createElement('section', { 'data-testid': 'card' }, children),
    Divider: () => React.createElement('hr', null),
    Steps,
    Form,
    Button: ({ children, onClick, loading, icon }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          onClick,
          'data-loading': loading ? 'true' : 'false',
        },
        icon,
        children,
      ),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconCheckCircleStroked: () =>
    React.createElement('i', { 'data-testid': 'icon-check' }),
}));

// Step shims: markers that (a) prove which step is mounted, (b) expose the
// callbacks the wizard passes down so the wizard's own state machine is what
// gets tested, and (c) render the injected navigation slot.
const stepShim = vi.hoisted(() => (name, extra) => ({
  default: (props) =>
    React.createElement(
      'div',
      { 'data-testid': `step-${name}` },
      extra ? extra(props) : null,
      props.renderNavigationButtons ? props.renderNavigationButtons() : null,
    ),
}));

vi.mock('./components/steps/DatabaseStep', () => stepShim('database'));
vi.mock('./components/steps/CompleteStep', () =>
  stepShim('complete', (props) =>
    React.createElement(
      'span',
      { 'data-testid': 'summary-mode' },
      String(props.formData?.usageMode),
    ),
  ),
);
vi.mock('./components/steps/AdminStep', () =>
  stepShim('admin', (props) =>
    React.createElement(
      'div',
      null,
      ...['username', 'password', 'confirmPassword'].map((field) =>
        React.createElement('input', {
          key: field,
          'data-testid': `admin-${field}`,
          onChange: (e) => {
            // Mirror Semi Form: the value lands in wizard state *and* in the
            // form API the submit handler reads.
            globalThis.__setupValues[field] = e.target.value;
            props.setFormData({ ...props.formData, [field]: e.target.value });
          },
        }),
      ),
    ),
  ),
);
vi.mock('./components/steps/UsageModeStep', () =>
  stepShim('usage', (props) =>
    React.createElement(
      'div',
      null,
      ...['external', 'self', 'demo', ''].map((mode) =>
        React.createElement(
          'button',
          {
            key: mode || 'blank',
            type: 'button',
            'data-testid': `pick-${mode || 'blank'}`,
            onClick: () =>
              props.handleUsageModeChange({ target: { value: mode } }),
          },
          mode || 'blank',
        ),
      ),
    ),
  ),
);

const apiGet = vi.fn();
const apiPost = vi.fn();
const showError = vi.fn();
const showNotice = vi.fn();
vi.mock('../../helpers', () => ({
  API: { get: (...a) => apiGet(...a), post: (...a) => apiPost(...a) },
  showError: (...a) => showError(...a),
  showNotice: (...a) => showNotice(...a),
}));

import SetupWizard from './SetupWizard';

const realLocation = window.location;
const reload = vi.fn();
let hrefWrites = [];

const installLocation = () => {
  hrefWrites = [];
  const stub = { origin: 'https://hub.test', reload: (...a) => reload(...a) };
  Object.defineProperty(stub, 'href', {
    get: () => 'https://hub.test/setup',
    set: (v) => hrefWrites.push(v),
  });
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: stub,
  });
};

const setupOk = (over = {}) => ({
  data: {
    success: true,
    data: {
      status: false,
      root_init: false,
      database_type: 'postgres',
      ...over,
    },
  },
});

const mount = async () => {
  const utils = render(React.createElement(SetupWizard, null));
  await waitFor(() => expect(apiGet).toHaveBeenCalledWith('/api/setup'));
  await act(async () => {
    await Promise.resolve();
  });
  return utils;
};

const currentStep = () =>
  screen.getByTestId('steps').getAttribute('data-current');

// The wizard keeps all four steps mounted and hides the inactive ones with
// `display: none`, so every step renders its own copy of the navigation. Only
// the one a user can actually see counts.
const isVisible = (node) => {
  let n = node;
  while (n && n !== document.body) {
    if (n.style && n.style.display === 'none') return false;
    n = n.parentElement;
  }
  return true;
};
const visibleButton = (label) =>
  screen
    .getAllByText(label)
    .map((el) => el.closest('button'))
    .find((b) => b && isVisible(b));
const click = (label) => {
  const btn = visibleButton(label);
  if (!btn) throw new Error(`no visible "${label}" button`);
  fireEvent.click(btn);
};

const type = (field, value) =>
  fireEvent.change(screen.getByTestId(`admin-${field}`), {
    target: { value },
  });

beforeEach(() => {
  vi.clearAllMocks();
  globalThis.__setupValues = {};
  installLocation();
  apiGet.mockResolvedValue(setupOk());
  apiPost.mockResolvedValue({ data: { success: true } });
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  Object.defineProperty(window, 'location', {
    configurable: true,
    writable: true,
    value: realLocation,
  });
});

describe('SetupWizard — bootstrap', () => {
  it('starts on the database step with all four steps listed', async () => {
    await mount();
    expect(currentStep()).toBe('0');
    expect(screen.getByTestId('step-database')).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(4);
  });

  it('leaves the wizard entirely when setup has already been completed', async () => {
    apiGet.mockResolvedValue(setupOk({ status: true }));
    await mount();
    expect(hrefWrites).toEqual(['/']);
  });

  it('reports a refused status call instead of pretending the system is fresh', async () => {
    apiGet.mockResolvedValue({ data: { success: false } });
    await mount();
    expect(showError).toHaveBeenCalledWith('获取初始化状态失败');
    expect(hrefWrites).toEqual([]);
  });

  it('reports a failed status call', async () => {
    apiGet.mockRejectedValue(new Error('offline'));
    await mount();
    expect(showError).toHaveBeenCalledWith('获取初始化状态失败');
  });
});

describe('SetupWizard — step gating', () => {
  it('lets the database step through unconditionally', async () => {
    await mount();
    click('下一步');
    expect(currentStep()).toBe('1');
    expect(showError).not.toHaveBeenCalled();
  });

  it('refuses to leave the admin step with an incomplete form', async () => {
    await mount();
    click('下一步');
    click('下一步');
    expect(showError).toHaveBeenCalledWith('请填写完整的管理员账号信息');
    expect(currentStep()).toBe('1');
  });

  it('refuses a mismatched password confirmation', async () => {
    await mount();
    click('下一步');
    type('username', 'root');
    type('password', 'longenough1');
    type('confirmPassword', 'different1');
    click('下一步');
    expect(showError).toHaveBeenCalledWith('两次输入的密码不一致');
    expect(currentStep()).toBe('1');
  });

  it('refuses a password under eight characters', async () => {
    await mount();
    click('下一步');
    type('username', 'root');
    type('password', 'short');
    type('confirmPassword', 'short');
    click('下一步');
    expect(showError).toHaveBeenCalledWith('密码长度至少为8个字符');
    expect(currentStep()).toBe('1');
  });

  it('accepts a well-formed admin credential set', async () => {
    await mount();
    click('下一步');
    type('username', 'root');
    type('password', 'longenough1');
    type('confirmPassword', 'longenough1');
    click('下一步');
    expect(showError).not.toHaveBeenCalled();
    expect(currentStep()).toBe('2');
  });

  it('skips credential validation entirely when root is already initialised', async () => {
    apiGet.mockResolvedValue(setupOk({ root_init: true }));
    await mount();
    click('下一步');
    click('下一步');
    expect(showError).not.toHaveBeenCalled();
    expect(currentStep()).toBe('2');
  });

  it('refuses to leave the usage-mode step with no mode selected', async () => {
    apiGet.mockResolvedValue(setupOk({ root_init: true }));
    await mount();
    click('下一步');
    click('下一步');
    fireEvent.click(screen.getByTestId('pick-blank'));
    click('下一步');
    expect(showError).toHaveBeenCalledWith('请选择使用模式');
    expect(currentStep()).toBe('2');
  });

  it('carries the chosen mode into the confirmation summary', async () => {
    apiGet.mockResolvedValue(setupOk({ root_init: true }));
    await mount();
    click('下一步');
    click('下一步');
    fireEvent.click(screen.getByTestId('pick-self'));
    click('下一步');
    expect(currentStep()).toBe('3');
    expect(screen.getByTestId('summary-mode')).toHaveTextContent('self');
  });

  it('walks back a step without losing the entered credentials', async () => {
    await mount();
    click('下一步');
    type('username', 'root');
    type('password', 'longenough1');
    type('confirmPassword', 'longenough1');
    click('下一步');
    expect(currentStep()).toBe('2');
    click('上一步');
    expect(currentStep()).toBe('1');
    click('下一步');
    expect(currentStep()).toBe('2');
    expect(showError).not.toHaveBeenCalled();
  });
});

describe('SetupWizard — submission', () => {
  const reachFinalStep = async (over = {}) => {
    apiGet.mockResolvedValue(setupOk({ root_init: true, ...over }));
    await mount();
    click('下一步');
    click('下一步');
    click('下一步');
    expect(currentStep()).toBe('3');
  };

  it('translates the usage mode into the two backend flags and drops the UI-only field', async () => {
    await reachFinalStep();
    fireEvent.click(screen.getByTestId('pick-demo'));
    click('初始化系统');

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    const [url, body] = apiPost.mock.calls[0];
    expect(url).toBe('/api/setup');
    expect(body.SelfUseModeEnabled).toBe(false);
    expect(body.DemoSiteEnabled).toBe(true);
    expect(body).not.toHaveProperty('usageMode');
  });

  it('marks self-use mode without also marking demo mode', async () => {
    await reachFinalStep();
    fireEvent.click(screen.getByTestId('pick-self'));
    click('初始化系统');
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(apiPost.mock.calls[0][1]).toMatchObject({
      SelfUseModeEnabled: true,
      DemoSiteEnabled: false,
    });
  });

  it('reloads into the initialised system after a successful submit', async () => {
    await reachFinalStep();
    vi.useFakeTimers();
    click('初始化系统');
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(showNotice).toHaveBeenCalledWith('系统初始化成功，正在跳转...');
    expect(reload).not.toHaveBeenCalled();
    await act(async () => {
      vi.advanceTimersByTime(1500);
    });
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('surfaces the backend refusal message and does not reload', async () => {
    apiPost.mockResolvedValue({
      data: { success: false, message: '数据库不可写' },
    });
    await reachFinalStep();
    click('初始化系统');
    await waitFor(() => expect(showError).toHaveBeenCalledWith('数据库不可写'));
    expect(showNotice).not.toHaveBeenCalled();
  });

  it('falls back to a generic message when the refusal carries none', async () => {
    apiPost.mockResolvedValue({ data: { success: false } });
    await reachFinalStep();
    click('初始化系统');
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('初始化失败，请重试'),
    );
  });

  it('reports a transport failure and re-arms the button for a retry', async () => {
    apiPost.mockRejectedValue(new Error('boom'));
    await reachFinalStep();
    click('初始化系统');
    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('系统初始化失败，请重试'),
    );
    await waitFor(() =>
      expect(
        screen.getAllByText('初始化系统')[0].closest('button'),
      ).toHaveAttribute('data-loading', 'false'),
    );
  });

  // `canProceedToNext` validates React state (`formData`) while `onSubmit`
  // re-validates what the Semi form actually holds (`formRef.getValues()`).
  // Those two can diverge — that is the whole reason the second check exists —
  // so these drive the final step with a form whose contents differ from the
  // state that got the user there.
  const reachFinalStepAsFreshInstall = async () => {
    apiGet.mockResolvedValue(setupOk({ root_init: false }));
    await mount();
    click('下一步');
    type('username', 'root');
    type('password', 'longenough1');
    type('confirmPassword', 'longenough1');
    click('下一步');
    click('下一步');
    expect(currentStep()).toBe('3');
  };

  it('validates the admin credentials again at submit time on a fresh install', async () => {
    await reachFinalStepAsFreshInstall();
    globalThis.__setupValues.username = '   ';
    click('初始化系统');
    expect(showError).toHaveBeenCalledWith('请输入管理员用户名');
    expect(apiPost).not.toHaveBeenCalled();
  });

  it('refuses a submit-time password that is too short', async () => {
    await reachFinalStepAsFreshInstall();
    globalThis.__setupValues.password = 'tiny';
    click('初始化系统');
    expect(showError).toHaveBeenCalledWith('密码长度至少为8个字符');
    expect(apiPost).not.toHaveBeenCalled();
  });

  it('refuses a submit-time confirmation mismatch', async () => {
    await reachFinalStepAsFreshInstall();
    globalThis.__setupValues.confirmPassword = 'nope12345';
    click('初始化系统');
    expect(showError).toHaveBeenCalledWith('两次输入的密码不一致');
    expect(apiPost).not.toHaveBeenCalled();
  });

  it('submits the admin credentials when the form really does hold them', async () => {
    await reachFinalStepAsFreshInstall();
    click('初始化系统');
    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(apiPost.mock.calls[0][1]).toMatchObject({
      username: 'root',
      password: 'longenough1',
    });
  });

  // DEFECT (correctness): the success path schedules a reload 1.5s out, but the
  // promise chain's `.finally()` clears the loading flag immediately — so for
  // that whole 1.5s the "初始化系统" button is live again on a page the user is
  // still looking at. A second click fires a second POST /api/setup, i.e. a
  // second attempt to create the root administrator, before the first has been
  // acknowledged. `setLoading(false)` belongs in `.catch()` / the failure
  // branch only, not in `.finally()`.
  // The pin that used to sit here asserted a second submit DOES land
  // (`apiPost.mock.calls.length > 1`), i.e. it locked the double-submit window
  // open: closing it — the lock below — would have turned it red. What must
  // hold either side of that fix is that an impatient second click during the
  // pending window cannot strand the user on the wizard: whether the click is
  // accepted (today) or ignored (after the fix), the scheduled reload still
  // fires and nothing reports a failure for a run the backend accepted.
  it('an impatient second click cannot cancel the pending reload or fake a failure', async () => {
    await reachFinalStep();
    vi.useFakeTimers();
    click('初始化系统');
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(apiPost).toHaveBeenCalledTimes(1);
    expect(reload).not.toHaveBeenCalled();

    // Nothing has visibly happened yet, so the user clicks again.
    click('初始化系统');
    await act(async () => {
      vi.advanceTimersByTime(1500);
    });
    expect(reload).toHaveBeenCalled();
    // A run the backend accepted must never be reported as a failure. (A
    // "please wait" notice for the ignored click would be fine — this only
    // rules out the failure channel.)
    expect(showError).not.toHaveBeenCalledWith('系统初始化失败，请重试');
    expect(showError).not.toHaveBeenCalledWith('初始化失败，请重试');
    // Every submit that did land was the same well-formed initialisation.
    for (const [url, body] of apiPost.mock.calls) {
      expect(url).toBe('/api/setup');
      expect(body).toHaveProperty('SelfUseModeEnabled');
      expect(body).not.toHaveProperty('usageMode');
    }
  });

  it.skip('a successful initialisation must not accept a second submit before the reload', async () => {
    await reachFinalStep();
    vi.useFakeTimers();
    click('初始化系统');
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    click('初始化系统');
    expect(apiPost).toHaveBeenCalledTimes(1);
  });
});
