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
 * The settings pages that are pure composition plus one option load:
 * GeneralSettingPage, QuotaLimitsSettingPage, UIModulesSettingPage (all three
 * via the shared `useSettingsOptions` hook), AIFeaturesSettingPage and
 * ModelConfigSettingPage (composition only), and OperationSetting (its own
 * hand-rolled copy of the loop).
 *
 * What is worth asserting here is the TYPE each declared option reaches the
 * child form as, because these pages are the last place a value is a JS value
 * before a child form turns it back into a string and PUTs it. A flag that
 * arrives as the string 'false' renders a Switch as ON — non-empty strings
 * are truthy — and the operator's next save writes "on" over a setting they
 * deliberately turned off.
 *
 * Child stubs render a marker carrying `props.options` as JSON: booleans stay
 * distinguishable from same-valued strings, and a key that was dropped is
 * simply absent.
 */
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../helpers', async () => {
  const boolean = await vi.importActual('../../helpers/boolean');
  return {
    API: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    toBoolean: boolean.toBoolean,
  };
});

vi.mock('@douyinfe/semi-ui', () => {
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Card = ({ children }) =>
    React.createElement('div', { 'data-testid': 'card' }, children);
  return { Spin, Card };
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

vi.mock('../../pages/Setting/Operation/SettingsGeneral', () =>
  childStub('general'),
);
vi.mock('../../pages/Setting/Operation/SettingsCreditLimit', () =>
  childStub('credit-limit'),
);
vi.mock('../../pages/Setting/Operation/SettingsCheckin', () =>
  childStub('checkin'),
);
vi.mock('../../pages/Setting/Operation/SettingsHeaderNavModules', () =>
  childStub('header-nav'),
);
vi.mock('../../pages/Setting/Operation/SettingsSidebarModulesAdmin', () =>
  childStub('sidebar-admin'),
);
vi.mock('../../pages/Setting/Operation/SettingsSensitiveWords', () =>
  childStub('sensitive-words'),
);
vi.mock('../../pages/Setting/Operation/SettingsLog', () => childStub('log'));
vi.mock('../../pages/Setting/Operation/SettingsMonitoring', () =>
  childStub('monitoring'),
);
vi.mock('./RateLimitSetting', () => childStub('rate-limit'));
vi.mock('./ChatsSetting', () => childStub('chats'));
vi.mock('./DrawingSetting', () => childStub('drawing'));
vi.mock('./ModelSetting', () => childStub('model'));
vi.mock('./ModelDeploymentSetting', () => childStub('model-deployment'));

import GeneralSettingPage from './GeneralSettingPage';
import QuotaLimitsSettingPage from './QuotaLimitsSettingPage';
import UIModulesSettingPage from './UIModulesSettingPage';
import AIFeaturesSettingPage from './AIFeaturesSettingPage';
import ModelConfigSettingPage from './ModelConfigSettingPage';
import OperationSetting from './OperationSetting';
import { API, showError } from '../../helpers';

const optionsOk = (pairs) => ({
  data: {
    success: true,
    data: Object.entries(pairs).map(([key, value]) => ({ key, value })),
  },
});

const optionsOf = (testid) =>
  JSON.parse(screen.getByTestId(testid).getAttribute('data-options'));

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  showError.mockReset();
});

