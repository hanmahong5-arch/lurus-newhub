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
 * PersonalSetting — the one place in this directory that builds a request body
 * by hand. `saveNotificationSettings` reads eleven fields out of React state
 * (all of which arrive from form widgets as strings) and PUTs them to
 * /api/user/setting under *different* names with *different* types.
 *
 * That mapping is the whole risk surface: a threshold that goes out as the
 * string "250000" instead of the number 250000, a switch that goes out as
 * "false" (truthy) instead of false, a renamed key that quietly drops the
 * field. So every assertion below reads the ACTUAL argument handed to
 * API.put — `.toBe(250000)`, not `.toBeTruthy()` — and the fixtures are
 * chosen so that a pass-through bug and a correct conversion cannot produce
 * the same request.
 */
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('../../helpers', () => ({
  API: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
  copy: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  setStatusData: vi.fn(),
  setUserData: vi.fn(),
}));

// Child stubs. Each one records the props it was rendered with so a test can
// drive the real handler, and mirrors the *deciding* prop into the DOM so an
// assertion has something to look at. Deliberately NOT `() => null`: a stub
// that renders nothing still executes the parent branch and still counts as
// covered, while leaving no evidence of what was passed.
const captured = vi.hoisted(() => ({
  account: null,
  notification: null,
  header: null,
  twoFactor: null,
}));

vi.mock('./personal/components/UserInfoHeader', () => ({
  default: (props) => {
    captured.header = props;
    return React.createElement('div', {
      'data-testid': 'user-info-header',
      'data-username': String(props.userState?.user?.username ?? ''),
    });
  },
}));

vi.mock('./personal/cards/AccountManagement', () => ({
  default: (props) => {
    captured.account = props;
    return React.createElement(
      'div',
      {
        'data-testid': 'account-management',
        // The freshly minted token has to reach the card — that IS the
        // feature (it is the only time the user can see it). What matters is
        // that it is the value the server returned, not a stale/blank one.
        'data-system-token': String(props.systemToken ?? ''),
        'data-status-keys': Object.keys(props.status ?? {})
          .sort()
          .join(','),
      },
      'account',
    );
  },
}));

vi.mock('./personal/cards/NotificationSettings', () => ({
  default: (props) => {
    captured.notification = props;
    return React.createElement('div', {
      'data-testid': 'notification-settings',
      'data-settings': JSON.stringify(props.notificationSettings ?? null),
    });
  },
}));

vi.mock('./personal/cards/TwoFactorAuth', () => ({
  default: (props) => {
    captured.twoFactor = props;
    return React.createElement('div', { 'data-testid': 'two-factor' });
  },
}));

import PersonalSetting from './PersonalSetting';
import { UserContext } from '../../context/User';
import {
  API,
  copy,
  showError,
  showSuccess,
  setUserData,
  setStatusData,
} from '../../helpers';

const ok = (data) => ({ data: { success: true, data } });
const refused = (message) => ({ data: { success: false, message } });

/** The serialised `user.setting` blob the backend stores per user. */
const settingBlob = (overrides = {}) =>
  JSON.stringify({
    notify_type: 'webhook',
    quota_warning_threshold: 250000,
    webhook_url: 'https://hooks.example.test/abc',
    webhook_secret: 's3cr3t',
    notification_email: 'ops@example.test',
    bark_url: 'https://bark.example.test',
    gotify_url: 'https://gotify.example.test',
    gotify_token: 'gt-1',
    gotify_priority: 7,
    accept_unset_model_ratio_model: true,
    record_ip_log: false,
    ...overrides,
  });

const dispatch = vi.fn();

const renderWith = (user) =>
  render(
    <UserContext.Provider value={[{ user }, dispatch]}>
      <PersonalSetting />
    </UserContext.Provider>,
  );

/** Read back the settings object the NotificationSettings card received. */
const settingsOf = () =>
  JSON.parse(
    screen.getByTestId('notification-settings').getAttribute('data-settings'),
  );

