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

// The user editor is where an operator hands out credit. The "添加额度" dialog
// adds a delta to a live balance and shows a before/after preview in money;
// the submit writes the balance back. Both are asserted on ARITHMETIC, not on
// "a call happened".
//
// renderQuota / renderQuotaWithPrompt are deliberately NOT stubbed — the money
// formatting is part of what is under test — so localStorage is seeded with a
// known quota_per_unit and the expected strings are computed from it. Only
// API and the two toast helpers are replaced in the helpers barrel.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, within } from '@testing-library/react';

vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

const H = vi.hoisted(() => {
  // The delta input's onChange, captured from the stub so a test can type into
  // the dialog exactly as Semi's InputNumber would.
  const deltaOnChange = { current: null };
  return {
    api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    fields: new Map(),
    submit: { current: null },
    formValues: { current: {} },
    formApi: { current: null },
    deltaOnChange,
    setDelta: (v) => deltaOnChange.current(v),
  };
});

vi.mock('../../../../helpers', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    API: H.api,
    showError: H.showError,
    showSuccess: H.showSuccess,
  };
});

// Only useTranslation is replaced; the rest of react-i18next is left real
// because the helpers barrel wires i18next up at import time.
vi.mock('react-i18next', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    useTranslation: () => ({ t: (k) => k, i18n: { language: 'zh' } }),
  };
});

vi.mock('../../../../hooks/common/useIsMobile', async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, useIsMobile: () => false };
});

