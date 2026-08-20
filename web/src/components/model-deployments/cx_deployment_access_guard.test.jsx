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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

const navigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));

// Card surfaces its `loading` flag as an attribute so the two skeleton states
// stay distinguishable from a real render — stubbing it to a bare <div> would
// make "checking connection" and "connected" look identical in the DOM.
vi.mock('@douyinfe/semi-ui', () => ({
  Card: ({ loading, children, ...rest }) =>
    React.createElement(
      'div',
      { 'data-testid': 'card', 'data-loading': String(!!loading), ...rest },
      children,
    ),
  Button: ({ icon, children, onClick, type }) =>
    React.createElement(
      'button',
      { type: 'button', onClick, 'data-btn-type': type },
      icon,
      children,
    ),
  Typography: {
    Title: ({ children }) =>
      React.createElement('h2', { 'data-testid': 'guard-title' }, children),
    Text: ({ children }) => React.createElement('span', null, children),
  },
}));

vi.mock('lucide-react', () => ({
  Settings: () => React.createElement('i', { 'data-testid': 'icon-settings' }),
  Server: () => React.createElement('i', { 'data-testid': 'icon-server' }),
  AlertCircle: () =>
    React.createElement('i', { 'data-testid': 'icon-alert-circle' }),
  WifiOff: () => React.createElement('i', { 'data-testid': 'icon-wifi-off' }),
}));

import DeploymentAccessGuard from './DeploymentAccessGuard';

const CHILD = <div data-testid='protected-child'>deployments table</div>;

const renderGuard = (props = {}) =>
  render(
    <DeploymentAccessGuard
      loading={false}
      isEnabled={true}
      connectionLoading={false}
      connectionOk={true}
      connectionError={null}
      {...props}
    >
      {CHILD}
    </DeploymentAccessGuard>,
  );

const child = () => screen.queryByTestId('protected-child');

beforeEach(() => {
  navigate.mockReset();
});

describe('DeploymentAccessGuard — gate closed', () => {
  it('withholds the children while settings are still loading', () => {
    renderGuard({ loading: true });

    expect(child()).toBeNull();
    expect(screen.getByTestId('card')).toHaveAttribute('data-loading', 'true');
    expect(document.body.textContent).toContain('加载设置中...');
  });

  it('withholds the children when the deployment feature is disabled', () => {
    renderGuard({ isEnabled: false });

    expect(child()).toBeNull();
    expect(screen.getByTestId('guard-title').textContent).toContain(
      '模型部署服务未启用',
    );
    expect(screen.getByTestId('icon-alert-circle')).toBeInTheDocument();
  });

  it('the loading gate wins over the disabled gate', () => {
    // Both closed: the user must see "loading", not a premature "disabled"
    // verdict computed from settings that have not arrived yet.
    renderGuard({ loading: true, isEnabled: false });

    expect(document.body.textContent).toContain('加载设置中...');
    expect(document.body.textContent).not.toContain('模型部署服务未启用');
  });

  it('withholds the children while the connection probe is in flight', () => {
    renderGuard({ connectionLoading: true, connectionOk: null });

    expect(child()).toBeNull();
    expect(document.body.textContent).toContain('正在检查 io.net 连接...');
  });

  it('withholds the children when the probe has not reported yet (ok=null, no error)', () => {
    renderGuard({ connectionLoading: false, connectionOk: null });

    expect(child()).toBeNull();
    expect(document.body.textContent).toContain('正在检查 io.net 连接...');
  });

  // NOTE (gap): `connectionOk === undefined` — i.e. the prop omitted entirely
  // — is neither the "still checking" case (which tests `=== null`) nor the
  // failure case (`=== false`), so the guard falls open and renders children.
  // The only caller (pages/ModelDeployment) always passes the value, so this
  // is unreachable today; asserting either way would pin an undecided design
  // question rather than a contract, so it is left unasserted deliberately.
});