/** The body of the last PUT to a given url. */
const lastPutBodyFor = (url) => {
  const call = [...API.put.mock.calls].reverse().find((c) => c[0] === url);
  return call ? call[1] : undefined;
};

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  API.post.mockReset();
  copy.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  setUserData.mockReset();
  setStatusData.mockReset();
  dispatch.mockReset();
  captured.account = null;
  captured.notification = null;
  localStorage.clear();
  // Both mount requests resolve by default; individual tests override.
  API.get.mockResolvedValue(ok({ username: 'ops' }));
});

describe('PersonalSetting — mount', () => {
  it('adopts the live /api/status payload and passes it to the account card', async () => {
    localStorage.setItem('status', JSON.stringify({ version: 'cached' }));
    API.get.mockImplementation((url) => {
      if (url === '/api/status')
        return Promise.resolve(ok({ version: 'live', email_login: true }));
      return Promise.resolve(ok({ username: 'ops' }));
    });

    renderWith(undefined);

    await waitFor(() =>
      expect(
        screen
          .getByTestId('account-management')
          .getAttribute('data-status-keys'),
      ).toBe('email_login,version'),
    );
    expect(setStatusData).toHaveBeenCalledWith({
      version: 'live',
      email_login: true,
    });
  });

  it('falls back to the cached status when /api/status is unreachable', async () => {
    localStorage.setItem('status', JSON.stringify({ version: 'cached' }));
    API.get.mockImplementation((url) => {
      if (url === '/api/status') return Promise.reject(new Error('offline'));
      return Promise.resolve(ok({ username: 'ops' }));
    });

    renderWith(undefined);

    // Negative half of the pair above: the cached copy is what survives, and
    // the failed probe must NOT be written back as if it were fresh.
    await waitFor(() =>
      expect(
        screen
          .getByTestId('account-management')
          .getAttribute('data-status-keys'),
      ).toBe('version'),
    );
    expect(setStatusData).not.toHaveBeenCalled();
  });

  it('pushes the fetched user into the context and the local user cache', async () => {
    API.get.mockImplementation((url) => {
      if (url === '/api/status') return Promise.resolve(ok({}));
      return Promise.resolve(ok({ id: 9, username: 'ops' }));
    });

    renderWith(undefined);

    await waitFor(() =>
      expect(dispatch).toHaveBeenCalledWith({
        type: 'login',
        payload: { id: 9, username: 'ops' },
      }),
    );
    expect(setUserData).toHaveBeenCalledWith({ id: 9, username: 'ops' });
  });

  it('reports the backend message and adopts nothing when /api/user/self is refused', async () => {
    API.get.mockImplementation((url) => {
      if (url === '/api/status') return Promise.resolve(ok({}));
      return Promise.resolve(refused('session expired'));
    });

    renderWith(undefined);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('session expired'),
    );
    expect(dispatch).not.toHaveBeenCalled();
    expect(setUserData).not.toHaveBeenCalled();
  });
});

