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

// cycle-13 L7. q6_EditUserModal.test.jsx (pre-existing, not owned by this
// lane) already covers this component's money arithmetic and load/save
// lifecycle in depth, including "blanks the password" on load — this file
// is narrower and exists for exactly the two things this lane's change
// touches: no control in the rendered form can set a password, and the
// outgoing PUT payload never carries the key regardless of what is in
// form state.

import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';

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

const H = vi.hoisted(() => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  fields: new Set(),
  submit: { current: null },
  formValues: { current: {} },
}));

vi.mock('../../../../helpers', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    API: H.api,
    showError: H.showError,
    showSuccess: H.showSuccess,
  };
});

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

// A minimal Semi UI stand-in — only the pieces this test exercises need to
// be faithful: Form must record which `field`s were actually rendered (so
// "no password field" is a real assertion about the JSX, not a guess), and
// submitForm must hand submit() exactly the form's current values,
// mirroring q6's Form stub.
vi.mock('@douyinfe/semi-ui', () => {
  const passthrough = (testid) => (props) =>
    React.createElement('div', { 'data-testid': testid }, props.children);

  const Form = ({ children, onSubmit, getFormApi, initValues }) => {
    const [values, setValues] = React.useState(() => ({ ...initValues }));
    // Re-pointed every render, and read through H inside submitForm below:
    // the memoised `api` therefore stays stable without closing over the
    // current render's `onSubmit` (which is what a [] dependency list would
    // have silently staled, and what react-hooks/exhaustive-deps flags).
    H.submit.current = onSubmit;
    const initialValues = React.useRef(initValues);
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
        submitForm: () => H.submit.current(H.formValues.current),
      }),
      [],
    );
    // Two effects rather than one mount-only effect with a suppressed
    // dependency warning: seeding form state must happen once (the real
    // component's initValues prop is a fresh object every render, so a
    // dependency on it would clobber loaded values), while handing the
    // parent its form api is idempotent and can re-run freely.
    React.useEffect(() => {
      H.formValues.current = { ...initialValues.current };
    }, []);
    React.useEffect(() => {
      getFormApi?.(api);
    }, [api, getFormApi]);
    return React.createElement(
      'form',
      { 'data-testid': 'form' },
      typeof children === 'function' ? children({ values }) : children,
    );
  };

  const field = (kind) => (props) => {
    H.fields.add(props.field);
    return React.createElement('div', {
      'data-testid': `field-${props.field}`,
      'data-kind': kind,
      'data-label': props.label,
    });
  };
  Form.Input = field('input');
  Form.InputNumber = field('number');
  Form.Select = field('select');
  Form.Slot = ({ label, children }) =>
    React.createElement('div', { 'data-testid': `slot-${label}` }, children);

  const Button = ({ children, onClick, icon, loading, ...rest }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, 'data-loading': String(!!loading), ...rest },
      [icon, children],
    );

  const Modal = ({ visible, children }) =>
    visible
      ? React.createElement('div', { 'data-testid': 'quota-modal' }, children)
      : null;

  const SideSheet = ({ visible, title, footer, children }) =>
    React.createElement(
      'div',
      { 'data-testid': 'sheet', 'data-visible': String(visible) },
      React.createElement('div', { 'data-testid': 'sheet-title' }, title),
      React.createElement('div', { 'data-testid': 'sheet-body' }, children),
      React.createElement('div', { 'data-testid': 'sheet-footer' }, footer),
    );

  const InputNumber = ({ value, onChange, placeholder }) =>
    React.createElement('input', {
      placeholder,
      onChange: (e) => onChange(e.target.value),
      value: value === undefined || value === null ? '' : String(value),
    });

  const Tag = ({ children }) =>
    React.createElement('span', { 'data-testid': 'tag' }, children);
  const Text = ({ children, ...rest }) =>
    React.createElement('span', rest, children);

  return {
    Form,
    Button,
    Modal,
    SideSheet,
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

const USER = {
  id: 91,
  username: 'dana.ops',
  display_name: 'Dana',
  email: 'dana@example.com',
  quota: 500000,
  daily_quota: 0,
  group: 'default',
  remark: '',
  github_id: '',
  // A real backend response never carries this (entity.User has had no
  // Password field for a while), but the fixture includes it anyway — the
  // same defence q6's fixture exercises — so this test proves the omission
  // from the outgoing payload holds even if some future/legacy response
  // shape resurrects the key.
  password: 'leaked-hash-should-never-post',
};

const okUser = () => ({
  data: { success: true, message: '', data: { ...USER } },
});

const renderModal = async () => {
  const props = {
    editingUser: { id: 91 },
    visible: true,
    handleClose: vi.fn(),
    refresh: vi.fn(),
  };
  await act(async () => {
    render(React.createElement(EditUserModal, props));
  });
  return props;
};

beforeEach(() => {
  vi.clearAllMocks();
  H.fields.clear();
  H.formValues.current = {};
  localStorage.setItem('quota_per_unit', '500000');
  H.api.get.mockImplementation((url) => {
    if (url.includes('/api/group/')) {
      return Promise.resolve({ data: { success: true, data: ['default'] } });
    }
    return Promise.resolve(okUser());
  });
  H.api.put.mockResolvedValue({ data: { success: true, message: '' } });
});

afterEach(() => {
  localStorage.clear();
});

describe('EditUserModal — no password control (cycle-13 L7)', () => {
  it('does not render a password form field', async () => {
    await renderModal();
    expect(screen.queryByTestId('field-password')).toBeNull();
    // The set the mocked Form.* field() helper populates is a direct read
    // of which `field=` props the real JSX declared — not a guess from the
    // DOM — so this is the same assertion from the other direction.
    expect(H.fields.has('password')).toBe(false);
  });

  it('still renders the account fields that remain (username, display name, group, quota)', async () => {
    await renderModal();
    expect(screen.getByTestId('field-username')).toBeTruthy();
    expect(screen.getByTestId('field-display_name')).toBeTruthy();
    expect(screen.getByTestId('field-group')).toBeTruthy();
    expect(screen.getByTestId('field-quota')).toBeTruthy();
  });

  it('the outgoing PUT payload never carries a password key', async () => {
    await renderModal();
    await act(async () => {
      H.submit.current({ ...H.formValues.current });
    });
    expect(H.api.put).toHaveBeenCalledTimes(1);
    const payload = H.api.put.mock.calls[0][1];
    expect('password' in payload).toBe(false);
  });

  // Belt-and-suspenders: even if form state somehow carried a non-blank
  // password (a stale value, a future regression reintroducing the field),
  // submit() must still strip it before the request leaves the browser.
  it('strips a non-blank password from form state before it reaches the request', async () => {
    await renderModal();
    await act(async () => {
      H.submit.current({
        ...H.formValues.current,
        password: 'typed-into-a-field-that-should-not-exist',
      });
    });
    const payload = H.api.put.mock.calls[0][1];
    expect('password' in payload).toBe(false);
  });

  it('blanks a fetched password before it ever reaches form state', async () => {
    await renderModal();
    expect(H.formValues.current.password).toBe('');
  });
});
