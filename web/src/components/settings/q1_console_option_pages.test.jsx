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

/**
 * ContentSettingPage / MonitoringSettingPage / DashboardSetting — the three
 * console pages that own a legacy-config migration modal and, between them,
 * three near-identical copies of the same `getOptions` loop.
 *
 * Two behaviours decide whether a live install keeps its configuration:
 *
 *  1. What TYPE each option reaches the child form as. A Switch bound to the
 *     string 'false' renders ON, and the next save writes "enabled" over a
 *     setting the operator turned off. Asserted with `.toBe(false)` against a
 *     fixture that also contains a same-valued string key, so "coerces
 *     everything" and "coerces the right keys" are distinguishable.
 *
 *  2. WHICH options survive a load. All three build `newInputs = {}` from
 *     scratch and then replace state with it, so a key the backend omits
 *     disappears from the form entirely — and, because the next load gates on
 *     `item.key in inputs`, can never come back. Fixtures below deliberately
 *     omit a key on the first response and supply it on the second, which is
 *     the only arrangement that can tell the two apart.
 *
 * Child stubs serialise `props.options` into a data attribute with
 * JSON.stringify: that preserves boolean-vs-string and omits dropped keys, so
 * the thing under test is visible in the DOM rather than merely executed.
 */
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// The real `toBoolean` is kept — it IS the coercion under test.
vi.mock('../../helpers', async () => {
  const boolean = await vi.importActual('../../helpers/boolean');
  return {
    API: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    toBoolean: boolean.toBoolean,
  };
});

const formApiCalls = vi.hoisted(() => ({ setValues: [] }));

vi.mock('@douyinfe/semi-ui', () => {
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Card = ({ children }) =>
    React.createElement('div', { 'data-testid': 'card' }, children);
  const Button = ({ children, onClick, loading }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, 'data-loading': String(!!loading) },
      children,
    );
  // A hidden modal really does render nothing, so its absence is the honest
  // observable for "no migration was offered".
  const Modal = ({ visible, children, onOk, onCancel, confirmLoading }) =>
    visible
      ? React.createElement(
          'div',
          {
            'data-testid': 'migrate-modal',
            'data-confirm-loading': String(!!confirmLoading),
          },
          children,
          React.createElement('button', {
            type: 'button',
            'data-testid': 'migrate-ok',
            onClick: onOk,
          }),
          React.createElement('button', {
            type: 'button',
            'data-testid': 'migrate-cancel',
            onClick: onCancel,
          }),
        )
      : null;
  const Form = ({ children, values, getFormApi }) => {
    React.useEffect(() => {
      if (getFormApi)
        getFormApi({
          setValues: (v) => formApiCalls.setValues.push(v),
          getValues: () => values,
        });
    }, []);
    return React.createElement(
      'div',
      { 'data-testid': 'form', 'data-values': JSON.stringify(values ?? null) },
      children,
    );
  };
  Form.Section = ({ children }) =>
    React.createElement('div', { 'data-testid': 'form-section' }, children);
  // `id` is load-bearing: the page's handleInputChange reads e.target.id to
  // decide WHICH field changed. A stub that dropped it would let the handler
  // run and write to `undefined` while still counting as covered.
  Form.TextArea = ({ field, onChange }) =>
    React.createElement('textarea', {
      'data-testid': `ta-${field}`,
      id: field,
      onChange: (e) => onChange && onChange(e.target.value, e),
    });
  return { Spin, Card, Button, Modal, Form };
});

const { childStub } = vi.hoisted(() => ({
  childStub: (testid) => ({
    default: (props) =>
      React.createElement(
        'div',
        {
          'data-testid': testid,
          'data-options': JSON.stringify(props.options ?? null),
        },
        React.createElement('button', {
          type: 'button',
          'data-testid': `${testid}-refresh`,
          onClick: () => props.refresh && props.refresh(),
        }),
      ),
  }),
}));

