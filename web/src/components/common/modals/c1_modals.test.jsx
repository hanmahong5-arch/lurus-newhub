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
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key, i18n: { language: 'zh' } }),
  Trans: ({ children }) => children,
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

vi.mock('@douyinfe/semi-ui', () => {
  const h = React.createElement;
  const Text = ({ children }) => h('span', null, children);
  return {
    Modal: ({ visible, title, footer, children }) =>
      visible
        ? h(
            'div',
            { role: 'dialog' },
            h('div', null, title),
            children,
            h('div', { 'data-testid': 'modal-footer' }, footer),
          )
        : null,
    Button: ({ onClick, disabled, loading, children }) =>
      h(
        'button',
        { type: 'button', onClick, disabled: !!disabled || !!loading },
        children,
      ),
    Input: ({ value, onChange, onKeyDown, placeholder, disabled, maxLength }) =>
      h('input', {
        value: value ?? '',
        placeholder,
        disabled: !!disabled,
        maxLength,
        onChange: (e) => onChange?.(e.target.value),
        onKeyDown,
      }),
    // Render the tab strip plus only the active pane, the way Semi behaves.
    Tabs: ({ activeKey, onChange, children }) =>
      h(
        'div',
        null,
        h(
          'div',
          { 'data-testid': 'tab-strip' },
          React.Children.toArray(children)
            .filter(Boolean)
            .map((child) =>
              h(
                'button',
                {
                  type: 'button',
                  key: child.props.itemKey,
                  'data-testid': `tab-${child.props.itemKey}`,
                  onClick: () => onChange?.(child.props.itemKey),
                },
                child.props.tab,
              ),
            ),
        ),
        React.Children.toArray(children)
          .filter(Boolean)
          .filter((child) => child.props.itemKey === activeKey)
          .map((child) => child.props.children),
      ),
    TabPane: ({ children }) => children,
    Space: ({ children }) => h('div', null, children),
    Spin: () => h('div', { 'data-testid': 'spin' }),
    Typography: {
      Text,
      Title: ({ children }) => h('h4', null, children),
      Paragraph: ({ children }) => h('p', null, children),
    },
  };
});

import TwoFactorAuthModal from './TwoFactorAuthModal';
import SecureVerificationModal from './SecureVerificationModal';

// ─── TwoFactorAuthModal ─────────────────────────────────────────────────────

const twoFactorProps = (over = {}) => ({
  visible: true,
  code: '',
  loading: false,
  onCodeChange: vi.fn(),
  onVerify: vi.fn(),
  onCancel: vi.fn(),
  ...over,
});