vi.mock('@douyinfe/semi-ui', () => {
  const passthrough =
    (testid, extra = () => ({})) =>
    (props) =>
      React.createElement(
        'div',
        { 'data-testid': testid, ...extra(props) },
        props.children,
      );

  // A real form engine is not needed, but the api surface EditUserModal drives
  // (setValues / setValue / getValue / submitForm) is, and setValue must
  // actually re-render so the money preview recomputes.
  const Form = ({ children, onSubmit, getFormApi, initValues }) => {
    const [values, setValues] = React.useState(() => ({ ...initValues }));
    H.submit.current = onSubmit;
    const api = React.useMemo(
      () => ({
        setValues: (v) => {
          H.formValues.current = { ...v };
          setValues({ ...v });
        },
        setValue: (k, v) => {
          H.formValues.current = { ...H.formValues.current, [k]: v };
          setValues((prev) => ({ ...prev, [k]: v }));
        },
        getValue: (k) => H.formValues.current[k],
        getValues: () => H.formValues.current,
        submitForm: () => onSubmit(H.formValues.current),
      }),
      [],
    );
    React.useEffect(() => {
      H.formValues.current = { ...initValues };
      H.formApi.current = api;
      getFormApi?.(api);
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);
    return React.createElement(
      'form',
      { 'data-testid': 'form' },
      typeof children === 'function' ? children({ values }) : children,
    );
  };

  // Fields register themselves so labels / rules / extraText are inspectable.
  // extraText is RENDERED, not just recorded: it is where the money preview
  // for the quota fields lives.
  const field = (kind) => (props) => {
    H.fields.set(props.field, { kind, ...props });
    return React.createElement(
      'div',
      {
        'data-testid': `field-${props.field}`,
        'data-kind': kind,
        'data-label': props.label,
        'data-readonly': String(Boolean(props.readonly)),
        'data-required': String(
          Array.isArray(props.rules)
            ? props.rules.some((r) => r.required)
            : false,
        ),
        'data-min': props.min === undefined ? '' : String(props.min),
        'data-step': props.step === undefined ? '' : String(props.step),
        'data-options': (props.optionList || []).map((o) => o.value).join(','),
      },
      React.createElement(
        'span',
        { 'data-testid': `extra-${props.field}` },
        props.extraText,
      ),
    );
  };
  Form.Input = field('input');
  Form.InputNumber = field('number');
  Form.Select = field('select');
  Form.Slot = ({ label, children }) =>
    React.createElement('div', { 'data-testid': `slot-${label}` }, children);

  const Button = ({ children, onClick, icon, loading, ...rest }) =>
    React.createElement(
      'button',
      {
        type: 'button',
        onClick,
        'data-loading': loading ? 'true' : 'false',
        ...rest,
      },
      icon,
      children,
    );

  const Modal = ({ visible, onOk, onCancel, title, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'quota-modal', 'data-visible': String(visible) },
      React.createElement('div', { 'data-testid': 'quota-modal-title' }, title),
      React.createElement(
        'div',
        { 'data-testid': 'quota-modal-body' },
        children,
      ),
      React.createElement(
        'button',
        { type: 'button', 'data-testid': 'quota-ok', onClick: onOk },
        'ok',
      ),
      React.createElement(
        'button',
        { type: 'button', 'data-testid': 'quota-cancel', onClick: onCancel },
        'cancel',
      ),
    );

  const SideSheet = ({ visible, title, footer, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'sheet', 'data-visible': String(visible) },
      React.createElement('div', { 'data-testid': 'sheet-title' }, title),
      React.createElement('div', { 'data-testid': 'sheet-body' }, children),
      React.createElement('div', { 'data-testid': 'sheet-footer' }, footer),
    );

  const Popconfirm = ({ title, content, onConfirm, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'popconfirm', 'data-title': title },
      React.createElement(
        'span',
        { 'data-testid': 'popconfirm-content' },
        content,
      ),
      React.createElement(
        'button',
        {
          type: 'button',
          'data-testid': 'popconfirm-confirm',
          onClick: onConfirm,
        },
        'confirm',
      ),
      children,
    );

  const InputNumber = ({ value, onChange, placeholder }) => {
    H.deltaOnChange.current = onChange;
    return React.createElement('input', {
      'data-testid': 'add-quota-input',
      'data-value': value === undefined || value === null ? '' : String(value),
      placeholder,
      onChange: (e) => onChange(e.target.value),
      readOnly: true,
      value: value === undefined || value === null ? '' : String(value),
    });
  };

  const Tag = ({ children, color, ...rest }) =>
    React.createElement(
      'span',
      { 'data-testid': 'tag', 'data-color': color, ...rest },
      children,
    );

  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);

  return {
    Form,
    Button,
    Modal,
    SideSheet,
    Popconfirm,
    InputNumber,
    Input: InputNumber,
    Tag,
    Space: passthrough('space'),
    Spin: (p) =>
      React.createElement(
        'div',
        { 'data-testid': 'spin', 'data-spinning': String(p.spinning) },
        p.children,
      ),
    Card: passthrough('card'),
    Row: passthrough('row'),
    Col: passthrough('col'),
    Avatar: passthrough('avatar'),
    Progress: passthrough('progress'),
    Descriptions: passthrough('descriptions'),
    Typography: { Text, Title: Text, Paragraph: Text },
  };
});

vi.mock('@douyinfe/semi-icons', () => {
  const icon = (name) => () =>
    React.createElement('i', { 'data-testid': `icon-${name}` });
  return {
    IconUser: icon('user'),
    IconSave: icon('save'),
    IconClose: icon('close'),
    IconLink: icon('link'),
    IconUserGroup: icon('group'),
    IconPlus: icon('plus'),
    IconRefresh: icon('refresh'),
    IconClock: icon('clock'),
  };
});

import EditUserModal from './EditUserModal';
import { renderQuota } from '../../../../helpers';

const QUOTA_PER_UNIT = 500000;

// Fixture id 88 is not an index and not 0/1; a handler reaching for the wrong
// value cannot coincide with the right one.
const USER = {
  id: 88,
  username: 'carol.ops',
  display_name: 'Carol',
  email: 'carol@example.com',
  quota: 1500000, // $3.00
  daily_quota: 0,
  group: 'vip',
  remark: 'ops team',
  github_id: 'gh-carol',
  password: 'should-never-survive',
};

const okUser = (over = {}) => ({
  data: { success: true, message: '', data: { ...USER, ...over } },
});

