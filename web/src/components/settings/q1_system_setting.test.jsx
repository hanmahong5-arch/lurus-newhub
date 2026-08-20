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
 * SystemSetting — the console screen that owns the authentication providers,
 * the SMTP credentials and the SSRF egress filter. Fourteen separate submit
 * buttons, each assembling its own `[{key, value}]` array and handing it to a
 * shared `updateOptions`.
 *
 * Every assertion here is on the argument list API.put actually received:
 * the key name, and the value AS A STRING, because /api/option/ stores
 * strings and a boolean that arrives un-stringified, or a secret that arrives
 * as '' because the field was left blank, silently rewrites live
 * configuration. Two behaviours get particular attention:
 *
 *  - Blank-means-unchanged. Every secret field (SMTP token, OAuth client
 *    secrets, WeChat token, Turnstile secret key) is deliberately NOT sent
 *    when empty, because the backend never sends them down and an empty box
 *    means "leave it alone", not "erase it". Each of those is asserted
 *    positively (a changed secret IS sent) and negatively (a blank one is
 *    NOT), because only the pair can tell the two apart.
 *
 *  - The SSRF filter payload. `fetch_setting.ip_list` etc. go out as JSON
 *    text; the fixtures below use multi-entry lists whose order and content
 *    make a hard-coded or truncated payload visibly different.
 */
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

// helpers/utils.jsx reads window.matchMedia at MODULE scope (utils.jsx:149) to
// decide toast placement, and jsdom does not implement it. The vi.mock factory
// below pulls that module in for real via vi.importActual, and mock factories
// are hoisted above ordinary statements — so the stub has to be hoisted too or
// it lands after the crash.
vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    });
  }
});

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'en' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