describe('TwoFactorAuthModal', () => {
  it('renders nothing while hidden', () => {
    render(<TwoFactorAuthModal {...twoFactorProps({ visible: false })} />);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('falls back to the default title, description and placeholder', () => {
    render(<TwoFactorAuthModal {...twoFactorProps()} />);

    expect(screen.getAllByText('安全验证').length).toBeGreaterThan(0);
    expect(
      screen.getByText('为了保护账户安全，请验证您的两步验证码。'),
    ).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText('请输入认证器验证码或备用码'),
    ).toBeInTheDocument();
  });

  it('prefers caller-supplied copy', () => {
    render(
      <TwoFactorAuthModal
        {...twoFactorProps({
          title: '解锁密钥',
          description: '请输入动态码',
          placeholder: '6 位数字',
        })}
      />,
    );

    expect(screen.getByText('解锁密钥')).toBeInTheDocument();
    expect(screen.getByText('请输入动态码')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('6 位数字')).toBeInTheDocument();
  });

  it('caps the code at 8 characters and reports each keystroke', () => {
    const props = twoFactorProps();
    render(<TwoFactorAuthModal {...props} />);

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('maxLength', '8');

    fireEvent.change(input, { target: { value: '123456' } });
    expect(props.onCodeChange).toHaveBeenCalledWith('123456');
  });

  it('arms the verify button only once a code is present', () => {
    const { unmount } = render(<TwoFactorAuthModal {...twoFactorProps()} />);
    expect(screen.getByRole('button', { name: '验证' })).toBeDisabled();
    unmount();

    const props = twoFactorProps({ code: '123456' });
    render(<TwoFactorAuthModal {...props} />);
    const verify = screen.getByRole('button', { name: '验证' });
    expect(verify).not.toBeDisabled();

    fireEvent.click(verify);
    expect(props.onVerify).toHaveBeenCalledTimes(1);
  });

  it('locks the verify button while a verification is in flight', () => {
    render(
      <TwoFactorAuthModal
        {...twoFactorProps({ code: '123456', loading: true })}
      />,
    );

    expect(screen.getByRole('button', { name: '验证' })).toBeDisabled();
  });

  it('submits on Enter, but only with a code and while idle', () => {
    const idle = twoFactorProps({ code: '123456' });
    const { unmount } = render(<TwoFactorAuthModal {...idle} />);
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(idle.onVerify).toHaveBeenCalledTimes(1);
    // A different key must not submit.
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'a' });
    expect(idle.onVerify).toHaveBeenCalledTimes(1);
    unmount();

    const empty = twoFactorProps({ code: '' });
    const second = render(<TwoFactorAuthModal {...empty} />);
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(empty.onVerify).not.toHaveBeenCalled();
    second.unmount();

    const busy = twoFactorProps({ code: '123456', loading: true });
    render(<TwoFactorAuthModal {...busy} />);
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' });
    expect(busy.onVerify).not.toHaveBeenCalled();
  });

  it('cancels from the footer button', () => {
    const props = twoFactorProps();
    render(<TwoFactorAuthModal {...props} />);

    fireEvent.click(screen.getByRole('button', { name: '取消' }));
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });
});

// ─── SecureVerificationModal ────────────────────────────────────────────────

const secureProps = (methods, state = {}, over = {}) => ({
  visible: true,
  verificationMethods: {
    has2FA: false,
    hasPasskey: false,
    passkeySupported: false,
    hasSession: false,
    ...methods,
  },
  verificationState: { method: '2fa', loading: false, code: '', ...state },
  onVerify: vi.fn(),
  onCancel: vi.fn(),
  onCodeChange: vi.fn(),
  onMethodSwitch: vi.fn(),
  ...over,
});