const renderModal = async (over = {}) => {
  const props = {
    editingUser: { id: 88 },
    visible: true,
    handleClose: vi.fn(),
    refresh: vi.fn(),
    ...over,
  };
  await act(async () => {
    render(React.createElement(EditUserModal, props));
  });
  return props;
};

// The plus icon appears twice (slot button and dialog title), so the button is
// found through the labelled form slot it lives in.
const openAddQuota = async () => {
  await act(async () => {
    within(screen.getByTestId('slot-添加额度')).getByRole('button').click();
  });
};

beforeEach(() => {
  vi.clearAllMocks();
  H.fields.clear();
  H.formValues.current = {};
  H.formApi.current = null;
  localStorage.setItem('quota_per_unit', String(QUOTA_PER_UNIT));
  localStorage.setItem('quota_display_type', 'USD');
  H.api.get.mockImplementation((url) => {
    if (url.includes('/api/group/'))
      return Promise.resolve({
        data: { success: true, data: ['default', 'vip'] },
      });
    if (url.includes('daily-quota'))
      return Promise.resolve({ data: { success: true, data: null } });
    return Promise.resolve(okUser());
  });
  H.api.put.mockResolvedValue({ data: { success: true, message: '' } });
  H.api.post.mockResolvedValue({ data: { success: true, message: '' } });
});

afterEach(() => {
  localStorage.clear();
});

describe('loading an existing account', () => {
  it('fetches that account by id, not the signed-in operator', async () => {
    await renderModal();
    const urls = H.api.get.mock.calls.map((c) => c[0]);
    expect(urls).toContain('/api/user/88');
    // /api/user/self would silently open the ADMIN's own profile under the
    // heading "edit user".
    expect(urls).not.toContain('/api/user/self');
  });

  it('also loads the group list and the daily-quota status for that account', async () => {
    await renderModal();
    const urls = H.api.get.mock.calls.map((c) => c[0]);
    expect(urls).toContain('/api/group/');
    expect(urls).toContain('/api/user/88/daily-quota');
  });

  it('seeds the form from the response and blanks the password', async () => {
    await renderModal();
    expect(H.formValues.current.username).toBe('carol.ops');
    expect(H.formValues.current.quota).toBe(1500000);
    expect(H.formValues.current.group).toBe('vip');
    // The fetched record must never carry a password into an editable field.
    expect(H.formValues.current.password).toBe('');
  });

  it('fills gaps with defaults rather than leaving fields undefined', async () => {
    H.api.get.mockImplementation((url) => {
      if (url.includes('/api/group/'))
        return Promise.resolve({ data: { success: true, data: [] } });
      if (url.includes('daily-quota'))
        return Promise.resolve({ data: { success: true, data: null } });
      return Promise.resolve({
        data: { success: true, data: { id: 88, username: 'carol.ops' } },
      });
    });
    await renderModal();
    expect(H.formValues.current.daily_quota).toBe(0);
    expect(H.formValues.current.fallback_group).toBe('');
  });

  it('reports a failed load and does not populate the form with junk', async () => {
    H.api.get.mockImplementation((url) => {
      if (url.includes('/api/group/'))
        return Promise.resolve({ data: { success: true, data: [] } });
      if (url.includes('daily-quota'))
        return Promise.resolve({ data: { success: true, data: null } });
      return Promise.resolve({
        data: { success: false, message: '用户不存在', data: null },
      });
    });
    await renderModal();
    expect(H.showError).toHaveBeenCalledWith('用户不存在');
    expect(H.formValues.current.username).toBe('');
  });

  it('stops spinning once the account has loaded', async () => {
    await renderModal();
    expect(screen.getByTestId('spin')).toHaveAttribute(
      'data-spinning',
      'false',
    );
  });

  it('offers the fetched groups as options on all three group selectors', async () => {
    await renderModal();
    ['group', 'base_group', 'fallback_group'].forEach((f) =>
      expect(screen.getByTestId(`field-${f}`)).toHaveAttribute(
        'data-options',
        'default,vip',
      ),
    );
  });

  it('keeps the third-party binding fields read-only', async () => {
    await renderModal();
    [
      'github_id',
      'discord_id',
      'oidc_id',
      'wechat_id',
      'email',
      'telegram_id',
    ].forEach((f) =>
      expect(screen.getByTestId(`field-${f}`)).toHaveAttribute(
        'data-readonly',
        'true',
      ),
    );
    // Negative half: the editable fields must NOT be read-only, or the
    // assertion above would pass on a form nobody can type into.
    expect(screen.getByTestId('field-username')).toHaveAttribute(
      'data-readonly',
      'false',
    );
  });

  it('requires a username, a group and a quota before saving', async () => {
    await renderModal();
    ['username', 'group', 'quota'].forEach((f) =>
      expect(screen.getByTestId(`field-${f}`)).toHaveAttribute(
        'data-required',
        'true',
      ),
    );
    expect(screen.getByTestId('field-remark')).toHaveAttribute(
      'data-required',
      'false',
    );
  });
});