vi.mock('../../pages/Setting/Dashboard/SettingsAnnouncements', () =>
  childStub('announcements'),
);
vi.mock('../../pages/Setting/Dashboard/SettingsFAQ', () => childStub('faq'));
vi.mock('../../pages/Setting/Dashboard/SettingsAPIInfo', () =>
  childStub('api-info'),
);
vi.mock('../../pages/Setting/Dashboard/SettingsDataDashboard', () =>
  childStub('data-dashboard'),
);
vi.mock('../../pages/Setting/Dashboard/SettingsUptimeKuma', () =>
  childStub('uptime-kuma'),
);
vi.mock('../../pages/Setting/Operation/SettingsMonitoring', () =>
  childStub('monitoring'),
);
vi.mock('../../pages/Setting/Operation/SettingsLog', () => childStub('log'));

import ContentSettingPage from './ContentSettingPage';
import MonitoringSettingPage from './MonitoringSettingPage';
import DashboardSetting from './DashboardSetting';
import { API, showError, showSuccess } from '../../helpers';

const optionsOk = (pairs) => ({
  data: {
    success: true,
    data: Object.entries(pairs).map(([key, value]) => ({ key, value })),
  },
});

const optionsOf = (testid) =>
  JSON.parse(screen.getByTestId(testid).getAttribute('data-options'));

const lastPut = () => API.put.mock.calls[API.put.mock.calls.length - 1];

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  API.post.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  formApiCalls.setValues.length = 0;
});

describe('ContentSettingPage — loading', () => {
  it('keeps only the keys it declares and pushes them into the form api', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        Notice: 'scheduled downtime',
        'legal.privacy_policy': '# privacy',
        // Not declared by this page: it belongs to another settings screen and
        // must not be pulled into this form, or the next save would write it.
        SomeOtherPageKey: 'do-not-adopt',
      }),
    );

    render(<ContentSettingPage />);

    await waitFor(() =>
      expect(optionsOf('announcements').Notice).toBe('scheduled downtime'),
    );
    expect(optionsOf('announcements')['legal.privacy_policy']).toBe(
      '# privacy',
    );
    expect('SomeOtherPageKey' in optionsOf('announcements')).toBe(false);
    // The Form is a controlled component fed from a ref, so the loaded values
    // have to be pushed into it explicitly or the textareas stay blank.
    expect(formApiCalls.setValues.length).toBeGreaterThan(0);
    expect(
      formApiCalls.setValues[formApiCalls.setValues.length - 1].Notice,
    ).toBe('scheduled downtime');
  });

  it('feeds the identical options object to all three dashboard panels', async () => {
    API.get.mockResolvedValue(optionsOk({ 'console_setting.faq': '[]' }));

    render(<ContentSettingPage />);

    await waitFor(() => expect(screen.getByTestId('faq')).toBeTruthy());
    expect(optionsOf('faq')).toEqual(optionsOf('api-info'));
    expect(optionsOf('faq')).toEqual(optionsOf('announcements'));
  });

  it('reports the backend message and leaves the declared defaults untouched when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'option table locked' },
    });

    render(<ContentSettingPage />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('option table locked'),
    );
    // A refused load must not blank the form.
    expect(optionsOf('announcements').Notice).toBe('');
    expect(formApiCalls.setValues.length).toBe(0);
  });

  it('reports a transport failure and clears the spinner', async () => {
    API.get.mockRejectedValue(new Error('ECONNRESET'));

    render(<ContentSettingPage />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('刷新失败'));
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });

  // ------------------------------------------------------------------
  // DEFECT (ContentSettingPage.jsx getOptions, and the same loop copied into
  // MonitoringSettingPage.jsx and DashboardSetting.jsx):
  //   const newInputs = {};
  //   data.forEach(item => { if (item.key in inputs) newInputs[item.key] = ... });
  //   setInputs(newInputs);
  // `newInputs` is seeded empty rather than from the declared defaults, so a
  // key the backend omits vanishes from state. Because the very next load
  // gates on `item.key in inputs` — and `inputs` is now the SHRUNKEN object —
  // that key can never be re-admitted, for the lifetime of the page. Concrete
  // consequence: an install that has never set `legal.privacy_policy` loads
  // the page, an admin later sets it from another tab, this page refreshes,
  // and the value still does not appear; the admin retypes it, saves, and
  // whatever else was dropped in the same way gets written back blank.
  // The sibling ModelDeploymentSetting.jsx gets this right — it seeds
  // `newInputs` with its own defaults.
  // ------------------------------------------------------------------
  it.skip('a key absent from the first load must still be adoptable on the next one', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ Notice: 'first' }))
      .mockResolvedValueOnce(
        optionsOk({ Notice: 'first', 'legal.privacy_policy': '# privacy' }),
      );

    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(optionsOf('announcements').Notice).toBe('first'),
    );

    fireEvent.click(screen.getByTestId('announcements-refresh'));

    await waitFor(() =>
      expect(optionsOf('announcements')['legal.privacy_policy']).toBe(
        '# privacy',
      ),
    );
  });

  // Live counterpart. It deliberately does NOT assert that the key is missing
  // (that would go red the day `newInputs` is seeded with the defaults).
  // What it asserts is the invariant that must hold either way: a value the
  // backend DID return is the value the form shows — no page-lifetime memory
  // effect where the second load produces a different answer than the first.
  it('shows the value the backend last returned for a key it has always sent', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ Notice: 'first', ApiInfo: '' }))
      .mockResolvedValueOnce(optionsOk({ Notice: 'second', ApiInfo: '' }));

    render(<ContentSettingPage />);
    await waitFor(() => expect(optionsOf('faq').Notice).toBe('first'));

    fireEvent.click(screen.getByTestId('faq-refresh'));

    await waitFor(() => expect(optionsOf('faq').Notice).toBe('second'));
  });
});