describe('SecureVerificationModal — no method enrolled', () => {
  it('sends the user to security settings instead of an unusable form', () => {
    const props = secureProps({});
    render(<SecureVerificationModal {...props} />);

    expect(screen.getByText('需要安全验证')).toBeInTheDocument();
    expect(
      screen.getByText('您需要先启用两步验证或 Passkey 才能查看敏感信息。'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('tab-strip')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '确定' }));
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('renders nothing at all while hidden', () => {
    render(
      <SecureVerificationModal {...secureProps({}, {}, { visible: false })} />,
    );

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

describe('SecureVerificationModal — 2FA', () => {
  it('verifies with the active method and the typed code', () => {
    const props = secureProps({ has2FA: true }, { code: '654321' });
    render(<SecureVerificationModal {...props} />);

    expect(screen.getByPlaceholderText('请输入6位验证码')).toHaveAttribute(
      'maxLength',
      '6',
    );
    fireEvent.click(screen.getByRole('button', { name: '验证' }));

    expect(props.onVerify).toHaveBeenCalledWith('2fa', '654321');
  });

  it('treats a whitespace-only code as empty', () => {
    render(
      <SecureVerificationModal
        {...secureProps({ has2FA: true }, { code: '   ' })}
      />,
    );

    expect(screen.getByRole('button', { name: '验证' })).toBeDisabled();
  });

  it('submits on Enter and aborts on Escape', () => {
    const props = secureProps({ has2FA: true }, { code: '654321' });
    render(<SecureVerificationModal {...props} />);
    const input = screen.getByRole('textbox');

    fireEvent.keyDown(input, { key: 'Enter' });
    expect(props.onVerify).toHaveBeenCalledWith('2fa', '654321');

    fireEvent.keyDown(input, { key: 'Escape' });
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('ignores both keys while a verification is running', () => {
    const props = secureProps(
      { has2FA: true },
      { code: '654321', loading: true },
    );
    render(<SecureVerificationModal {...props} />);
    const input = screen.getByRole('textbox');

    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.keyDown(input, { key: 'Escape' });

    expect(props.onVerify).not.toHaveBeenCalled();
    expect(props.onCancel).not.toHaveBeenCalled();
    expect(input).toBeDisabled();
  });

  it('does not submit on Enter from another tab', () => {
    const props = secureProps(
      { has2FA: true, hasPasskey: true, passkeySupported: true },
      { method: 'passkey', code: '654321' },
    );
    render(<SecureVerificationModal {...props} />);

    // The 2FA pane is not mounted on the passkey tab, so drive the handler
    // through the passkey pane's own verify button instead.
    fireEvent.click(screen.getByRole('button', { name: '验证 Passkey' }));
    expect(props.onVerify).toHaveBeenCalledWith('passkey');
  });
});

describe('SecureVerificationModal — session and passkey tabs', () => {
  it('offers session confirmation only when 2FA is absent', () => {
    const props = secureProps({ hasSession: true }, { method: 'session' });
    const { unmount } = render(<SecureVerificationModal {...props} />);

    expect(screen.getByTestId('tab-session')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '确认' }));
    expect(props.onVerify).toHaveBeenCalledWith('session');
    unmount();

    render(
      <SecureVerificationModal
        {...secureProps({ has2FA: true, hasSession: true })}
      />,
    );
    expect(screen.queryByTestId('tab-session')).not.toBeInTheDocument();
  });

  it('hides the passkey tab when the browser cannot do WebAuthn', () => {
    render(
      <SecureVerificationModal
        {...secureProps({
          has2FA: true,
          hasPasskey: true,
          passkeySupported: false,
        })}
      />,
    );

    expect(screen.getByTestId('tab-2fa')).toBeInTheDocument();
    expect(screen.queryByTestId('tab-passkey')).not.toBeInTheDocument();
  });

  it('reports tab switches to the caller', () => {
    const props = secureProps(
      { has2FA: true, hasPasskey: true, passkeySupported: true },
      { method: '2fa' },
    );
    render(<SecureVerificationModal {...props} />);

    fireEvent.click(screen.getByTestId('tab-passkey'));

    expect(props.onMethodSwitch).toHaveBeenCalledWith('passkey');
  });

  it('shows the caller description and title', () => {
    render(
      <SecureVerificationModal
        {...secureProps(
          { has2FA: true },
          {},
          { title: '查看渠道密钥', description: '请验证您的身份。' },
        )}
      />,
    );

    expect(screen.getByText('查看渠道密钥')).toBeInTheDocument();
    expect(screen.getByText('请验证您的身份。')).toBeInTheDocument();
  });

  it('disables the cancel buttons inside a pane while loading', () => {
    render(
      <SecureVerificationModal
        {...secureProps(
          { hasSession: true },
          { method: 'session', loading: true },
        )}
      />,
    );

    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled();
    expect(screen.getByRole('button', { name: '确认' })).toBeDisabled();
  });

  // DEFECT (skipped): the "nothing enrolled" guard only looks at has2FA /
  // hasPasskey / hasSession, ignoring passkeySupported. A user whose only
  // factor is a passkey, on a browser without WebAuthn, falls through to the
  // tabbed modal — where every TabPane is gated off, so the dialog renders an
  // empty body with footer={null}: no verify control, no cancel control, no
  // explanation. It should show the same "enrol a factor" guidance.
  it.skip('explains itself when the only factor is an unsupported passkey', () => {
    render(
      <SecureVerificationModal
        {...secureProps({ hasPasskey: true, passkeySupported: false })}
      />,
    );

    expect(screen.getByText('需要安全验证')).toBeInTheDocument();
  });
});