describe('DeploymentAccessGuard — connection failure', () => {
  it('reports a generic connection failure', () => {
    renderGuard({ connectionOk: false, connectionError: { type: 'network' } });

    expect(child()).toBeNull();
    expect(screen.getByTestId('guard-title').textContent).toContain(
      '无法连接 io.net',
    );
    expect(document.body.textContent).toContain('当前配置无法连接到 io.net。');
    expect(screen.getByTestId('icon-wifi-off')).toBeInTheDocument();
  });

  it('distinguishes an expired key from a generic outage', () => {
    renderGuard({ connectionOk: false, connectionError: { type: 'expired' } });

    expect(screen.getByTestId('guard-title').textContent).toContain(
      '接口密钥已过期',
    );
    expect(document.body.textContent).toContain(
      '当前 API 密钥已过期，请在设置中更新。',
    );
    // Negative half of the pair: the generic copy must NOT also be showing,
    // otherwise "always warns" would pass as "warns on expiry".
    expect(document.body.textContent).not.toContain('无法连接 io.net');
  });

  it('surfaces the upstream error detail when one is supplied', () => {
    renderGuard({
      connectionOk: false,
      connectionError: { type: 'network', message: 'dial tcp: timeout' },
    });
    expect(document.body.textContent).toContain('dial tcp: timeout');
  });

  it('renders no detail line when the error carries no message', () => {
    renderGuard({ connectionOk: false, connectionError: { type: 'network' } });
    expect(document.body.textContent).not.toContain('dial tcp');
  });

  it('offers a retry only when the caller supplied a retry handler', () => {
    const onRetry = vi.fn();
    const { unmount } = renderGuard({
      connectionOk: false,
      connectionError: { type: 'network' },
      onRetry,
    });
    fireEvent.click(screen.getByText('重试连接'));
    expect(onRetry).toHaveBeenCalledTimes(1);
    unmount();

    renderGuard({ connectionOk: false, connectionError: { type: 'network' } });
    expect(screen.queryByText('重试连接')).toBeNull();
  });

  it('routes to the model-deployment settings tab from the failure screen', () => {
    renderGuard({ connectionOk: false, connectionError: { type: 'network' } });
    fireEvent.click(screen.getByText('前往设置'));

    expect(navigate).toHaveBeenCalledWith(
      '/console/setting?tab=model-deployment',
    );
  });
});

describe('DeploymentAccessGuard — gate open', () => {
  it('renders the children once enabled and connected', () => {
    renderGuard();

    expect(child()).toBeInTheDocument();
    // And none of the interstitials leaked through.
    expect(document.body.textContent).not.toContain('模型部署服务未启用');
    expect(document.body.textContent).not.toContain('正在检查 io.net 连接...');
  });

  it('an error object alongside ok=true does not block the children', () => {
    // A stale error from a previous probe must not re-close a gate the latest
    // successful probe opened.
    renderGuard({ connectionOk: true, connectionError: { type: 'network' } });
    expect(child()).toBeInTheDocument();
  });
});

describe('DeploymentAccessGuard — settings shortcut on the disabled screen', () => {
  it('routes to the model-deployment settings tab', () => {
    renderGuard({ isEnabled: false });
    fireEvent.click(screen.getByText('前往设置页面'));

    expect(navigate).toHaveBeenCalledWith(
      '/console/setting?tab=model-deployment',
    );
  });

  it('lists both prerequisites so the operator knows what to configure', () => {
    renderGuard({ isEnabled: false });

    expect(document.body.textContent).toContain('启用 io.net 部署开关');
    expect(document.body.textContent).toContain('配置有效的 io.net API Key');
  });

  it('applies hover styling in place without navigating', () => {
    renderGuard({ isEnabled: false });
    const link = screen.getByText('前往设置页面').closest('div');

    fireEvent.mouseEnter(link);
    expect(link.style.background).toBe('var(--semi-color-fill-1)');
    fireEvent.mouseLeave(link);
    expect(link.style.background).toBe('var(--semi-color-fill-0)');
    expect(navigate).not.toHaveBeenCalled();
  });
});