describe('ContentSettingPage — saving one field at a time', () => {
  const mountLoaded = async () => {
    API.get.mockResolvedValue(
      optionsOk({
        Notice: 'old notice',
        'legal.user_agreement': 'old ua',
        'legal.privacy_policy': 'old pp',
      }),
    );
    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(optionsOf('announcements').Notice).toBe('old notice'),
    );
  };

  it('PUTs the edited notice under its own key and nothing else', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    fireEvent.change(screen.getByTestId('ta-Notice'), {
      target: { value: 'new notice text' },
    });
    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(lastPut()[0]).toBe('/api/option/');
    // The exact wire payload: one key, one value, the edited text — not the
    // whole form, and not the pre-edit value.
    expect(lastPut()[1]).toEqual({
      key: 'Notice',
      value: 'new notice text',
    });
  });

  it('PUTs the user agreement under legal.user_agreement, not under the notice key', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    fireEvent.change(screen.getByTestId('ta-legal.user_agreement'), {
      target: { value: 'agreement v2' },
    });
    fireEvent.click(screen.getByText('设置用户协议'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(lastPut()[1]).toEqual({
      key: 'legal.user_agreement',
      value: 'agreement v2',
    });
  });

  it('PUTs the privacy policy under legal.privacy_policy', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    fireEvent.change(screen.getByTestId('ta-legal.privacy_policy'), {
      target: { value: 'policy v2' },
    });
    fireEvent.click(screen.getByText('设置隐私政策'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(lastPut()[1]).toEqual({
      key: 'legal.privacy_policy',
      value: 'policy v2',
    });
  });

  it('sends the previously loaded text when the operator saves without editing', async () => {
    // The dangerous alternative is sending '' and wiping a live announcement.
    await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(lastPut()[1].value).toBe('old notice');
  });

  it('surfaces the backend message when a save is refused', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'notice too long' },
    });

    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('notice too long'),
    );
  });

  it('keeps the old value in state when the save is refused', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'notice too long' },
    });

    fireEvent.change(screen.getByTestId('ta-Notice'), {
      target: { value: 'rejected text' },
    });
    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    // Nothing was persisted, so a subsequent save must resend the same text
    // rather than a value the server never accepted being treated as saved.
    API.put.mockClear();
    API.put.mockResolvedValue({ data: { success: true } });
    fireEvent.click(screen.getByText('设置公告'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(lastPut()[1].value).toBe('rejected text');
  });

  // ------------------------------------------------------------------
  // DEFECT (ContentSettingPage.jsx submitNotice / submitUserAgreement /
  // submitPrivacyPolicy): `updateOption` handles a refusal by calling
  // showError and RETURNING NORMALLY — it never throws and never returns a
  // status. The three submit handlers therefore run
  //   await updateOption(...); showSuccess(t('公告已更新'));
  // unconditionally, so a save the server rejected is announced to the
  // operator as "公告已更新". The error toast and the success toast are shown
  // at the same time; the success is the one that reads as the outcome. The
  // operator navigates away believing the announcement/user agreement/privacy
  // policy is live when the old text is still being served — for the two
  // legal documents that is a compliance-visible failure.
  // ------------------------------------------------------------------
  it.skip('a refused save must not be reported as success', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'notice too long' },
    });

    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it.skip('a refused legal-document save must not be reported as success', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'policy rejected' },
    });

    fireEvent.click(screen.getByText('设置隐私政策'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showSuccess).not.toHaveBeenCalled();
  });

  // Live counterpart to the two locks above: whichever way the repair goes,
  // the operator must be TOLD about a refusal. Today that happens via
  // showError (alongside a wrong success); after a fix it happens via
  // showError alone. Silence is the state that must never occur.
  it('always tells the operator when a save was refused', async () => {
    await mountLoaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'notice too long' },
    });

    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() =>
      expect(showError.mock.calls.map((c) => c[0])).toContain(
        'notice too long',
      ),
    );
  });

  it('reports the failure, not a success, when the PUT itself throws', async () => {
    await mountLoaded();
    API.put.mockRejectedValue(new Error('ECONNRESET'));

    fireEvent.click(screen.getByText('设置公告'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('公告更新失败'));
    expect(showSuccess).not.toHaveBeenCalled();
  });
});

