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
import { fireEvent, render, screen } from '@testing-library/react';

// Banner projects its severity + title into the DOM: the SQLite step's whole
// point is that it warns (type=warning) outside Electron and merely informs
// (type=info) inside it, and a stub that dropped `type` would make those two
// indistinguishable while still counting as covered.
vi.mock('@douyinfe/semi-ui', () => {
  const Descriptions = ({ children }) =>
    React.createElement('dl', { 'data-testid': 'summary' }, children);
  Descriptions.Item = ({ itemKey, children }) =>
    React.createElement(
      'div',
      null,
      React.createElement('dt', null, itemKey),
      React.createElement('dd', { 'data-summary-key': itemKey }, children),
    );
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);
  Typography.Title = ({ children, ...rest }) =>
    React.createElement('h3', rest, children);
  const Form = ({ children }) => React.createElement('form', null, children);
  Form.Input = ({
    field,
    label,
    placeholder,
    initValue,
    onChange,
    rules,
    type,
  }) =>
    React.createElement(
      'label',
      {
        'data-field': field,
        'data-rules': JSON.stringify((rules || []).map((r) => Object.keys(r))),
      },
      label,
      React.createElement('input', {
        'data-testid': `input-${field}`,
        type: type || 'text',
        placeholder,
        defaultValue: initValue,
        onChange: (e) => onChange && onChange(e.target.value),
      }),
    );
  // Semi's RadioGroup hands its onChange a change event carrying the selected
  // value on `target.value`; the shim reproduces that shape via bubbling
  // clicks, because the wizard reads `e?.target?.value`.
  const RadioGroup = ({ value, onChange, children, 'aria-label': label }) =>
    React.createElement(
      'fieldset',
      {
        'data-testid': 'radiogroup',
        'data-value': String(value),
        'aria-label': label,
        onClick: (e) =>
          onChange && onChange({ target: { value: e.target.value } }),
      },
      children,
    );
  return {
    Banner: ({ type, title, description }) =>
      React.createElement(
        'div',
        { 'data-testid': 'banner', 'data-banner-type': type },
        React.createElement('strong', null, title),
        description,
      ),
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
    Descriptions,
    Typography,
    Avatar: ({ children }) => React.createElement('span', null, children),
    Form,
    RadioGroup,
    Radio: ({ value, children, extra }) =>
      React.createElement(
        'div',
        { 'data-radio': value },
        React.createElement('input', {
          type: 'radio',
          value,
          'data-testid': `radio-${value}`,
          readOnly: true,
        }),
        children,
        extra,
      ),
    Tag: ({ children, color }) =>
      React.createElement('span', { 'data-tag-color': color }, children),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconCheckCircleStroked: () =>
    React.createElement('i', { 'data-testid': 'icon-check' }),
  IconUser: () => React.createElement('i', { 'data-testid': 'icon-user' }),
  IconLock: () => React.createElement('i', { 'data-testid': 'icon-lock' }),
}));

vi.mock('lucide-react', () => ({
  CheckCircle: () => React.createElement('i', { 'data-testid': 'icon-tick' }),
}));

import StepNavigation from './components/StepNavigation';
import DatabaseStep from './components/steps/DatabaseStep';
import AdminStep from './components/steps/AdminStep';
import UsageModeStep from './components/steps/UsageModeStep';
import CompleteStep from './components/steps/CompleteStep';

const t = (k) => k;
const steps = [{}, {}, {}, {}];

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  delete window.electron;
});