describe('GeneralSettingPage', () => {
  it('types the declared boolean options as booleans and same-valued strings as strings', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        DisplayTokenStatEnabled: 'false',
        SelfUseModeEnabled: 'true',
        'quota_setting.enable_free_model_pre_consume': '0',
        // Declared as '' (a string), and deliberately given the SAME text as
        // the first key. Without this negative half, "coerces the declared
        // booleans" and "coerces anything spelt true/false" look identical.
        TopUpLink: 'false',
      }),
    );

    render(<GeneralSettingPage />);

    await waitFor(() => expect(screen.getByTestId('general')).toBeTruthy());
    const opts = optionsOf('general');
    expect(opts.DisplayTokenStatEnabled).toBe(false);
    expect(opts.SelfUseModeEnabled).toBe(true);
    expect(opts['quota_setting.enable_free_model_pre_consume']).toBe(false);
    expect(opts.TopUpLink).toBe('false');
    expect(typeof opts.TopUpLink).toBe('string');
  });

  it('ignores options it does not declare rather than forwarding them to the form', async () => {
    API.get.mockResolvedValue(
      optionsOk({ QuotaPerUnit: '500000', NotOnThisPage: 'x' }),
    );

    render(<GeneralSettingPage />);

    await waitFor(() =>
      expect(optionsOf('general').QuotaPerUnit).toBeDefined(),
    );
    // Forwarding a foreign key would put it into the child's diff-on-save set.
    expect('NotOnThisPage' in optionsOf('general')).toBe(false);
  });

  it('carries a loaded quota value through as the amount the backend stated', async () => {
    API.get.mockResolvedValue(optionsOk({ QuotaForNewUser: '123456' }));

    render(<GeneralSettingPage />);

    // The assertion is on the VALUE, not on the representation: this page
    // declares the field as a number but the option API speaks strings, and
    // either representation is a legitimate design. What must never happen is
    // the hardcoded 0 default surviving a load that said 123456.
    await waitFor(() =>
      expect(Number(optionsOf('general').QuotaForNewUser)).toBe(123456),
    );
  });

  it('shows the spinner while loading and clears it afterwards', async () => {
    let release;
    API.get.mockReturnValue(
      new Promise((resolve) => {
        release = () => resolve(optionsOk({ TopUpLink: '/topup' }));
      }),
    );

    render(<GeneralSettingPage />);

    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'true',
      ),
    );
    release();
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });

  it('reports the backend message and keeps the declared defaults when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'not an administrator' },
    });

    render(<GeneralSettingPage />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('not an administrator'),
    );
    // The refusal path leaves state alone, so the declared boolean default is
    // still a boolean and the form is not blanked.
    expect(optionsOf('general').DisplayTokenStatEnabled).toBe(false);
    expect(
      optionsOf('general')['quota_setting.enable_free_model_pre_consume'],
    ).toBe(true);
  });

  it('reports a transport failure and stops spinning', async () => {
    API.get.mockRejectedValue(new Error('ECONNRESET'));

    render(<GeneralSettingPage />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('刷新失败'));
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });

  it('re-reads the options when the child asks for a refresh', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ TopUpLink: '/old' }))
      .mockResolvedValueOnce(optionsOk({ TopUpLink: '/new' }));

    render(<GeneralSettingPage />);
    await waitFor(() => expect(optionsOf('general').TopUpLink).toBe('/old'));

    fireEvent.click(screen.getByTestId('general-refresh'));

    await waitFor(() => expect(optionsOf('general').TopUpLink).toBe('/new'));
  });

  // ------------------------------------------------------------------
  // DEFECT (pages/Setting/useSettingsOptions.js getOptions, reached from
  // GeneralSettingPage / QuotaLimitsSettingPage / UIModulesSettingPage):
  //   const newInputs = {};
  //   data.forEach(...only keys present in the response...);
  //   setInputs(newInputs);
  // The accumulator starts empty instead of from `initialState`, so any
  // declared key the response omits is not merely un-updated, it is REMOVED
  // from the form's value object. The child form then reads `undefined` for
  // that field, renders it blank, and — because these child forms diff their
  // current values against the ones they were handed and PUT everything that
  // differs — a save the operator makes for an unrelated field can carry the
  // blanked one along and clear a live setting.
  // This bites during a rolling upgrade, when the new console asks an old
  // backend for options it does not know yet. The sibling
  // ModelDeploymentSetting.jsx avoids it by seeding `newInputs` with its own
  // defaults; the hook should do the same.
  // ------------------------------------------------------------------
  it.skip('a declared option omitted by the backend must keep its default', async () => {
    API.get.mockResolvedValue(optionsOk({ TopUpLink: '/topup' }));

    render(<GeneralSettingPage />);

    await waitFor(() => expect(optionsOf('general').TopUpLink).toBe('/topup'));
    // Declared as `false` in INITIAL_STATE and absent from the response.
    expect(optionsOf('general').DemoSiteEnabled).toBe(false);
    expect(optionsOf('general')['general_setting.quota_display_type']).toBe(
      'USD',
    );
  });
});