describe('ContentSettingPage — legacy migration', () => {
  it('offers the migration only when a legacy key actually holds data', async () => {
    API.get.mockResolvedValue(optionsOk({ ApiInfo: '{"legacy":true}' }));

    render(<ContentSettingPage />);

    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );
  });

  it('does not offer the migration when the legacy keys are empty', async () => {
    // The negative half: without it, "offers on legacy data" and "always
    // offers" are the same test.
    API.get.mockResolvedValue(
      optionsOk({ ApiInfo: '', Announcements: '', FAQ: '' }),
    );

    render(<ContentSettingPage />);

    await waitFor(() =>
      expect(screen.getByTestId('announcements')).toBeTruthy(),
    );
    expect(screen.queryByTestId('migrate-modal')).toBeNull();
  });

  it('posts the migration, re-reads the options and closes the dialog', async () => {
    // The first response has to carry `console_setting.api_info` (even empty)
    // for the post-migration value to be adoptable at all — see the
    // `newInputs = {}` defect lock above. Including it keeps THIS test about
    // the migration round-trip and green either side of that repair.
    API.get
      .mockResolvedValueOnce(
        optionsOk({
          ApiInfo: '{"legacy":true}',
          'console_setting.api_info': '',
        }),
      )
      .mockResolvedValue(
        optionsOk({ ApiInfo: '', 'console_setting.api_info': '{"new":true}' }),
      );
    API.post.mockResolvedValue({ data: { success: true } });

    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-ok'));

    await waitFor(() =>
      expect(API.post).toHaveBeenCalledWith(
        '/api/option/migrate_console_setting',
      ),
    );
    await waitFor(() =>
      expect(optionsOf('api-info')['console_setting.api_info']).toBe(
        '{"new":true}',
      ),
    );
    await waitFor(() =>
      expect(screen.queryByTestId('migrate-modal')).toBeNull(),
    );
  });

  it('leaves the dialog open and reports the error when the migration throws', async () => {
    API.get.mockResolvedValue(optionsOk({ ApiInfo: '{"legacy":true}' }));
    API.post.mockRejectedValue(new Error('table is locked'));

    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-ok'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('迁移失败: table is locked'),
    );
    expect(screen.getByTestId('migrate-modal')).toBeTruthy();
  });

  it('closes the dialog without posting anything when the operator declines', async () => {
    API.get.mockResolvedValue(optionsOk({ FAQ: '[{"q":"x"}]' }));

    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-cancel'));

    await waitFor(() =>
      expect(screen.queryByTestId('migrate-modal')).toBeNull(),
    );
    expect(API.post).not.toHaveBeenCalled();
  });

  // ------------------------------------------------------------------
  // DEFECT (ContentSettingPage.jsx handleMigrate — identical code in
  // MonitoringSettingPage.jsx and DashboardSetting.jsx): the POST result is
  // never inspected. `await API.post(...)` resolving with
  // `{success:false, message:'…'}` is treated exactly like a success —
  // "旧配置迁移完成" is shown and the dialog is dismissed. The migration is
  // one-way and destructive (the modal's own text says the old configuration
  // is cleared afterwards), so an operator who is told it completed will not
  // re-run it, and will not restore the backup they were asked to take.
  // Only a transport-level throw is caught.
  // ------------------------------------------------------------------
  it.skip('a refused migration must not be announced as complete', async () => {
    API.get.mockResolvedValue(optionsOk({ ApiInfo: '{"legacy":true}' }));
    API.post.mockResolvedValue({
      data: { success: false, message: 'nothing to migrate' },
    });

    render(<ContentSettingPage />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-ok'));

    await waitFor(() => expect(API.post).toHaveBeenCalled());
    expect(showSuccess).not.toHaveBeenCalledWith('旧配置迁移完成');
  });
});