describe('PersonalSetting — decoding the stored user.setting blob', () => {
  it('maps every snake_case backend key onto its camelCase form field', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });

    await waitFor(() =>
      expect(settingsOf().webhookUrl).toBe('https://hooks.example.test/abc'),
    );
    expect(settingsOf()).toEqual({
      warningType: 'webhook',
      warningThreshold: 250000,
      webhookUrl: 'https://hooks.example.test/abc',
      webhookSecret: 's3cr3t',
      notificationEmail: 'ops@example.test',
      barkUrl: 'https://bark.example.test',
      gotifyUrl: 'https://gotify.example.test',
      gotifyToken: 'gt-1',
      gotifyPriority: 7,
      acceptUnsetModelRatioModel: true,
      recordIpLog: false,
    });
  });

  it('keeps a stored gotify_priority of 0 instead of substituting the default', async () => {
    // Positive/negative pair with the threshold lock below: `gotify_priority`
    // is decoded with an explicit `!== undefined` test, so a deliberate 0
    // survives. This is the CORRECT pattern, and it is right here in the same
    // function as the broken one.
    renderWith({
      username: 'ops',
      setting: settingBlob({ gotify_priority: 0 }),
    });

    await waitFor(() => expect(settingsOf().gotifyPriority).toBe(0));
  });

  // ------------------------------------------------------------------
  // DEFECT (PersonalSetting.jsx, the user.setting decode effect):
  //   warningThreshold: settings.quota_warning_threshold || 500000
  // `||` cannot tell "absent" from "deliberately zero". An operator who sets
  // the quota-warning threshold to 0 (meaning "never warn me") gets 500000
  // put back into the form on the next page load, and the next save writes
  // 500000 to the server — the setting silently un-does itself. The sibling
  // field two lines below (`gotify_priority`) uses `!== undefined` and does
  // NOT have this bug, so the fix is a one-line change to match it.
  // Aggravating: the component's own useState default for this field is
  // 100000, a third value, so the form shows different "defaults" depending
  // on whether a setting blob exists at all.
  // ------------------------------------------------------------------
  it.skip('a deliberately-zero warning threshold must survive a reload', async () => {
    renderWith({
      username: 'ops',
      setting: settingBlob({ quota_warning_threshold: 0 }),
    });

    await waitFor(() =>
      expect(screen.getByTestId('notification-settings')).toBeTruthy(),
    );
    expect(settingsOf().warningThreshold).toBe(0);
  });

  // ------------------------------------------------------------------
  // DEFECT (PersonalSetting.jsx, mount effect + user.setting effect):
  // Both `JSON.parse(localStorage.getItem('status'))` and
  // `JSON.parse(userState.user.setting)` run unguarded inside a useEffect. A
  // truncated localStorage entry (quota-exceeded write, an interrupted tab)
  // or a corrupt setting column makes the parse throw out of the effect,
  // which unmounts the whole personal-settings subtree — the user loses
  // access to 2FA and token management with a blank page and no message,
  // and cannot self-repair because the bad value is re-read on every reload.
  // Every other JSON.parse on this path (NotificationSettings' sidebar
  // decode) is wrapped in try/catch.
  // ------------------------------------------------------------------
  it.skip('a corrupt user.setting blob must not blank the page', async () => {
    renderWith({ username: 'ops', setting: '{not json' });

    await waitFor(() => expect(screen.getByTestId('two-factor')).toBeTruthy());
  });

  it.skip('a corrupt cached status entry must not blank the page', async () => {
    localStorage.setItem('status', '{truncated');

    renderWith({ username: 'ops', setting: settingBlob() });

    await waitFor(() => expect(screen.getByTestId('two-factor')).toBeTruthy());
  });
});