describe('a save that fails in transport', () => {
  // The rejection is driven through the captured submit handler and swallowed
  // here, so the suite never carries an unhandled rejection of its own.
  const saveAndSwallow = async (props) => {
    let escaped = null;
    await act(async () => {
      try {
        await H.submit.current({ ...H.formValues.current });
      } catch (e) {
        escaped = e;
      }
    });
    return escaped;
  };

  // DEFECT (correctness): neither loadUser nor submit has a try/catch. When
  // the request rejects — offline, 502, dropped session — the error escapes
  // the handler, showError is never called and setLoading(false) is never
  // reached, so the sheet is left spinning with the submit button stuck in its
  // loading state forever. The operator cannot tell "still saving" from
  // "failed", and the edited quota is silently unsaved.
  it('CONTRACT: a save that fails in transport tells the operator', async () => {
    H.api.put.mockRejectedValue(new Error('network down'));
    const props = await renderModal();
    await saveAndSwallow(props);
    expect(H.showError).toHaveBeenCalled();
  });

  it('CONTRACT: a save that fails in transport releases the spinner', async () => {
    H.api.put.mockRejectedValue(new Error('network down'));
    const props = await renderModal();
    await saveAndSwallow(props);
    expect(screen.getByTestId('spin')).toHaveAttribute(
      'data-spinning',
      'false',
    );
  });

  it('a save that never reached the server is never reported as done', async () => {
    // Fix-safe invariant, and the one that actually protects the balance: no
    // success toast, no grid refresh, no sheet close over the unsaved edit.
    // True today (the handler dies before any of them) and true after a fix
    // (the catch branch must not run them either).
    H.api.put.mockRejectedValue(new Error('network down'));
    const props = await renderModal();
    await saveAndSwallow(props);
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    expect(props.handleClose).not.toHaveBeenCalled();
  });
});