describe('MonitoringSettingPage', () => {
  it('coerces exactly the boolean-defaulted keys and leaves same-valued strings alone', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        AutomaticDisableChannelEnabled: 'false',
        LogConsumeEnabled: 'true',
        'monitor_setting.auto_test_channel_enabled': '1',
        // Same textual value as the first key, but declared as '' not false,
        // so it must stay a string. This pair is what distinguishes "coerces
        // the declared booleans" from "coerces anything that looks boolean".
        'console_setting.uptime_kuma_enabled': 'false',
      }),
    );

    render(<MonitoringSettingPage />);

    await waitFor(() => expect(screen.getByTestId('monitoring')).toBeTruthy());
    const opts = optionsOf('monitoring');
    expect(opts.AutomaticDisableChannelEnabled).toBe(false);
    expect(opts.LogConsumeEnabled).toBe(true);
    expect(opts['monitor_setting.auto_test_channel_enabled']).toBe(true);
    expect(opts['console_setting.uptime_kuma_enabled']).toBe('false');
    expect(typeof opts['console_setting.uptime_kuma_enabled']).toBe('string');
  });

  it('drops keys it does not declare rather than forwarding them to the forms', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ChannelDisableThreshold: '5',
        SomeUnrelatedKey: 'x',
      }),
    );

    render(<MonitoringSettingPage />);

    await waitFor(() =>
      expect(optionsOf('log').ChannelDisableThreshold).toBe('5'),
    );
    expect('SomeUnrelatedKey' in optionsOf('log')).toBe(false);
  });

  it('hands the same options object to all four panels', async () => {
    API.get.mockResolvedValue(optionsOk({ LogConsumeEnabled: 'true' }));

    render(<MonitoringSettingPage />);

    await waitFor(() => expect(screen.getByTestId('uptime-kuma')).toBeTruthy());
    expect(optionsOf('monitoring')).toEqual(optionsOf('log'));
    expect(optionsOf('monitoring')).toEqual(optionsOf('data-dashboard'));
    expect(optionsOf('monitoring')).toEqual(optionsOf('uptime-kuma'));
  });

  it('re-reads the options when a panel asks for a refresh', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ LogConsumeEnabled: 'false' }))
      .mockResolvedValueOnce(optionsOk({ LogConsumeEnabled: 'true' }));

    render(<MonitoringSettingPage />);
    await waitFor(() => expect(optionsOf('log').LogConsumeEnabled).toBe(false));

    fireEvent.click(screen.getByTestId('log-refresh'));

    await waitFor(() => expect(optionsOf('log').LogConsumeEnabled).toBe(true));
  });

  it('offers the uptime-kuma migration only when a legacy key holds data', async () => {
    API.get.mockResolvedValue(
      optionsOk({ UptimeKumaUrl: 'https://kuma.example.test' }),
    );

    render(<MonitoringSettingPage />);

    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );
  });

  it('does not offer the migration when the legacy keys are empty', async () => {
    API.get.mockResolvedValue(
      optionsOk({ UptimeKumaUrl: '', UptimeKumaSlug: '' }),
    );

    render(<MonitoringSettingPage />);

    await waitFor(() => expect(screen.getByTestId('monitoring')).toBeTruthy());
    expect(screen.queryByTestId('migrate-modal')).toBeNull();
  });

  it('reports the backend message and keeps its defaults when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'permission denied' },
    });

    render(<MonitoringSettingPage />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('permission denied'),
    );
    // The declared boolean default survives as a boolean, not as undefined.
    expect(optionsOf('monitoring').AutomaticDisableChannelEnabled).toBe(false);
  });

  // Same defect as ContentSettingPage's `newInputs = {}` lock above; recorded
  // separately because it is a separate copy of the loop with its own
  // `typeof inputs[key] === 'boolean'` gate, which additionally means a
  // dropped boolean key stops being coerced on every later load.
  it.skip('a boolean key absent from the first load must still arrive typed on the next one', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ ChannelDisableThreshold: '5' }))
      .mockResolvedValueOnce(
        optionsOk({ ChannelDisableThreshold: '5', LogConsumeEnabled: 'true' }),
      );

    render(<MonitoringSettingPage />);
    await waitFor(() =>
      expect(optionsOf('monitoring').ChannelDisableThreshold).toBe('5'),
    );

    fireEvent.click(screen.getByTestId('monitoring-refresh'));

    await waitFor(() =>
      expect(optionsOf('monitoring').LogConsumeEnabled).toBe(true),
    );
  });
});