describe('PersonalSetting — what saveNotificationSettings puts on the wire', () => {
  it('converts the string-typed form values to the numbers the API expects', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });
    API.get.mockResolvedValue(ok({ id: 9, username: 'ops' }));

    // These are the shapes Semi's InputNumber actually hands back once a user
    // has typed into the field: strings, not numbers. 250000/7 are chosen so
    // that neither the useState default (100000/5) nor the decoded blob value
    // could produce the same request by accident.
    act(() => {
      captured.notification.handleNotificationSettingChange(
        'warningThreshold',
        '310000',
      );
    });
    act(() => {
      captured.notification.handleNotificationSettingChange(
        'gotifyPriority',
        '8',
      );
    });
    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    const body = lastPutBodyFor('/api/user/setting');
    expect(body.quota_warning_threshold).toBe(310000);
    expect(typeof body.quota_warning_threshold).toBe('number');
    expect(body.gotify_priority).toBe(8);
    expect(typeof body.gotify_priority).toBe('number');
  });

  it('sends the booleans as real booleans under their snake_case names', async () => {
    // recordIpLog false / acceptUnsetModelRatioModel true — deliberately
    // opposite, so "sends both as true" and "sends the right values" are
    // distinguishable, and so a stringified 'false' (which is truthy on the
    // server side of a JSON decode into interface{}) shows up as a failure.
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    const body = lastPutBodyFor('/api/user/setting');
    expect(body.accept_unset_model_ratio_model).toBe(true);
    expect(body.record_ip_log).toBe(false);
    expect(Object.keys(body).sort()).toEqual([
      'accept_unset_model_ratio_model',
      'bark_url',
      'gotify_priority',
      'gotify_token',
      'gotify_url',
      'notification_email',
      'notify_type',
      'quota_warning_threshold',
      'record_ip_log',
      'webhook_secret',
      'webhook_url',
    ]);
  });

  it('carries an edited webhook url through unmodified', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });

    act(() => {
      captured.notification.handleNotificationSettingChange(
        'webhookUrl',
        'https://new.example.test/hook?a=1&b=2',
      );
    });
    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    expect(lastPutBodyFor('/api/user/setting').webhook_url).toBe(
      'https://new.example.test/hook?a=1&b=2',
    );
  });

  it('re-reads the user after a save so the form cannot drift from the server', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });
    API.get.mockClear();
    API.get.mockResolvedValue(ok({ id: 9, username: 'ops-renamed' }));

    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    expect(showSuccess).toHaveBeenCalled();
    expect(API.get).toHaveBeenCalledWith('/api/user/self');
    expect(dispatch).toHaveBeenCalledWith({
      type: 'login',
      payload: { id: 9, username: 'ops-renamed' },
    });
  });

  it('does not report success, and does not re-read the user, when the save is refused', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue(refused('threshold below minimum'));
    API.get.mockClear();
    showSuccess.mockClear();

    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    expect(showError).toHaveBeenCalledWith('threshold below minimum');
    expect(showSuccess).not.toHaveBeenCalled();
    expect(API.get).not.toHaveBeenCalled();
  });

  it('reports a transport failure instead of claiming the settings were saved', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockRejectedValue(new Error('ECONNRESET'));
    showSuccess.mockClear();

    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    expect(showError).toHaveBeenCalledWith('设置保存失败');
    expect(showSuccess).not.toHaveBeenCalled();
  });

  // ------------------------------------------------------------------
  // DEFECT (PersonalSetting.jsx, saveNotificationSettings):
  //   quota_warning_threshold: parseFloat(notificationSettings.warningThreshold)
  // has no NaN guard, unlike `gotify_priority` immediately below it which
  // does `isNaN(parsed) ? 5 : parsed`. Clearing the threshold input yields
  // '' -> parseFloat('') -> NaN, and JSON.stringify turns NaN into `null`,
  // so the request carries `"quota_warning_threshold": null`. The operator
  // is told "设置保存成功" while the server records a null threshold —
  // i.e. the quota alarm is silently disarmed by an empty text box.
  // ------------------------------------------------------------------
  it.skip('an emptied threshold must not be sent as NaN/null', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });

    act(() => {
      captured.notification.handleNotificationSettingChange(
        'warningThreshold',
        '',
      );
    });
    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    const sent = lastPutBodyFor('/api/user/setting').quota_warning_threshold;
    expect(Number.isFinite(sent)).toBe(true);
  });

  // The live counterpart to the lock above. It deliberately does NOT assert
  // "the value is NaN" (that would go red the day someone adds the guard) —
  // it asserts the invariant that has to hold either way: whatever leaves the
  // component for a numeric field must not be a *string*, because the server
  // decodes this field into a float.
  it('never lets a numeric field leave as a string, whatever the user typed', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    API.put.mockResolvedValue({ data: { success: true } });

    act(() => {
      captured.notification.handleNotificationSettingChange(
        'warningThreshold',
        '',
      );
    });
    await act(async () => {
      await captured.notification.saveNotificationSettings();
    });

    const body = lastPutBodyFor('/api/user/setting');
    expect(typeof body.quota_warning_threshold).not.toBe('string');
    expect(typeof body.gotify_priority).not.toBe('string');
  });
});