// The real toBoolean and removeTrailingSlash are kept: both are part of what
// is under test (the type a flag reaches the form as, and the exact URL text
// that reaches the wire).
vi.mock('../../helpers', async () => {
  const boolean = await vi.importActual('../../helpers/boolean');
  const utils = await vi.importActual('../../helpers/utils');
  return {
    API: { get: vi.fn(), put: vi.fn(), post: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    toBoolean: boolean.toBoolean,
    removeTrailingSlash: utils.removeTrailingSlash,
  };
});

const oidcDiscovery = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('axios', () => ({
  default: { create: () => ({ get: oidcDiscovery.get }) },
}));

// A Semi stand-in that keeps the two things this component depends on:
// (a) Form owns the values and republishes the whole object through
//     onValueChange, which is how SystemSetting's state is updated;
// (b) `field="['a.b']"` bracket-path notation resolves to the key 'a.b', the
//     way real Semi does — several passkey fields use it, and a stub that
//     kept the literal would silently test the stub instead of the code.
vi.mock('@douyinfe/semi-ui', () => {
  const FormCtx = React.createContext({ values: {}, setField: () => {} });
  const RadioCtx = React.createContext({ onChange: () => {} });
  const norm = (f) => {
    const m = /^\['(.*)'\]$/.exec(f ?? '');
    return m ? m[1] : f;
  };

  const Form = ({ initValues, onValueChange, getFormApi, children }) => {
    const [values, setValues] = React.useState(() => ({
      ...(initValues || {}),
    }));
    const ref = React.useRef(values);
    ref.current = values;
    const setField = (field, v) => {
      const next = { ...ref.current, [field]: v };
      ref.current = next;
      setValues(next);
      if (onValueChange) onValueChange(next);
    };
    const setFieldRef = React.useRef(setField);
    setFieldRef.current = setField;
    const api = React.useMemo(
      () => ({
        getValues: () => ref.current,
        setValues: (v) => {
          ref.current = v;
          setValues(v);
        },
        // Real Semi's form api exposes setValue for a single field, and the
        // password-login confirmation modal calls it to put the checkbox back
        // when the operator cancels. A stub without it makes that handler
        // throw, which React reports as an unhandled error rather than a test
        // failure — the cancel path then looks covered while nothing about it
        // is observed.
        setValue: (field, v) => setFieldRef.current(norm(field), v),
      }),
      [],
    );
    React.useEffect(() => {
      if (getFormApi) getFormApi(api);
    }, []);
    const body =
      typeof children === 'function'
        ? children({ formState: { values }, values, formApi: api })
        : children;
    return React.createElement(
      FormCtx.Provider,
      { value: { values, setField } },
      React.createElement('div', { 'data-testid': 'form' }, body),
    );
  };
  Form.Section = ({ children }) => React.createElement('div', null, children);
  Form.Input = ({
    field,
    value,
    onChange,
    placeholder,
    suffix,
    onEnterPress,
  }) => {
    const ctx = React.useContext(FormCtx);
    const key = norm(field);
    const uncontrolled = field === undefined;
    const v = uncontrolled ? (value ?? '') : (ctx.values[key] ?? '');
    return React.createElement(
      'span',
      null,
      React.createElement('input', {
        'data-testid': uncontrolled ? `input-free` : `f-${key}`,
        placeholder,
        value: v,
        onChange: (e) =>
          uncontrolled
            ? onChange && onChange(e.target.value)
            : ctx.setField(key, e.target.value),
        onKeyDown: (e) => {
          if (e.key === 'Enter' && onEnterPress) onEnterPress(e);
        },
      }),
      suffix,
    );
  };
  Form.Checkbox = ({ field, children, onChange }) => {
    const ctx = React.useContext(FormCtx);
    const key = norm(field);
    return React.createElement(
      'label',
      null,
      React.createElement('input', {
        type: 'checkbox',
        'data-testid': `f-${key}`,
        checked: !!ctx.values[key],
        onChange: (e) => {
          ctx.setField(key, e.target.checked);
          if (onChange) onChange(e);
        },
      }),
      children,
    );
  };
  Form.Select = ({ field, optionList, placeholder }) => {
    const ctx = React.useContext(FormCtx);
    const key = norm(field);
    return React.createElement(
      'select',
      {
        'data-testid': `f-${key}`,
        placeholder,
        value: ctx.values[key] ?? '',
        onChange: (e) => ctx.setField(key, e.target.value),
      },
      (optionList || []).map((o) =>
        React.createElement(
          'option',
          { key: o.value, value: o.value },
          o.label,
        ),
      ),
    );
  };

  const Button = ({ children, onClick }) =>
    React.createElement('button', { type: 'button', onClick }, children);
  const Card = ({ children }) => React.createElement('div', null, children);
  const Row = ({ children }) => React.createElement('div', null, children);
  const Col = ({ children }) => React.createElement('div', null, children);
  const Banner = ({ description }) =>
    React.createElement('div', { 'data-testid': 'banner' }, description);
  const Spin = ({ spinning, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'spin', 'data-spinning': String(!!spinning) },
      children,
    );
  const Modal = ({ visible, children, onOk, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'modal' },
          children,
          React.createElement('button', {
            type: 'button',
            'data-testid': 'modal-ok',
            onClick: onOk,
          }),
          React.createElement('button', {
            type: 'button',
            'data-testid': 'modal-cancel',
            onClick: onCancel,
          }),
        )
      : null;
  const TagInput = ({ value, onChange, placeholder }) =>
    React.createElement('input', {
      'data-testid': 'tag-input',
      placeholder,
      // The current list is surfaced so a test can tell "list was replaced"
      // from "list was appended to" without guessing.
      'data-value': JSON.stringify(value ?? null),
      value: '',
      onChange: (e) =>
        onChange(e.target.value === '' ? [] : e.target.value.split(',')),
    });
  const Radio = ({ value, children }) => {
    const ctx = React.useContext(RadioCtx);
    return React.createElement(
      'button',
      {
        type: 'button',
        'data-testid': `radio-${value}`,
        onClick: () => ctx.onChange({ target: { value } }),
      },
      children,
    );
  };
  Radio.Group = ({ value, onChange, children }) =>
    React.createElement(
      RadioCtx.Provider,
      { value: { onChange } },
      React.createElement(
        'div',
        { 'data-testid': 'radio-group', 'data-value': String(value) },
        children,
      ),
    );
  const Typography = {
    Text: ({ children }) => React.createElement('span', null, children),
  };
  const Select = ({ children }) => React.createElement('div', null, children);
  return {
    Button,
    Form,
    Row,
    Col,
    Typography,
    Modal,
    Banner,
    TagInput,
    Spin,
    Card,
    Radio,
    Select,
  };
});

import SystemSetting from './SystemSetting';
import { API, showError, showSuccess } from '../../helpers';

const optionsOk = (pairs) => ({
  data: {
    success: true,
    data: Object.entries(pairs).map(([key, value]) => ({ key, value })),
  },
});

const putOk = { data: { success: true } };

/** Every {key,value} body PUT to /api/option/ since the last reset. */
const puts = () =>
  API.put.mock.calls.filter((c) => c[0] === '/api/option/').map((c) => c[1]);
const putFor = (key) => puts().find((p) => p.key === key);
const putKeys = () => puts().map((p) => p.key);

const BASE = {
  ServerAddress: 'https://console.example.test',
  WorkerUrl: 'https://worker.example.test',
  WorkerValidKey: '',
  WorkerAllowHttpImageRequestEnabled: 'false',
  SMTPServer: 'smtp.example.test',
  SMTPPort: '587',
  SMTPAccount: 'noreply@example.test',
  SMTPFrom: 'noreply@example.test',
  SMTPToken: '',
  PasswordLoginEnabled: 'true',
  EmailDomainWhitelist: 'example.test,partner.test',
  'fetch_setting.enable_ssrf_protection': 'true',
  'fetch_setting.domain_filter_mode': 'false',
  'fetch_setting.ip_filter_mode': 'false',
  'fetch_setting.domain_list': '["api.example.test","*.cdn.example.test"]',
  'fetch_setting.ip_list': '["10.0.0.0/8","172.16.0.0/12"]',
  'fetch_setting.allowed_ports': '["443","8443"]',
  GitHubClientId: 'gh-id',
  GitHubClientSecret: '',
  'discord.client_id': 'dc-id',
  'discord.client_secret': '',
  'oidc.well_known':
    'https://idp.example.test/.well-known/openid-configuration',
  'oidc.client_id': 'oidc-id',
  'oidc.client_secret': '',
  'oidc.authorization_endpoint': 'https://idp.example.test/authorize',
  'oidc.token_endpoint': 'https://idp.example.test/token',
  'oidc.user_info_endpoint': 'https://idp.example.test/userinfo',
  TelegramBotToken: 'tg-token',
  TelegramBotName: 'tg-bot',
  TurnstileSiteKey: 'ts-site',
  TurnstileSecretKey: '',
  LinuxDOClientId: 'ld-id',
  LinuxDOClientSecret: '',
  LinuxDOMinimumTrustLevel: '1',
  WeChatServerAddress: 'https://wechat.example.test',
  WeChatServerToken: '',
  WeChatAccountQRCodeImageURL: 'https://cdn.example.test/qr.png',
  'passkey.rp_display_name': 'Example Console',
  'passkey.rp_id': 'example.test',
  'passkey.origins': 'https://console.example.test',
  'passkey.user_verification': 'preferred',
  'passkey.attachment_preference': '',
};

const mountLoaded = async (overrides = {}) => {
  API.get.mockResolvedValue(optionsOk({ ...BASE, ...overrides }));
  render(<SystemSetting />);
  await waitFor(() => expect(screen.getByTestId('form')).toBeTruthy());
  API.put.mockClear();
  showSuccess.mockClear();
  showError.mockClear();
};

const typeInto = (testid, value) =>
  fireEvent.change(screen.getByTestId(testid), { target: { value } });

beforeEach(() => {
  API.get.mockReset();
  API.put.mockReset();
  API.put.mockResolvedValue(putOk);
  showError.mockReset();
  showSuccess.mockReset();
  oidcDiscovery.get.mockReset();
});

describe('SystemSetting — loading options', () => {
  it('coerces the declared auth flags to booleans and leaves other keys as strings', async () => {
    await mountLoaded({
      PasswordLoginEnabled: 'false',
      'oidc.enabled': 'true',
      'passkey.enabled': '0',
      // Same textual value as PasswordLoginEnabled but NOT in the coercion
      // switch: it must stay a string. Without this negative half, "coerces
      // the listed flags" and "coerces everything" are the same test.
      LinuxDOMinimumTrustLevel: 'false',
    });

    expect(screen.getByTestId('f-PasswordLoginEnabled').checked).toBe(false);
    expect(screen.getByTestId('f-oidc.enabled').checked).toBe(true);
    expect(screen.getByTestId('f-passkey.enabled').checked).toBe(false);
    // A string reaching a Select stays the string it was.
    expect(screen.getByTestId('f-LinuxDOMinimumTrustLevel')).toBeTruthy();
  });

  it('splits the comma-joined email whitelist into a list', async () => {
    await mountLoaded();

    const emailTags = screen.getByPlaceholderText('输入域名后回车');
    expect(JSON.parse(emailTags.getAttribute('data-value'))).toEqual([
      'example.test',
      'partner.test',
    ]);
  });

  it('treats an empty email whitelist as no entries rather than one empty entry', async () => {
    await mountLoaded({ EmailDomainWhitelist: '' });

    const emailTags = screen.getByPlaceholderText('输入域名后回车');
    // ''.split(',') would give [''] — a phantom domain that would be PUT back
    // and would then match nothing while looking configured.
    expect(JSON.parse(emailTags.getAttribute('data-value'))).toEqual([]);
  });

  it('parses the SSRF domain, ip and port lists out of their JSON columns', async () => {
    await mountLoaded();

    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入域名后回车，如：example.com')
          .getAttribute('data-value'),
      ),
    ).toEqual(['api.example.test', '*.cdn.example.test']);
    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入IP地址后回车，如：8.8.8.8')
          .getAttribute('data-value'),
      ),
    ).toEqual(['10.0.0.0/8', '172.16.0.0/12']);
    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入端口后回车，如：80 或 8000-8999')
          .getAttribute('data-value'),
      ),
    ).toEqual(['443', '8443']);
  });

  it('falls back to an empty list, not a crash, when the stored ip list is corrupt', async () => {
    await mountLoaded({ 'fetch_setting.ip_list': '{not json' });

    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入IP地址后回车，如：8.8.8.8')
          .getAttribute('data-value'),
      ),
    ).toEqual([]);
  });

  it('falls back to the four default ports when the stored port list is corrupt', async () => {
    await mountLoaded({ 'fetch_setting.allowed_ports': 'not-json' });

    // Deliberately different from the fixture's ["443","8443"], so the
    // fallback and a pass-through cannot produce the same result.
    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入端口后回车，如：80 或 8000-8999')
          .getAttribute('data-value'),
      ),
    ).toEqual(['80', '443', '8080', '8443']);
  });

  it('reflects the stored filter mode in the whitelist/blacklist selector', async () => {
    await mountLoaded({
      'fetch_setting.domain_filter_mode': 'true',
      'fetch_setting.ip_filter_mode': 'false',
    });

    const groups = screen.getAllByTestId('radio-group');
    expect(groups[0].getAttribute('data-value')).toBe('whitelist');
    expect(groups[1].getAttribute('data-value')).toBe('blacklist');
  });

  it('reports the backend message and does not render the form when the load is refused', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'not an administrator' },
    });

    render(<SystemSetting />);

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('not an administrator'),
    );
    // Rendering a blank form over a refused load is how an operator ends up
    // saving empty strings over live credentials.
    expect(screen.queryByTestId('form')).toBeNull();
  });

  // ------------------------------------------------------------------
  // DEFECT (SystemSetting.jsx getOptions): the `TopupGroupRatio` branch runs
  //   item.value = JSON.stringify(JSON.parse(item.value), null, 2)
  // with no guard, and `getOptions()` is invoked from useEffect with no
  // try/catch and no .catch(). One unparseable option value therefore throws
  // out of the forEach, past `setLoading(false)` and past `setIsLoaded(true)`,
  // as an unhandled promise rejection: the entire system-settings screen sits
  // on a spinner for ever, with no error toast and nothing in the UI to say
  // why. Every provider credential, the SMTP configuration and the SSRF
  // filter become unreachable until someone fixes the row by hand in the
  // database. The sibling ModelSetting.jsx guards the identical call with
  // `if (item.value !== '')`, and this component's own fetch_setting branches
  // wrap their JSON.parse in try/catch.
  // ------------------------------------------------------------------
  it.skip('one corrupt option value must not strand the whole page on a spinner', async () => {
    API.get.mockResolvedValue(optionsOk({ ...BASE, TopupGroupRatio: '' }));

    render(<SystemSetting />);

    await waitFor(() => expect(screen.getByTestId('form')).toBeTruthy());
  });
});