describe('saving', () => {
  it('PUTs the edited account with its id coerced to a number', async () => {
    await renderModal();
    await act(async () => {
      H.formApi.current.setValue('quota', 2000000);
    });
    await act(async () => {
      H.submit.current({ ...H.formValues.current });
    });
    expect(H.api.put).toHaveBeenCalledTimes(1);
    const [url, payload] = H.api.put.mock.calls[0];
    expect(url).toBe('/api/user/');
    expect(payload.id).toBe(88);
    expect(payload.quota).toBe(2000000);
  });

  it('coerces a string quota to an integer instead of shipping the string', async () => {
    await renderModal();
    await act(async () => {
      H.submit.current({
        ...H.formValues.current,
        quota: '750000',
        daily_quota: '250000',
      });
    });
    const payload = H.api.put.mock.calls[0][1];
    expect(payload.quota).toBe(750000);
    expect(payload.daily_quota).toBe(250000);
  });

  it('turns an unparseable quota string into 0 rather than NaN', async () => {
    await renderModal();
    await act(async () => {
      H.submit.current({ ...H.formValues.current, quota: 'abc' });
    });
    const payload = H.api.put.mock.calls[0][1];
    // A NaN here would serialise as null and could blank a live balance.
    expect(Number.isNaN(payload.quota)).toBe(false);
    expect(payload.quota).toBe(0);
  });

  it('reports success, refreshes the grid and closes only on a successful save', async () => {
    const props = await renderModal();
    await act(async () => {
      H.submit.current({ ...H.formValues.current });
    });
    expect(H.showSuccess).toHaveBeenCalled();
    expect(props.refresh).toHaveBeenCalledTimes(1);
    expect(props.handleClose).toHaveBeenCalledTimes(1);
  });

  it('a refused save does not report success, refresh or close over the input', async () => {
    H.api.put.mockResolvedValue({
      data: { success: false, message: '额度不能为负' },
    });
    const props = await renderModal();
    await act(async () => {
      H.submit.current({ ...H.formValues.current });
    });
    expect(H.showError).toHaveBeenCalledWith('额度不能为负');
    expect(H.showSuccess).not.toHaveBeenCalled();
    expect(props.refresh).not.toHaveBeenCalled();
    expect(props.handleClose).not.toHaveBeenCalled();
  });

  it('closes without writing anything when cancel is pressed', async () => {
    const props = await renderModal();
    await act(async () => {
      screen.getByTestId('icon-close').closest('button').click();
    });
    expect(props.handleClose).toHaveBeenCalledTimes(1);
    expect(H.api.put).not.toHaveBeenCalled();
  });
});