describe('PersonalSetting — handleNotificationSettingChange input shapes', () => {
  it('takes a bare value as-is', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    act(() => {
      captured.notification.handleNotificationSettingChange(
        'warningType',
        'bark',
      );
    });

    await waitFor(() => expect(settingsOf().warningType).toBe('bark'));
  });

  it('unwraps an event-shaped payload from its target.value', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    act(() => {
      captured.notification.handleNotificationSettingChange('gotifyToken', {
        target: { value: 'gt-2' },
      });
    });

    await waitFor(() => expect(settingsOf().gotifyToken).toBe('gt-2'));
  });

  it('falls back to target.checked when the event carries no value', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    act(() => {
      captured.notification.handleNotificationSettingChange('recordIpLog', {
        target: { checked: true },
      });
    });

    await waitFor(() => expect(settingsOf().recordIpLog).toBe(true));
  });

  // ------------------------------------------------------------------
  // DEFECT (PersonalSetting.jsx, handleNotificationSettingChange):
  //   value.target.value !== undefined ? value.target.value : value.target.checked
  // prefers `.value` over `.checked`. A native DOM checkbox change event
  // always carries BOTH — `.value` defaults to the string 'on' regardless of
  // state — so wiring any of the two switches to a plain <input type=checkbox>
  // (rather than Semi's Form.Switch, which passes a bare boolean) makes the
  // flag leave as the string 'on' even when unchecking. On the wire that is
  // `"record_ip_log": "on"`, which a Go json decode into a bool rejects, or
  // into interface{} accepts as a truthy string: IP logging turns itself back
  // on. Latent today because every caller passes a bare boolean; the ordering
  // is wrong regardless, since `.checked` is the authoritative field when it
  // is present.
  // ------------------------------------------------------------------
  it.skip('a checkbox event must be read from .checked, not from .value', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.notification).toBeTruthy());

    act(() => {
      captured.notification.handleNotificationSettingChange('recordIpLog', {
        target: { value: 'on', checked: false },
      });
    });

    await waitFor(() => expect(settingsOf().recordIpLog).toBe(false));
  });
});

describe('PersonalSetting — access token', () => {
  it('adopts the server-issued token and copies exactly that value', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.account).toBeTruthy());

    API.get.mockResolvedValue(ok('sk-issued-9f2a'));

    await act(async () => {
      await captured.account.generateAccessToken();
    });

    // Showing the user their own freshly-minted token is the feature: it is
    // the only moment it is retrievable. What is asserted is that the value
    // shown/copied is the one the server just returned.
    expect(copy).toHaveBeenCalledWith('sk-issued-9f2a');
    await waitFor(() =>
      expect(
        screen
          .getByTestId('account-management')
          .getAttribute('data-system-token'),
      ).toBe('sk-issued-9f2a'),
    );
    expect(showSuccess).toHaveBeenCalled();
  });

  it('copies nothing and keeps the field blank when the reset is refused', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.account).toBeTruthy());

    API.get.mockResolvedValue(refused('rate limited'));
    copy.mockClear();
    showSuccess.mockClear();

    await act(async () => {
      await captured.account.generateAccessToken();
    });

    expect(showError).toHaveBeenCalledWith('rate limited');
    expect(copy).not.toHaveBeenCalled();
    expect(showSuccess).not.toHaveBeenCalled();
    expect(
      screen
        .getByTestId('account-management')
        .getAttribute('data-system-token'),
    ).toBe('');
  });

  it('copies the field contents when the token box is clicked', async () => {
    renderWith({ username: 'ops', setting: settingBlob() });
    await waitFor(() => expect(captured.account).toBeTruthy());

    const select = vi.fn();
    await act(async () => {
      await captured.account.handleSystemTokenClick({
        target: { select, value: 'sk-in-the-box' },
      });
    });

    expect(select).toHaveBeenCalled();
    expect(copy).toHaveBeenCalledWith('sk-in-the-box');
  });
});