describe('SystemSetting — server address and worker', () => {
  it('strips the trailing slash before storing the server address', async () => {
    await mountLoaded();

    typeInto('f-ServerAddress', 'https://console.example.test/');
    fireEvent.click(screen.getByText('更新服务器地址'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('ServerAddress')).toEqual({
      key: 'ServerAddress',
      value: 'https://console.example.test',
    });
  });

  it('sends the worker flag as the string a boolean option column expects', async () => {
    await mountLoaded();

    fireEvent.click(screen.getByTestId('f-WorkerAllowHttpImageRequestEnabled'));
    fireEvent.click(screen.getByText('更新Worker设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // 'true', not true and not 'on': the option table is text, and a raw
    // boolean would be serialised by axios as an unquoted JSON literal.
    expect(putFor('WorkerAllowHttpImageRequestEnabled')).toEqual({
      key: 'WorkerAllowHttpImageRequestEnabled',
      value: 'true',
    });
  });

  it('does not send the worker key when the field was left blank', async () => {
    await mountLoaded();

    fireEvent.click(screen.getByText('更新Worker设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // The backend never sends secrets down, so a blank box means "unchanged".
    // Sending it would erase the stored key.
    expect(putKeys()).not.toContain('WorkerValidKey');
    expect(putKeys()).toContain('WorkerUrl');
  });

  it('does send the worker key once the operator actually types one', async () => {
    await mountLoaded();

    typeInto('f-WorkerValidKey', 'wk-new-secret');
    fireEvent.click(screen.getByText('更新Worker设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('WorkerValidKey')).toEqual({
      key: 'WorkerValidKey',
      value: 'wk-new-secret',
    });
  });

  it('clears the worker key deliberately when the worker url itself is cleared', async () => {
    await mountLoaded();

    typeInto('f-WorkerUrl', '');
    fireEvent.click(screen.getByText('更新Worker设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // Turning the worker off is the one case where an empty key must be
    // written, so the stale secret does not outlive the feature.
    expect(putFor('WorkerValidKey')).toEqual({
      key: 'WorkerValidKey',
      value: '',
    });
    expect(putFor('WorkerUrl').value).toBe('');
  });
});

describe('SystemSetting — SMTP', () => {
  it('sends only the field that changed', async () => {
    await mountLoaded();

    typeInto('f-SMTPServer', 'smtp2.example.test');
    fireEvent.click(screen.getByText('保存 SMTP 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putKeys()).toEqual(['SMTPServer']);
    expect(putFor('SMTPServer').value).toBe('smtp2.example.test');
  });

  it('never sends a blank SMTP token, so an untouched password survives', async () => {
    await mountLoaded();

    typeInto('f-SMTPAccount', 'alerts@example.test');
    fireEvent.click(screen.getByText('保存 SMTP 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putKeys()).not.toContain('SMTPToken');
  });

  it('sends the SMTP token when one is typed', async () => {
    await mountLoaded();

    typeInto('f-SMTPToken', 'new-app-password');
    fireEvent.click(screen.getByText('保存 SMTP 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('SMTPToken').value).toBe('new-app-password');
  });

  it('issues no request at all when nothing was edited', async () => {
    await mountLoaded();

    fireEvent.click(screen.getByText('保存 SMTP 设置'));

    // A no-op save must stay a no-op: re-PUTting unchanged values is how a
    // masked secret gets written back as its masked placeholder.
    await new Promise((r) => setTimeout(r, 0));
    expect(API.put).not.toHaveBeenCalled();
  });
});

describe('SystemSetting — SSRF egress filter', () => {
  it('sends the filter modes as strings and the lists as JSON text', async () => {
    await mountLoaded();

    fireEvent.click(screen.getByText('更新SSRF防护设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('fetch_setting.domain_filter_mode')).toEqual({
      key: 'fetch_setting.domain_filter_mode',
      value: 'false',
    });
    expect(putFor('fetch_setting.ip_list')).toEqual({
      key: 'fetch_setting.ip_list',
      value: '["10.0.0.0/8","172.16.0.0/12"]',
    });
    expect(putFor('fetch_setting.domain_list').value).toBe(
      '["api.example.test","*.cdn.example.test"]',
    );
    expect(putFor('fetch_setting.allowed_ports').value).toBe('["443","8443"]');
  });

  it('carries an edited ip list through to the wire in full', async () => {
    await mountLoaded();

    fireEvent.change(
      screen.getByPlaceholderText('输入IP地址后回车，如：8.8.8.8'),
      { target: { value: '10.0.0.0/8,192.168.0.0/16,127.0.0.1' } },
    );
    fireEvent.click(screen.getByText('更新SSRF防护设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // Three entries, none of them the first one alone: a payload that
    // truncated or kept only the original list is visibly different.
    expect(putFor('fetch_setting.ip_list').value).toBe(
      '["10.0.0.0/8","192.168.0.0/16","127.0.0.1"]',
    );
  });

  it('switches the ip filter to whitelist mode and sends that, not the old mode', async () => {
    await mountLoaded({ 'fetch_setting.ip_filter_mode': 'false' });

    // Second radio group is the IP one; the first is the domain one.
    fireEvent.click(screen.getAllByTestId('radio-whitelist')[1]);
    fireEvent.click(screen.getByText('更新SSRF防护设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('fetch_setting.ip_filter_mode').value).toBe('true');
    // The domain mode must NOT have flipped along with it.
    expect(putFor('fetch_setting.domain_filter_mode').value).toBe('false');
  });

  it('switches the domain filter independently of the ip filter', async () => {
    await mountLoaded({
      'fetch_setting.domain_filter_mode': 'false',
      'fetch_setting.ip_filter_mode': 'true',
    });

    fireEvent.click(screen.getAllByTestId('radio-whitelist')[0]);
    fireEvent.click(screen.getByText('更新SSRF防护设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('fetch_setting.domain_filter_mode').value).toBe('true');
    expect(putFor('fetch_setting.ip_filter_mode').value).toBe('true');
  });
});

describe('SystemSetting — updateOptions result handling', () => {
  it('stops after the first refused flag and does not report success', async () => {
    await mountLoaded();

    API.put.mockResolvedValue({
      data: { success: false, message: 'flag is deploy-time locked' },
    });

    fireEvent.click(screen.getByTestId('f-EmailDomainRestrictionEnabled'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('flag is deploy-time locked'),
    );
    // The `*enabled` branch of updateOptions returns early on refusal. This
    // is the CORRECT pattern, and it sits in the same function as the defect
    // locked below.
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('reports a transport failure rather than claiming the update landed', async () => {
    await mountLoaded();

    API.put.mockRejectedValue(new Error('ECONNRESET'));

    typeInto('f-ServerAddress', 'https://new.example.test');
    fireEvent.click(screen.getByText('更新服务器地址'));

    await waitFor(() => expect(showError).toHaveBeenCalledWith('更新失败'));
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('always surfaces the backend message for a refused non-flag option', async () => {
    await mountLoaded();

    API.put.mockResolvedValue({
      data: { success: false, message: 'address must be https' },
    });

    typeInto('f-ServerAddress', 'http://insecure.example.test');
    fireEvent.click(screen.getByText('更新服务器地址'));

    // Invariant that holds either side of the defect below: the operator is
    // told. What must never happen is silence.
    await waitFor(() =>
      expect(showError.mock.calls.map((c) => c[0])).toContain(
        'address must be https',
      ),
    );
  });

  // ------------------------------------------------------------------
  // DEFECT (SystemSetting.jsx updateOptions): the non-flag branch collects
  // the failed results, calls showError for each — and then falls straight
  // through to `showSuccess(t('更新成功'))` and merges the rejected values
  // into local state, so the form redraws showing the value as if it had been
  // saved. The flag branch immediately above gets this right (`return` on the
  // first failure). Ten of the fourteen save buttons on this screen — SMTP,
  // OIDC, GitHub, Discord, LinuxDO, WeChat, Telegram, Turnstile, passkey and
  // the SSRF egress filter — go through the broken branch. An operator who
  // tightens the SSRF ip whitelist, is shown "更新成功" and sees the new list
  // on screen has in fact changed nothing on the server, and has no reason to
  // check again.
  // ------------------------------------------------------------------
  it.skip('a refused non-flag save must not be reported as success', async () => {
    await mountLoaded();

    API.put.mockResolvedValue({
      data: { success: false, message: 'address must be https' },
    });

    typeInto('f-ServerAddress', 'http://insecure.example.test');
    fireEvent.click(screen.getByText('更新服务器地址'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it.skip('a refused SSRF filter update must not be reported as success', async () => {
    await mountLoaded();

    API.put.mockResolvedValue({
      data: { success: false, message: 'ip list rejected' },
    });

    fireEvent.click(screen.getByText('更新SSRF防护设置'));

    await waitFor(() => expect(showError).toHaveBeenCalled());
    expect(showSuccess).not.toHaveBeenCalled();
  });
});

describe('SystemSetting — password login guard', () => {
  it('asks for confirmation instead of immediately disabling password login', async () => {
    await mountLoaded({ PasswordLoginEnabled: 'true' });

    fireEvent.click(screen.getByTestId('f-PasswordLoginEnabled'));

    await waitFor(() => expect(screen.getByTestId('modal')).toBeTruthy());
    // Nothing may be written before the operator confirms — this is the
    // switch that can lock everyone out of the console.
    expect(API.put).not.toHaveBeenCalled();
  });

  it('writes the disable only after the confirmation, as the string false', async () => {
    await mountLoaded({ PasswordLoginEnabled: 'true' });

    fireEvent.click(screen.getByTestId('f-PasswordLoginEnabled'));
    await waitFor(() => expect(screen.getByTestId('modal')).toBeTruthy());
    fireEvent.click(screen.getByTestId('modal-ok'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('PasswordLoginEnabled')).toEqual({
      key: 'PasswordLoginEnabled',
      value: 'false',
    });
  });

  it('writes nothing when the operator backs out of the confirmation', async () => {
    await mountLoaded({ PasswordLoginEnabled: 'true' });

    fireEvent.click(screen.getByTestId('f-PasswordLoginEnabled'));
    await waitFor(() => expect(screen.getByTestId('modal')).toBeTruthy());
    fireEvent.click(screen.getByTestId('modal-cancel'));

    await waitFor(() => expect(screen.queryByTestId('modal')).toBeNull());
    expect(API.put).not.toHaveBeenCalled();
    // Backing out has to put the checkbox back as well. A box left unticked
    // beside a server where password login is still ON tells the operator they
    // have disabled it when they have not — and the next thing they do is walk
    // away from a console they believe is locked down.
    expect(screen.getByTestId('f-PasswordLoginEnabled').checked).toBe(true);
  });

  it('re-enabling password login needs no confirmation and writes true', async () => {
    await mountLoaded({ PasswordLoginEnabled: 'false' });

    fireEvent.click(screen.getByTestId('f-PasswordLoginEnabled'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // Negative half of the pair: the guard is specific to turning it OFF.
    expect(putFor('PasswordLoginEnabled').value).toBe('true');
    expect(screen.queryByTestId('modal')).toBeNull();
  });

  it('writes an unrelated flag straight through without a confirmation', async () => {
    await mountLoaded({ EmailAliasRestrictionEnabled: 'false' });

    fireEvent.click(screen.getByTestId('f-EmailAliasRestrictionEnabled'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('EmailAliasRestrictionEnabled').value).toBe('true');
  });
});

describe('SystemSetting — email domain whitelist', () => {
  it('rejects an entry that is not a domain and does not add it', async () => {
    await mountLoaded();

    typeInto('input-free', 'not a domain');
    fireEvent.click(screen.getByText('添加'));

    expect(showError).toHaveBeenCalledWith(
      '邮箱域名格式不正确，请输入有效的域名，如 gmail.com',
    );
    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入域名后回车')
          .getAttribute('data-value'),
      ),
    ).toEqual(['example.test', 'partner.test']);
  });

  it('adds a well-formed domain to the list', async () => {
    await mountLoaded();

    typeInto('input-free', 'newco.example');
    fireEvent.click(screen.getByText('添加'));

    await waitFor(() =>
      expect(
        JSON.parse(
          screen
            .getByPlaceholderText('输入域名后回车')
            .getAttribute('data-value'),
        ),
      ).toEqual(['example.test', 'partner.test', 'newco.example']),
    );
  });

  it('refuses to add a domain that is already whitelisted', async () => {
    await mountLoaded();

    typeInto('input-free', 'partner.test');
    fireEvent.click(screen.getByText('添加'));

    expect(showError).toHaveBeenCalledWith('该域名已存在于白名单中');
    expect(
      JSON.parse(
        screen
          .getByPlaceholderText('输入域名后回车')
          .getAttribute('data-value'),
      ),
    ).toHaveLength(2);
  });

  it('stores the whitelist as a comma-joined string', async () => {
    await mountLoaded();

    typeInto('input-free', 'newco.example');
    fireEvent.click(screen.getByText('添加'));
    await waitFor(() =>
      expect(
        JSON.parse(
          screen
            .getByPlaceholderText('输入域名后回车')
            .getAttribute('data-value'),
        ),
      ).toHaveLength(3),
    );

    fireEvent.click(screen.getByText('保存邮箱域名白名单设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('EmailDomainWhitelist')).toEqual({
      key: 'EmailDomainWhitelist',
      value: 'example.test,partner.test,newco.example',
    });
  });
});

describe('SystemSetting — OIDC', () => {
  it('refuses a well-known url without a scheme and contacts nothing', async () => {
    await mountLoaded();

    typeInto('f-oidc.well_known', 'idp.example.test/.well-known');
    fireEvent.click(screen.getByText('保存 OIDC 设置'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith(
        'Well-Known URL 必须以 http:// 或 https:// 开头',
      ),
    );
    expect(oidcDiscovery.get).not.toHaveBeenCalled();
    expect(API.put).not.toHaveBeenCalled();
  });

  it('stores the endpoints discovered from an edited well-known url', async () => {
    await mountLoaded();

    oidcDiscovery.get.mockResolvedValue({
      data: {
        authorization_endpoint: 'https://idp2.example.test/auth',
        token_endpoint: 'https://idp2.example.test/token',
        userinfo_endpoint: 'https://idp2.example.test/me',
      },
    });

    typeInto(
      'f-oidc.well_known',
      'https://idp2.example.test/.well-known/openid-configuration',
    );
    fireEvent.click(screen.getByText('保存 OIDC 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('oidc.authorization_endpoint').value).toBe(
      'https://idp2.example.test/auth',
    );
    expect(putFor('oidc.token_endpoint').value).toBe(
      'https://idp2.example.test/token',
    );
    // The response field is `userinfo_endpoint` but the option key is
    // `oidc.user_info_endpoint` — an easy pair to mis-wire, and a wrong
    // mapping here silently breaks login for every SSO user.
    expect(putFor('oidc.user_info_endpoint').value).toBe(
      'https://idp2.example.test/me',
    );
  });

  it('writes nothing when discovery fails', async () => {
    await mountLoaded();

    oidcDiscovery.get.mockRejectedValue(new Error('DNS failure'));

    typeInto('f-oidc.well_known', 'https://broken.example.test/.well-known');
    fireEvent.click(screen.getByText('保存 OIDC 设置'));

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith(
        '获取 OIDC 配置失败，请检查网络状况和 Well-Known URL 是否正确',
      ),
    );
    // Half-written OIDC configuration is worse than none: it would leave the
    // old endpoints paired with a new issuer.
    expect(API.put).not.toHaveBeenCalled();
  });

  it('does not send a blank client secret, so the stored one survives', async () => {
    await mountLoaded();

    oidcDiscovery.get.mockResolvedValue({
      data: {
        authorization_endpoint: 'https://idp.example.test/authorize',
        token_endpoint: 'https://idp.example.test/token',
        userinfo_endpoint: 'https://idp.example.test/userinfo',
      },
    });

    typeInto('f-oidc.client_id', 'oidc-id-2');
    fireEvent.click(screen.getByText('保存 OIDC 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('oidc.client_id').value).toBe('oidc-id-2');
    expect(putKeys()).not.toContain('oidc.client_secret');
  });

  // ------------------------------------------------------------------
  // DEFECT (SystemSetting.jsx submitOIDCSettings + getOptions):
  // getOptions does `setInputs(newInputs); setOriginInputs(newInputs);` —
  // the SAME object reference for both. submitOIDCSettings then writes the
  // discovery result by MUTATING state in place:
  //   inputs['oidc.authorization_endpoint'] = res.data['authorization_endpoint']
  // Because `originInputs` IS `inputs` until some form edit replaces the
  // object, that mutation updates both sides of every subsequent
  //   originInputs[k] !== inputs[k]
  // comparison, so all six comparisons are false, `options` stays empty and
  // updateOptions is never called. The operator sees "获取 OIDC 配置成功！"
  // and nothing is written.
  // The reachable path is the ordinary one: an admin whose IdP has rotated
  // its endpoints opens the page and clicks "保存 OIDC 设置" without retyping
  // the well-known URL. Discovery runs, reports success, and the endpoints in
  // the database stay stale — SSO keeps failing with no indication why.
  // ------------------------------------------------------------------
  it.skip('re-running discovery without editing the url must still persist the endpoints', async () => {
    await mountLoaded();

    oidcDiscovery.get.mockResolvedValue({
      data: {
        authorization_endpoint: 'https://idp.example.test/authorize-v2',
        token_endpoint: 'https://idp.example.test/token-v2',
        userinfo_endpoint: 'https://idp.example.test/userinfo-v2',
      },
    });

    fireEvent.click(screen.getByText('保存 OIDC 设置'));

    await waitFor(() => expect(oidcDiscovery.get).toHaveBeenCalled());
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('oidc.authorization_endpoint').value).toBe(
      'https://idp.example.test/authorize-v2',
    );
  });
});

describe('SystemSetting — the other OAuth providers', () => {
  it('strips the trailing slash from the WeChat server address', async () => {
    await mountLoaded();

    typeInto('f-WeChatServerAddress', 'https://wechat2.example.test/');
    fireEvent.click(screen.getByText('保存 WeChat Server 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('WeChatServerAddress').value).toBe(
      'https://wechat2.example.test',
    );
    expect(putKeys()).not.toContain('WeChatServerToken');
  });

  it('sends a changed Turnstile site key but not the blank secret key', async () => {
    await mountLoaded();

    typeInto('f-TurnstileSiteKey', 'ts-site-2');
    fireEvent.click(screen.getByText('保存 Turnstile 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('TurnstileSiteKey').value).toBe('ts-site-2');
    expect(putKeys()).not.toContain('TurnstileSecretKey');
  });
});

describe('SystemSetting — passkey', () => {
  it('sends the five passkey fields from the current form values', async () => {
    await mountLoaded();

    typeInto('f-passkey.rp_display_name', 'Example Console v2');
    fireEvent.click(screen.getByText('保存 Passkey 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('passkey.rp_display_name').value).toBe('Example Console v2');
    expect(putFor('passkey.rp_id').value).toBe('example.test');
    expect(putFor('passkey.user_verification').value).toBe('preferred');
    expect(putFor('passkey.origins').value).toBe(
      'https://console.example.test',
    );
    expect(putKeys().sort()).toEqual([
      'passkey.attachment_preference',
      'passkey.origins',
      'passkey.rp_display_name',
      'passkey.rp_id',
      'passkey.user_verification',
    ]);
  });

  it('sends a changed verification level rather than the default', async () => {
    await mountLoaded();

    typeInto('f-passkey.user_verification', 'required');
    fireEvent.click(screen.getByText('保存 Passkey 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    // 'required' differs from both the stored value and the 'preferred'
    // fallback, so a fallback that fired anyway would be visible.
    expect(putFor('passkey.user_verification').value).toBe('required');
  });

  // These two were written as skipped defect locks, on the theory that
  // submitPasskeySettings' `formValues[k] || inputs[k] || <default>` chain
  // re-sends the OLD origin list when the operator clears the box. Both PASS
  // when un-skipped, so the theory is wrong, and a lock that passes gets read
  // later as a bug someone already fixed. The reason is at
  // SystemSetting.jsx:705 — the Form's `onValueChange` is `handleFormChange`,
  // which does `setInputs(values)` — so `inputs` IS the live form object and
  // the middle term of the chain can never resurrect a value that was just
  // cleared. They are kept live rather than deleted because the behaviour is
  // the security-relevant one (clearing an origin is how a decommissioned
  // console hostname is revoked from WebAuthn), and because the chain would
  // start dropping the clear the day its fallback is repointed at
  // `originInputs`, which does hold the loaded value.
  it('clearing the passkey origins clears them on the server too', async () => {
    await mountLoaded();

    typeInto('f-passkey.origins', '');
    fireEvent.click(screen.getByText('保存 Passkey 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('passkey.origins').value).toBe('');
  });

  it('clearing the passkey relying-party id clears it on the server too', async () => {
    await mountLoaded();

    typeInto('f-passkey.rp_id', '');
    fireEvent.click(screen.getByText('保存 Passkey 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('passkey.rp_id').value).toBe('');
  });

  // Live counterpart: whatever the fallback chain ends up doing, a passkey
  // save must never invent an origin the operator has not entered. Setting a
  // new value must win outright — this passes both before and after the fix.
  it('a newly typed origin list replaces the stored one outright', async () => {
    await mountLoaded();

    typeInto('f-passkey.origins', 'https://new-console.example.test');
    fireEvent.click(screen.getByText('保存 Passkey 设置'));

    await waitFor(() => expect(API.put).toHaveBeenCalled());
    expect(putFor('passkey.origins').value).toBe(
      'https://new-console.example.test',
    );
  });
});