describe('the add-quota dialog — money arithmetic', () => {
  it('stays closed until the plus button is pressed', async () => {
    await renderModal();
    expect(screen.getByTestId('quota-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
    await openAddQuota();
    expect(screen.getByTestId('quota-modal')).toHaveAttribute(
      'data-visible',
      'true',
    );
  });

  it('previews the balance as current + delta, in money', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    const body = screen.getByTestId('quota-modal-body').textContent;
    // All three figures, computed from the fixture rather than hard-coded, so
    // a change to quota_per_unit cannot make this pass vacuously.
    expect(body).toContain(renderQuota(1500000)); // $3.00 current
    expect(body).toContain(renderQuota(500000)); // $1.00 delta
    expect(body).toContain(renderQuota(2000000)); // $4.00 result
    expect(renderQuota(2000000)).toBe('$4.00');
  });

  it('adds the typed delta to the balance and closes the dialog', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    expect(H.formValues.current.quota).toBe(2000000);
    expect(screen.getByTestId('quota-modal')).toHaveAttribute(
      'data-visible',
      'false',
    );
  });

  it('supports a negative delta — the dialog says it does', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('-500000');
    });
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    expect(H.formValues.current.quota).toBe(1000000);
  });

  it('leaves the balance untouched when the dialog is cancelled', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    await act(async () => {
      screen.getByTestId('quota-cancel').click();
    });
    expect(H.formValues.current.quota).toBe(1500000);
  });

  it('treats an unparseable delta as zero rather than corrupting the balance', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('abc');
    });
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    // The one thing that must never happen: a live balance becoming NaN.
    expect(Number.isNaN(H.formValues.current.quota)).toBe(false);
    expect(H.formValues.current.quota).toBe(1500000);
  });

  // DEFECT (money): the delta typed into the dialog is kept in component state
  // and is NEVER cleared after being applied. Add $1.00, press OK, reopen the
  // dialog and press OK again — the same $1.00 is granted a second time. The
  // dialog reopens pre-filled with the previous amount, so an operator who
  // opens it to check the balance and dismisses it with OK instead of Cancel
  // silently doubles the credit they just issued.
  it('CONTRACT: an applied delta is cleared so reopening cannot re-grant it', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    await openAddQuota();
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    expect(H.formValues.current.quota).toBe(2000000);
  });

  it('reopens the add-quota dialog with the amount field emptied', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    await openAddQuota();
    expect(screen.getByTestId('add-quota-input')).toHaveAttribute(
      'data-value',
      '',
    );
  });

  // DEFECT (money): the preview computes `parseInt(addQuotaLocal || 0)` while
  // the apply computes `parseInt(addQuotaLocal) || 0`. For any non-numeric
  // entry the preview renders the result as "$NaN" while OK actually adds 0.
  // The two must agree: whatever the preview promises is what OK must do.
  it('CONTRACT: the preview shows exactly the balance OK will apply', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('abc');
    });
    const previewed = screen.getByTestId('quota-modal-body').textContent;
    expect(previewed).not.toContain('NaN');
  });

  it('previews a non-numeric delta as the nothing it will actually add', async () => {
    await renderModal();
    await openAddQuota();
    await act(async () => {
      H.setDelta('abc');
    });
    // The whole figure, not just "no NaN": $3.00 + $0.00 = $3.00.
    expect(screen.getByTestId('quota-modal-body').textContent).toBe(
      `新额度：${renderQuota(1500000)} + ${renderQuota(0)} = ${renderQuota(1500000)}`,
    );
    expect(renderQuota(1500000)).toBe('$3.00');
    expect(renderQuota(0)).toBe('$0.00');
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    // ...and OK moves the balance by exactly what was previewed.
    expect(H.formValues.current.quota).toBe(1500000);
  });

  it('previews a balance held as a string the same way it applies it', async () => {
    // The submit path guards `typeof quota === 'string'`, so the form really
    // can hold one. Unparsed, '1500000' + 500000 concatenates: the preview
    // would promise $3000001.00 while OK adds the $1.00 that was typed.
    await renderModal();
    await act(async () => {
      H.formApi.current.setValue('quota', '1500000');
    });
    await openAddQuota();
    await act(async () => {
      H.setDelta('500000');
    });
    expect(screen.getByTestId('quota-modal-body').textContent).toBe(
      `新额度：${renderQuota(1500000)} + ${renderQuota(500000)} = ${renderQuota(2000000)}`,
    );
    expect(renderQuota(2000000)).toBe('$4.00');
    await act(async () => {
      screen.getByTestId('quota-ok').click();
    });
    // ...and OK moves the balance by exactly what was previewed: $4.00.
    expect(H.formValues.current.quota).toBe(2000000);
  });

  // DEFECT (money): -1 is the platform's "unlimited" sentinel for a quota. Fed
  // to renderQuota it divides by quota_per_unit and formats the result, so the
  // preview greets the operator with "$-0.00" — a balance that reads as a tiny
  // negative debt rather than "unlimited". The same helper defect is recorded
  // on the users grid; here it lands on the dialog that grants credit.
  it('CONTRACT: an unlimited balance is never previewed as a negative amount', async () => {
    await renderModal();
    await act(async () => {
      H.formApi.current.setValue('quota', -1);
    });
    await openAddQuota();
    expect(screen.getByTestId('quota-modal-body').textContent).not.toContain(
      '-0.00',
    );
  });

  it('never previews a balance it could not compute as a concrete amount', async () => {
    // Fix-safe invariant: whatever an unlimited balance is rendered as, the
    // preview must not contain the literal "NaN". Holds today and after a fix.
    await renderModal();
    await act(async () => {
      H.formApi.current.setValue('quota', -1);
    });
    await openAddQuota();
    expect(screen.getByTestId('quota-modal-body').textContent).not.toContain(
      'NaN',
    );
  });
});