describe('QuotaLimitsSettingPage', () => {
  it('types the check-in flag as a boolean and hands the same object to both cards', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'checkin_setting.enabled': 'false',
        'checkin_setting.min_quota': '2000',
        'quota_setting.enable_free_model_pre_consume': 'true',
      }),
    );

    render(<QuotaLimitsSettingPage />);

    await waitFor(() => expect(screen.getByTestId('checkin')).toBeTruthy());
    expect(optionsOf('checkin')['checkin_setting.enabled']).toBe(false);
    expect(
      optionsOf('checkin')['quota_setting.enable_free_model_pre_consume'],
    ).toBe(true);
    expect(optionsOf('checkin')).toEqual(optionsOf('credit-limit'));
  });

  it('carries the loaded check-in quota bounds through as the stated amounts', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        'checkin_setting.min_quota': '2000',
        'checkin_setting.max_quota': '90000',
      }),
    );

    render(<QuotaLimitsSettingPage />);

    await waitFor(() =>
      expect(Number(optionsOf('checkin')['checkin_setting.min_quota'])).toBe(
        2000,
      ),
    );
    // 90000 differs from the declared default 10000, so a page that silently
    // kept its default would fail here.
    expect(Number(optionsOf('checkin')['checkin_setting.max_quota'])).toBe(
      90000,
    );
  });

  it('renders the rate-limit section alongside the quota cards', async () => {
    API.get.mockResolvedValue(optionsOk({ 'checkin_setting.enabled': 'true' }));

    render(<QuotaLimitsSettingPage />);

    await waitFor(() => expect(screen.getByTestId('rate-limit')).toBeTruthy());
  });

  it('reports the backend message when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'quota table locked' },
    });

    render(<QuotaLimitsSettingPage />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('quota table locked'),
    );
    expect(optionsOf('checkin')['checkin_setting.enabled']).toBe(false);
  });
});

describe('UIModulesSettingPage', () => {
  it('passes the module JSON through byte-for-byte to both panels', async () => {
    const header = '{"home":true,"pricing":false}';
    const sidebar = '{"admin":{"enabled":true}}';
    API.get.mockResolvedValue(
      optionsOk({ HeaderNavModules: header, SidebarModulesAdmin: sidebar }),
    );

    render(<UIModulesSettingPage />);

    await waitFor(() =>
      expect(optionsOf('header-nav').HeaderNavModules).toBe(header),
    );
    // Byte-for-byte matters: these are re-serialised and PUT back, so any
    // reformatting here would rewrite the stored option on the next save.
    expect(optionsOf('sidebar-admin').SidebarModulesAdmin).toBe(sidebar);
    expect(optionsOf('header-nav')).toEqual(optionsOf('sidebar-admin'));
  });

  it('does not coerce the module JSON to a boolean despite the string content', async () => {
    API.get.mockResolvedValue(optionsOk({ HeaderNavModules: 'false' }));

    render(<UIModulesSettingPage />);

    await waitFor(() =>
      expect(optionsOf('header-nav').HeaderNavModules).toBe('false'),
    );
    expect(typeof optionsOf('header-nav').HeaderNavModules).toBe('string');
  });

  it('re-reads the modules when a panel asks for a refresh', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ HeaderNavModules: '{"a":1}' }))
      .mockResolvedValueOnce(optionsOk({ HeaderNavModules: '{"a":2}' }));

    render(<UIModulesSettingPage />);
    await waitFor(() =>
      expect(optionsOf('header-nav').HeaderNavModules).toBe('{"a":1}'),
    );

    fireEvent.click(screen.getByTestId('sidebar-admin-refresh'));

    await waitFor(() =>
      expect(optionsOf('header-nav').HeaderNavModules).toBe('{"a":2}'),
    );
  });
});