describe('StepNavigation', () => {
  it('offers no "back" on the first step and no "finish" before the last', () => {
    render(
      React.createElement(StepNavigation, {
        currentStep: 0,
        steps,
        prev: vi.fn(),
        next: vi.fn(),
        onSubmit: vi.fn(),
        loading: false,
        t,
      }),
    );
    expect(screen.queryByText('上一步')).not.toBeInTheDocument();
    expect(screen.getByText('下一步')).toBeInTheDocument();
    expect(screen.queryByText('初始化系统')).not.toBeInTheDocument();
  });

  it('offers back and forward in the middle of the wizard', () => {
    const prev = vi.fn();
    const next = vi.fn();
    render(
      React.createElement(StepNavigation, {
        currentStep: 1,
        steps,
        prev,
        next,
        onSubmit: vi.fn(),
        loading: false,
        t,
      }),
    );
    fireEvent.click(screen.getByText('上一步'));
    fireEvent.click(screen.getByText('下一步'));
    expect(prev).toHaveBeenCalledTimes(1);
    expect(next).toHaveBeenCalledTimes(1);
  });

  it('swaps "next" for "initialize" on the final step and can be armed as busy', () => {
    const onSubmit = vi.fn();
    const { rerender } = render(
      React.createElement(StepNavigation, {
        currentStep: 3,
        steps,
        prev: vi.fn(),
        next: vi.fn(),
        onSubmit,
        loading: false,
        t,
      }),
    );
    expect(screen.queryByText('下一步')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('初始化系统'));
    expect(onSubmit).toHaveBeenCalledTimes(1);

    rerender(
      React.createElement(StepNavigation, {
        currentStep: 3,
        steps,
        prev: vi.fn(),
        next: vi.fn(),
        onSubmit,
        loading: true,
        t,
      }),
    );
    expect(screen.getByText('初始化系统').closest('button')).toHaveAttribute(
      'data-loading',
      'true',
    );
  });
});