describe('the daily quota panel', () => {
  const withDaily = (data) => {
    H.api.get.mockImplementation((url) => {
      if (url.includes('/api/group/'))
        return Promise.resolve({
          data: { success: true, data: ['default', 'vip'] },
        });
      if (url.includes('daily-quota'))
        return Promise.resolve({ data: { success: true, data } });
      return Promise.resolve(okUser());
    });
  };

  it('shows nothing at all when the account has no daily-quota record', async () => {
    await renderModal();
    expect(screen.queryByTestId('popconfirm')).not.toBeInTheDocument();
  });

  it('breaks today’s usage down against the cap, in money', async () => {
    withDaily({
      daily_used: 250000,
      daily_quota: 1000000,
      current_group: 'vip',
      is_using_fallback: false,
    });
    await renderModal();
    const body = screen.getByTestId('sheet-body').textContent;
    expect(body).toContain(renderQuota(250000)); // $0.50 used
    expect(body).toContain(renderQuota(1000000)); // $2.00 cap
  });

  it('says 无限制 instead of printing a zero cap as money', async () => {
    withDaily({
      daily_used: 250000,
      daily_quota: 0,
      current_group: 'vip',
      is_using_fallback: false,
    });
    await renderModal();
    const body = screen.getByTestId('sheet-body').textContent;
    expect(body).toContain('无限制');
    expect(body).toContain(renderQuota(250000));
  });

  it('flags an account that has dropped to its fallback group', async () => {
    withDaily({
      daily_used: 1000000,
      daily_quota: 1000000,
      current_group: 'free',
      is_using_fallback: true,
    });
    await renderModal();
    expect(screen.getByTestId('sheet-body').textContent).toContain('降级中');
  });

  it('does NOT flag an account still on its normal group', async () => {
    withDaily({
      daily_used: 100,
      daily_quota: 1000000,
      current_group: 'vip',
      is_using_fallback: false,
    });
    await renderModal();
    expect(screen.getByTestId('sheet-body').textContent).not.toContain(
      '降级中',
    );
  });

  it('gates the daily-quota reset behind a confirmation', async () => {
    withDaily({
      daily_used: 100,
      daily_quota: 1000000,
      current_group: 'vip',
      is_using_fallback: false,
    });
    await renderModal();
    expect(screen.getByTestId('popconfirm')).toHaveAttribute(
      'data-title',
      '确认重置',
    );
    expect(H.api.post).not.toHaveBeenCalled();
  });

  it('resets the daily quota for THAT account when confirmed', async () => {
    withDaily({
      daily_used: 100,
      daily_quota: 1000000,
      current_group: 'vip',
      is_using_fallback: false,
    });
    await renderModal();
    await act(async () => {
      screen.getByTestId('popconfirm-confirm').click();
    });
    expect(H.api.post).toHaveBeenCalledWith('/api/user/88/daily-quota/reset');
    expect(H.showSuccess).toHaveBeenCalledWith('每日额度已重置');
  });

  it('reports a refused reset instead of claiming it worked', async () => {
    withDaily({
      daily_used: 100,
      daily_quota: 1000000,
      current_group: 'vip',
      is_using_fallback: false,
    });
    H.api.post.mockResolvedValue({
      data: { success: false, message: '重置失败' },
    });
    await renderModal();
    await act(async () => {
      screen.getByTestId('popconfirm-confirm').click();
    });
    expect(H.showError).toHaveBeenCalledWith('重置失败');
    expect(H.showSuccess).not.toHaveBeenCalled();
  });
});

describe('sheet chrome', () => {
  it('announces itself as an edit for an existing account', async () => {
    await renderModal();
    expect(screen.getByTestId('sheet-title').textContent).toContain('编辑用户');
  });

  it('shows the create heading when there is no account id', async () => {
    H.api.get.mockImplementation((url) => {
      if (url.includes('/api/group/'))
        return Promise.resolve({ data: { success: true, data: [] } });
      return Promise.resolve(okUser());
    });
    await renderModal({ editingUser: {} });
    expect(screen.getByTestId('sheet-title').textContent).toContain('创建用户');
  });

  it('hides the quota and daily-quota cards when there is no account id', async () => {
    H.api.get.mockImplementation((url) => {
      if (url.includes('/api/group/'))
        return Promise.resolve({ data: { success: true, data: [] } });
      return Promise.resolve(okUser());
    });
    await renderModal({ editingUser: {} });
    // Credit controls must not appear on a sheet that has no account to
    // credit — otherwise the delta would silently land on /api/user/self.
    expect(screen.queryByTestId('field-quota')).not.toBeInTheDocument();
    expect(screen.queryByTestId('field-daily_quota')).not.toBeInTheDocument();
  });
});