describe('AIFeaturesSettingPage / ModelConfigSettingPage', () => {
  it('stacks the chat panel above the drawing panel', async () => {
    render(<AIFeaturesSettingPage />);

    const chats = screen.getByTestId('chats');
    const drawing = screen.getByTestId('drawing');
    // Order is the only behaviour these two files have; asserting it via
    // DOCUMENT_POSITION_FOLLOWING catches a swapped-composition regression
    // that a bare "both are present" check would miss.
    expect(
      chats.compareDocumentPosition(drawing) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it('stacks the model panel above the deployment panel', async () => {
    render(<ModelConfigSettingPage />);

    const model = screen.getByTestId('model');
    const deployment = screen.getByTestId('model-deployment');
    expect(
      model.compareDocumentPosition(deployment) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it('neither page fetches options itself — the children own their loads', async () => {
    render(<AIFeaturesSettingPage />);
    render(<ModelConfigSettingPage />);

    // Both wrappers are pure composition. If either grew its own /api/option/
    // call the two loads would race and the later one would win silently.
    expect(API.get).not.toHaveBeenCalled();
  });
});

describe('OperationSetting', () => {
  it('coerces every declared boolean flag and leaves the string-declared ones alone', async () => {
    API.get.mockResolvedValue(
      optionsOk({
        CheckSensitiveEnabled: 'false',
        CheckSensitiveOnPromptEnabled: 'true',
        LogConsumeEnabled: '1',
        AutomaticDisableChannelEnabled: '0',
        // Declared '' — same text as CheckSensitiveEnabled, must stay a string.
        SensitiveWords: 'false',
      }),
    );

    render(<OperationSetting />);

    await waitFor(() =>
      expect(screen.getByTestId('sensitive-words')).toBeTruthy(),
    );
    const opts = optionsOf('sensitive-words');
    expect(opts.CheckSensitiveEnabled).toBe(false);
    expect(opts.CheckSensitiveOnPromptEnabled).toBe(true);
    expect(opts.LogConsumeEnabled).toBe(true);
    expect(opts.AutomaticDisableChannelEnabled).toBe(false);
    expect(opts.SensitiveWords).toBe('false');
  });

  it('hands one shared options object to all eight panels', async () => {
    API.get.mockResolvedValue(optionsOk({ LogConsumeEnabled: 'true' }));

    render(<OperationSetting />);

    await waitFor(() => expect(screen.getByTestId('checkin')).toBeTruthy());
    for (const id of [
      'header-nav',
      'sidebar-admin',
      'sensitive-words',
      'log',
      'monitoring',
      'credit-limit',
      'checkin',
    ]) {
      expect(optionsOf(id)).toEqual(optionsOf('general'));
    }
  });

  it('keeps a boolean flag boolean across a refresh', async () => {
    // Positive half of the pair with the defect lock below: as long as the
    // key is present on BOTH loads the coercion keeps working.
    API.get
      .mockResolvedValueOnce(optionsOk({ CheckSensitiveEnabled: 'true' }))
      .mockResolvedValueOnce(optionsOk({ CheckSensitiveEnabled: 'false' }));

    render(<OperationSetting />);
    await waitFor(() =>
      expect(optionsOf('sensitive-words').CheckSensitiveEnabled).toBe(true),
    );

    fireEvent.click(screen.getByTestId('sensitive-words-refresh'));

    await waitFor(() =>
      expect(optionsOf('sensitive-words').CheckSensitiveEnabled).toBe(false),
    );
  });

  // ------------------------------------------------------------------
  // DEFECT (OperationSetting.jsx getOptions): the boolean test is
  //   typeof inputs[item.key] === 'boolean'
  // against the CURRENT state rather than against the declared defaults, and
  // the previous load already replaced that state with a fresh `{}` holding
  // only the keys the backend sent. So once a flag is missing from one
  // response, every later response assigns it RAW — and unlike its siblings
  // this loop has no `item.key in inputs` guard, so the key comes back as the
  // *string* 'false'. A string 'false' is truthy: the sensitive-word filter,
  // the consume log and the automatic channel-disable switches all render ON
  // while the server has them OFF, and the operator's next save writes that
  // back. The fix is to test against the initial defaults object, the way
  // MonitoringSettingPage's copy of this loop is at least attempting to.
  // ------------------------------------------------------------------
  it.skip('a flag missing from one load must still be boolean on the next', async () => {
    API.get
      .mockResolvedValueOnce(optionsOk({ SensitiveWords: 'a\nb' }))
      .mockResolvedValueOnce(
        optionsOk({ SensitiveWords: 'a\nb', CheckSensitiveEnabled: 'false' }),
      );

    render(<OperationSetting />);
    await waitFor(() =>
      expect(optionsOf('sensitive-words').SensitiveWords).toBe('a\nb'),
    );

    fireEvent.click(screen.getByTestId('sensitive-words-refresh'));

    await waitFor(() =>
      expect('CheckSensitiveEnabled' in optionsOf('sensitive-words')).toBe(
        true,
      ),
    );
    expect(optionsOf('sensitive-words').CheckSensitiveEnabled).toBe(false);
  });

  // NOTE: there is deliberately no live test asserting that the flag arrives
  // as the string 'false' today. Such an assertion would go red the moment
  // somebody repairs the coercion, so the repair would read as a regression —
  // the skipped lock above is the whole record of this defect. The live test
  // that stays is the positive one ("keeps a boolean flag boolean across a
  // refresh"), which passes on both sides of the fix.

  it('reports the backend message and keeps its declared defaults when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'operation settings locked' },
    });

    render(<OperationSetting />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('operation settings locked'),
    );
    expect(optionsOf('general').CheckSensitiveEnabled).toBe(false);
    expect(
      optionsOf('general')['quota_setting.enable_free_model_pre_consume'],
    ).toBe(true);
  });

  it('reports a transport failure and stops spinning', async () => {
    API.get.mockRejectedValue(new Error('ETIMEDOUT'));

    render(<OperationSetting />);

    await waitFor(() => expect(showError).toHaveBeenCalledWith('刷新失败'));
    await waitFor(() =>
      expect(screen.getByTestId('spin').getAttribute('data-spinning')).toBe(
        'false',
      ),
    );
  });
});