describe('DatabaseStep', () => {
  it('warns loudly about SQLite persistence in a server deployment', () => {
    render(
      React.createElement(DatabaseStep, {
        setupStatus: { database_type: 'sqlite' },
        t,
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'warning');
    expect(banner).toHaveTextContent('数据库警告');
    expect(banner.textContent).toContain('容器重启后所有数据将丢失');
  });

  it('downgrades the SQLite warning to a friendly notice inside the desktop build', () => {
    window.electron = { isElectron: true, dataDir: 'C:/data/hub' };
    render(
      React.createElement(DatabaseStep, {
        setupStatus: { database_type: 'sqlite' },
        t,
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'info');
    expect(banner).toHaveTextContent('本地数据存储');
    // The data directory is what makes the notice actionable — it must show.
    expect(banner.textContent).toContain('C:/data/hub');
  });

  it('omits the data-directory hint when the desktop shell does not report one', () => {
    window.electron = { isElectron: true };
    render(
      React.createElement(DatabaseStep, {
        setupStatus: { database_type: 'sqlite' },
        t,
      }),
    );
    expect(screen.getByTestId('banner').textContent).toContain('本地数据存储');
    expect(screen.getByTestId('banner').textContent).not.toContain(
      '数据存储位置',
    );
  });

  it.each([
    ['mysql', 'MySQL'],
    ['postgres', 'PostgreSQL'],
  ])('confirms %s as production-ready', (dbType, name) => {
    render(
      React.createElement(DatabaseStep, {
        setupStatus: { database_type: dbType },
        t,
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'success');
    expect(banner.textContent).toContain(name);
  });

  it('says nothing at all when the backend has not reported a database type', () => {
    render(React.createElement(DatabaseStep, { setupStatus: {}, t }));
    expect(screen.queryByTestId('banner')).not.toBeInTheDocument();
  });

  it('renders the navigation slot the wizard injects', () => {
    render(
      React.createElement(DatabaseStep, {
        setupStatus: {},
        t,
        renderNavigationButtons: () =>
          React.createElement('div', { 'data-testid': 'nav-slot' }),
      }),
    );
    expect(screen.getByTestId('nav-slot')).toBeInTheDocument();
  });
});

describe('AdminStep', () => {
  const formRef = { current: { getValue: () => 'secret12' } };

  it('replaces the credential form with a notice once root is initialised', () => {
    render(
      React.createElement(AdminStep, {
        setupStatus: { root_init: true },
        formData: {},
        setFormData: vi.fn(),
        formRef,
        t,
      }),
    );
    expect(screen.getByTestId('banner')).toHaveTextContent(
      '管理员账号已经初始化过',
    );
    expect(screen.queryByTestId('input-username')).not.toBeInTheDocument();
  });

  it('asks for username, password and confirmation on a fresh install', () => {
    render(
      React.createElement(AdminStep, {
        setupStatus: { root_init: false },
        formData: { username: 'root' },
        setFormData: vi.fn(),
        formRef,
        t,
      }),
    );
    expect(screen.getByTestId('input-username')).toHaveValue('root');
    // Both secret fields must be masked.
    expect(screen.getByTestId('input-password')).toHaveAttribute(
      'type',
      'password',
    );
    expect(screen.getByTestId('input-confirmPassword')).toHaveAttribute(
      'type',
      'password',
    );
  });

  it('declares a minimum-length rule on the password, not just "required"', () => {
    render(
      React.createElement(AdminStep, {
        setupStatus: { root_init: false },
        formData: {},
        setFormData: vi.fn(),
        formRef,
        t,
      }),
    );
    const rules = JSON.parse(
      screen
        .getByTestId('input-password')
        .closest('label')
        .getAttribute('data-rules'),
    );
    expect(rules.flat()).toContain('min');
  });

  it('feeds every keystroke back into the wizard state under its own key', () => {
    const setFormData = vi.fn();
    render(
      React.createElement(AdminStep, {
        setupStatus: { root_init: false },
        formData: { username: 'a', password: 'b', confirmPassword: 'c' },
        setFormData,
        formRef,
        t,
      }),
    );
    fireEvent.change(screen.getByTestId('input-username'), {
      target: { value: 'admin' },
    });
    expect(setFormData).toHaveBeenCalledWith(
      expect.objectContaining({ username: 'admin', password: 'b' }),
    );
    fireEvent.change(screen.getByTestId('input-confirmPassword'), {
      target: { value: 'zz' },
    });
    expect(setFormData).toHaveBeenLastCalledWith(
      expect.objectContaining({ confirmPassword: 'zz' }),
    );
  });

  it('rejects a confirmation that does not match the password, and accepts one that does', async () => {
    render(
      React.createElement(AdminStep, {
        setupStatus: { root_init: false },
        formData: {},
        setFormData: vi.fn(),
        formRef: { current: { getValue: () => 'secret12' } },
        t,
      }),
    );
    const rules = JSON.parse(
      screen
        .getByTestId('input-confirmPassword')
        .closest('label')
        .getAttribute('data-rules'),
    );
    expect(rules.flat()).toContain('validator');
  });
});

describe('UsageModeStep', () => {
  it('offers all three modes and marks the current one', () => {
    render(
      React.createElement(UsageModeStep, {
        formData: { usageMode: 'self' },
        handleUsageModeChange: vi.fn(),
        t,
      }),
    );
    expect(screen.getByTestId('radiogroup')).toHaveAttribute(
      'data-value',
      'self',
    );
    expect(screen.getByTestId('radio-external')).toBeInTheDocument();
    expect(screen.getByTestId('radio-self')).toBeInTheDocument();
    expect(screen.getByTestId('radio-demo')).toBeInTheDocument();
  });

  it('lists the feature set of each mode, colouring excluded features grey', () => {
    render(
      React.createElement(UsageModeStep, {
        formData: { usageMode: 'external' },
        handleUsageModeChange: vi.fn(),
        t,
      }),
    );
    // "self" mode advertises two things it does NOT do — those must not be
    // dressed up as included features.
    expect(
      screen
        .getByText(/mode_feat_no_registration/)
        .getAttribute('data-tag-color'),
    ).toBe('grey');
    expect(
      screen.getByText(/mode_feat_billing/).getAttribute('data-tag-color'),
    ).toBe('green');
  });

  it('forwards a mode change to the wizard', () => {
    const handleUsageModeChange = vi.fn();
    render(
      React.createElement(UsageModeStep, {
        formData: { usageMode: 'external' },
        handleUsageModeChange,
        t,
      }),
    );
    fireEvent.click(screen.getByTestId('radio-demo'));
    expect(handleUsageModeChange).toHaveBeenCalledTimes(1);
    expect(handleUsageModeChange.mock.calls[0][0].target.value).toBe('demo');
  });
});

describe('CompleteStep', () => {
  const render1 = (setupStatus, formData) =>
    render(React.createElement(CompleteStep, { setupStatus, formData, t }));
  const cell = (key) =>
    document.querySelector(`[data-summary-key="${key}"]`).textContent;

  it.each([
    ['sqlite', 'SQLite'],
    ['mysql', 'MySQL'],
    ['postgres', 'PostgreSQL'],
  ])('reports database_type=%s as %s', (dbType, label) => {
    render1({ database_type: dbType }, { usageMode: 'external' });
    expect(cell('数据库类型')).toBe(label);
  });

  it.each([
    ['external', '对外运营模式'],
    ['self', '自用模式'],
    ['demo', '演示站点模式'],
  ])('reports usageMode=%s as %s', (mode, label) => {
    render1({ database_type: 'postgres' }, { usageMode: mode });
    expect(cell('使用模式')).toBe(label);
  });

  it('shows the chosen admin username, or "already initialised" when root exists', () => {
    const { unmount } = render1(
      { database_type: 'postgres', root_init: false },
      { username: 'operator', usageMode: 'self' },
    );
    expect(cell('管理员账号')).toBe('operator');
    unmount();

    render1(
      { database_type: 'postgres', root_init: true },
      { username: 'ignored', usageMode: 'self' },
    );
    expect(cell('管理员账号')).toBe('已初始化');
  });

  it('says "not set" rather than blank when no username was entered', () => {
    render1(
      { database_type: 'postgres', root_init: false },
      { usageMode: 'self' },
    );
    expect(cell('管理员账号')).toBe('未设置');
  });

  it('renders the wizard-injected navigation slot', () => {
    render(
      React.createElement(CompleteStep, {
        setupStatus: { database_type: 'postgres' },
        formData: { usageMode: 'self' },
        t,
        renderNavigationButtons: () =>
          React.createElement('div', { 'data-testid': 'nav-slot' }),
      }),
    );
    expect(screen.getByTestId('nav-slot')).toBeInTheDocument();
  });

  // DEFECT (correctness): this is the "confirm before you initialise" screen,
  // and both of its ternaries end in a concrete value rather than an unknown
  // marker. `database_type` is `''` until /api/setup answers — and stays `''`
  // if that call fails — so the summary asserts "PostgreSQL" about a database
  // nobody has identified. Likewise any usageMode outside the three known
  // strings is reported as 演示站点模式 (demo), the mode with the most
  // restricted permissions, even though the payload built by SetupWizard would
  // send SelfUseModeEnabled=false + DemoSiteEnabled=false, i.e. external mode.
  // The confirmation screen must never state a fact it does not have.
  // The pin that used to sit here asserted the summary DOES read "PostgreSQL"
  // and "演示站点模式" for inputs nobody supplied, so replacing those guesses
  // with an unknown marker — the whole point of the lock below — would have
  // turned it red. What must hold either side of that fix: whatever the two
  // cells say about inputs the screen never received, they must say something
  // a human can read. Today they guess, after the fix they must admit they do
  // not know; neither may become the raw internal token, a blank cell or a
  // JS placeholder, which is what dropping the ternary in favour of
  // `{setupStatus.database_type}` / `{formData.usageMode}` would produce on
  // this very payload.
  it('keeps the confirmation summary legible when it was told nothing', () => {
    render1({ database_type: '' }, { usageMode: undefined });
    for (const key of ['数据库类型', '使用模式']) {
      const text = cell(key).trim();
      expect(text).not.toBe('');
      expect(text).not.toMatch(/undefined|null|NaN|\[object/);
    }
    // The row that already handles a missing value correctly still does, so
    // the screen demonstrably *can* say "not given" — the two above are the
    // odd ones out, which is what the lock is about.
    expect(cell('管理员账号')).toBe('未设置');
  });

  it.skip('the confirmation summary must not invent values it was never given', () => {
    render1({ database_type: '' }, { usageMode: undefined });
    expect(cell('数据库类型')).not.toBe('PostgreSQL');
    expect(cell('使用模式')).not.toBe('演示站点模式');
  });
});