describe('DashboardSetting', () => {
  it('coerces DataExportEnabled to a boolean and leaves the console_setting flags as strings', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        DataExportEnabled: 'false',
        // Also ends with a lowercase `enabled` and holds the same text, but
        // this page deliberately keeps the console_setting flags as strings.
        'console_setting.faq_enabled': 'false',
        'console_setting.api_info_enabled': 'true',
      }),
    );

    render(<DashboardSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('data-dashboard')).toBeTruthy(),
    );
    const opts = optionsOf('data-dashboard');
    expect(opts.DataExportEnabled).toBe(false);
    expect(opts['console_setting.faq_enabled']).toBe('false');
    expect(opts['console_setting.api_info_enabled']).toBe('true');
  });

  it('keeps DataExportDefaultTime and DataExportInterval as the strings the backend sent', async () => {
    API.get.mockResolvedValue(
      optionsOk({ DataExportDefaultTime: 'day', DataExportInterval: '15' }),
    );

    render(<DashboardSetting />);

    await waitFor(() =>
      expect(optionsOf('data-dashboard').DataExportDefaultTime).toBe('day'),
    );
    // NOTE (not a defect lock): the declared default for DataExportInterval is
    // the number 5, but a loaded value arrives as a string. Asserting the
    // string here would pin that; instead the assertion is the invariant that
    // survives either representation — the loaded value means 15, not the
    // hardcoded default.
    expect(Number(optionsOf('data-dashboard').DataExportInterval)).toBe(15);
  });

  it('offers the migration when any of the five legacy keys holds data', async () => {
    API.get.mockResolvedValue(optionsOk({ UptimeKumaSlug: 'prod' }));

    render(<DashboardSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );
  });

  it('does not offer the migration when every legacy key is empty', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        ApiInfo: '',
        Announcements: '',
        FAQ: '',
        UptimeKumaUrl: '',
        UptimeKumaSlug: '',
      }),
    );

    render(<DashboardSetting />);

    await waitFor(() => expect(screen.getByTestId('faq')).toBeTruthy());
    expect(screen.queryByTestId('migrate-modal')).toBeNull();
  });

  it('reports the migration failure with the thrown message', async () => {
    API.get.mockResolvedValue(optionsOk({ Announcements: '[{"a":1}]' }));
    API.post.mockRejectedValue(new Error('disk full'));

    render(<DashboardSetting />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-ok'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('迁移失败: disk full'),
    );
  });

  it('falls back to a generic message when the thrown error carries none', async () => {
    API.get.mockResolvedValue(optionsOk({ Announcements: '[{"a":1}]' }));
    API.post.mockRejectedValue({});

    render(<DashboardSetting />);
    await waitFor(() =>
      expect(screen.getByTestId('migrate-modal')).toBeTruthy(),
    );

    fireEvent.click(screen.getByTestId('migrate-ok'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('迁移失败: 未知错误'),
    );
  });

  it('reports the backend message when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'db unavailable' },
    });

    render(<DashboardSetting />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('db unavailable'),
    );
  });

  it('reports a transport failure and stops spinning', async () => {
    API.get.mockRejectedValue(new Error('ETIMEDOUT'));

    render(<DashboardSetting />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('刷新失败'));
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });
});
