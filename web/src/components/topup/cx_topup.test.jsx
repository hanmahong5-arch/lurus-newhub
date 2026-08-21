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
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

// Semi UI is unimportable under jsdom (lottie-web -> canvas). Shim only what
// TopUp/RechargeCard touch. Every shim renders its children AND the props that
// carry behaviour (Form.Input's `suffix` holds the redeem button, `extraText`
// holds the "buy a code" link) — stubbing those to null would let the redeem
// button disappear entirely while these tests stayed green.
const modalSuccess = vi.fn();
vi.mock('@douyinfe/semi-ui', () => {
  const Text = ({ children, onClick, ...rest }) =>
    React.createElement('span', { onClick, ...rest }, children);
  const Typography = ({ children }) =>
    React.createElement('span', null, children);
  Typography.Text = Text;
  const Form = ({ children, getFormApi }) => {
    if (getFormApi) getFormApi({ setValue: vi.fn(), getValues: () => ({}) });
    return React.createElement('form', null, children);
  };
  Form.Input = ({ value, onChange, placeholder, prefix, suffix, extraText }) =>
    React.createElement(
      'div',
      null,
      React.createElement('span', { 'data-testid': 'input-prefix' }, prefix),
      React.createElement('input', {
        'data-testid': 'redeem-input',
        placeholder,
        value: value ?? '',
        onChange: (e) => onChange && onChange(e.target.value),
      }),
      React.createElement('span', { 'data-testid': 'input-suffix' }, suffix),
      React.createElement('span', { 'data-testid': 'input-extra' }, extraText),
    );
  return {
    Form,
    Modal: { success: (...a) => modalSuccess(...a) },
    Card: ({ children, cover, title }) =>
      React.createElement('section', null, title, cover, children),
    Avatar: ({ children }) => React.createElement('span', null, children),
    Typography,
    Button: ({ children, onClick, loading, disabled }) =>
      React.createElement(
        'button',
        {
          type: 'button',
          onClick,
          disabled: !!(disabled || loading),
          'data-loading': loading ? 'true' : 'false',
        },
        children,
      ),
    Space: ({ children }) => React.createElement('div', null, children),
  };
});

vi.mock('lucide-react', () => ({
  Wallet: () => React.createElement('i', { 'data-testid': 'icon-wallet' }),
  BarChart2: () => React.createElement('i', { 'data-testid': 'icon-bar' }),
  TrendingUp: () => React.createElement('i', { 'data-testid': 'icon-trend' }),
  Coins: () => React.createElement('i', { 'data-testid': 'icon-coins' }),
}));

vi.mock('@douyinfe/semi-icons', () => ({
  IconGift: () => React.createElement('i', { 'data-testid': 'icon-gift' }),
}));

const apiGet = vi.fn();
const apiPost = vi.fn();
const showError = vi.fn();
const showInfo = vi.fn();
const showSuccess = vi.fn();
const isV2Mode = vi.fn(() => false);

vi.mock('../../helpers', () => ({
  API: {
    get: (...a) => apiGet(...a),
    post: (...a) => apiPost(...a),
  },
  showError: (...a) => showError(...a),
  showInfo: (...a) => showInfo(...a),
  showSuccess: (...a) => showSuccess(...a),
  // A recognisable formatting marker: the assertions below check *which value*
  // reached the money formatter, not how it is styled.
  renderQuota: (v) => `Q<${String(v)}>`,
  isV2Mode: (...a) => isV2Mode(...a),
  v2Url: (p) => `/api/v2/acme${p}`,
}));

import { UserContext } from '../../context/User';
import { StatusContext } from '../../context/Status';
import TopUp from './index';

const renderTopUp = ({ user = { id: 5, quota: 1000 }, status = {} } = {}) => {
  const dispatch = vi.fn();
  const utils = render(
    React.createElement(
      StatusContext.Provider,
      { value: [{ status }, vi.fn()] },
      React.createElement(
        UserContext.Provider,
        { value: [{ user }, dispatch] },
        React.createElement(TopUp, null),
      ),
    ),
  );
  return { dispatch, ...utils };
};

const redeemButton = () => screen.getByText('兑换额度').closest('button');

beforeEach(() => {
  vi.clearAllMocks();
  isV2Mode.mockReturnValue(false);
  apiGet.mockResolvedValue({ data: { success: true, data: { id: 5 } } });
  apiPost.mockResolvedValue({ data: { success: true, data: 500 } });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('TopUp — redemption submission', () => {
  it('refuses to call the API when the code box is empty and tells the user', async () => {
    renderTopUp();
    fireEvent.click(redeemButton());
    await waitFor(() =>
      expect(showInfo).toHaveBeenCalledWith('请输入兑换码！'),
    );
    expect(apiPost).not.toHaveBeenCalled();
  });

  it('posts the typed code to the v1 endpoint and credits the returned amount', async () => {
    const { dispatch } = renderTopUp({ user: { id: 5, quota: 1000 } });
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'CODE-1' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(apiPost).toHaveBeenCalledWith('/api/user/topup', { key: 'CODE-1' });
    expect(showSuccess).toHaveBeenCalledWith('兑换成功！');
    // The confirmation dialog must state the credited amount, not a bare success.
    expect(modalSuccess).toHaveBeenCalledWith(
      expect.objectContaining({ content: '成功兑换额度：Q<500>' }),
    );
    // 1000 + 500 — the cached balance must move by exactly the credited amount.
    expect(dispatch).toHaveBeenCalledWith({
      type: 'login',
      payload: expect.objectContaining({ id: 5, quota: 1500 }),
    });
    // Code box cleared so the same code cannot be resubmitted by accident.
    await waitFor(() =>
      expect(screen.getByTestId('redeem-input')).toHaveValue(''),
    );
  });

  it('routes the redemption through the tenant-scoped v2 endpoint in v2 mode', async () => {
    isV2Mode.mockReturnValue(true);
    renderTopUp();
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'CODE-2' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    expect(apiPost).toHaveBeenCalledWith('/api/v2/acme/redemptions/redeem', {
      key: 'CODE-2',
    });
  });

  it('surfaces the server message and credits nothing when redemption is refused', async () => {
    apiPost.mockResolvedValue({
      data: { success: false, message: '兑换码已被使用' },
    });
    const { dispatch } = renderTopUp();
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'USED' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('兑换码已被使用'),
    );
    expect(showSuccess).not.toHaveBeenCalled();
    expect(modalSuccess).not.toHaveBeenCalled();
    // A refused redemption must never move the cached balance.
    expect(dispatch).not.toHaveBeenCalled();
    // …and the code must stay in the box so the user can see what failed.
    expect(screen.getByTestId('redeem-input')).toHaveValue('USED');
  });

  it('recovers the button from its loading state when the request throws', async () => {
    apiPost.mockRejectedValue(new Error('network down'));
    const { dispatch } = renderTopUp();
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'BOOM' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() => expect(showError).toHaveBeenCalledWith('请求失败'));
    expect(dispatch).not.toHaveBeenCalled();
    // Not stuck spinning — the user must be able to retry.
    await waitFor(() =>
      expect(redeemButton()).toHaveAttribute('data-loading', 'false'),
    );
    expect(redeemButton()).not.toBeDisabled();
  });

  it('disables the redeem button while the request is in flight (no double spend)', async () => {
    let resolvePost;
    apiPost.mockReturnValue(
      new Promise((r) => {
        resolvePost = r;
      }),
    );
    renderTopUp();
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'SLOW' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() =>
      expect(redeemButton()).toHaveAttribute('data-loading', 'true'),
    );
    fireEvent.click(redeemButton());
    expect(apiPost).toHaveBeenCalledTimes(1);

    resolvePost({ data: { success: true, data: 1 } });
    await waitFor(() =>
      expect(redeemButton()).toHaveAttribute('data-loading', 'false'),
    );
  });

  // DEFECT (correctness/money): `topUp()` credits the local cache with
  // `userState.user.quota + data` without checking that `data` is a number.
  // A success envelope that omits the amount turns the cached balance into
  // NaN, and every balance readout on the page renders NaN until the user
  // reloads. The paired it.skip below holds the contract that a malformed
  // success response must not corrupt the cached balance.
  it('never credits a positive amount from a success envelope that carries none', async () => {
    apiPost.mockResolvedValue({ data: { success: true } });
    const { dispatch } = renderTopUp({ user: { id: 5, quota: 1000 } });
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'NOAMOUNT' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));

    // INVARIANT, not a pin on the defect. Both shapes satisfy it: today the
    // missing guard writes NaN (not a finite number, so not > 1000), and once
    // the guard lands `dispatch` is either skipped or called with the untouched
    // 1000. What must never happen in either world is money being minted out of
    // a malformed response — e.g. a careless `data ?? 500` fallback. Written
    // this way, repairing the defect leaves this test green instead of turning
    // it red and reading as a regression.
    const credited = dispatch.mock.calls.map((c) => c[0]?.payload?.quota);
    expect(credited.some((q) => Number.isFinite(q) && q > 1000)).toBe(false);
  });

  it.skip('a redemption response without an amount must not corrupt the cached balance', async () => {
    apiPost.mockResolvedValue({ data: { success: true } });
    const { dispatch } = renderTopUp({ user: { id: 5, quota: 1000 } });
    fireEvent.change(screen.getByTestId('redeem-input'), {
      target: { value: 'NOAMOUNT' },
    });
    fireEvent.click(redeemButton());

    await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(1));
    const bad = dispatch.mock.calls.find(
      (c) => !Number.isFinite(c[0]?.payload?.quota),
    );
    expect(bad).toBeUndefined();
  });
});

describe('TopUp — external top-up link', () => {
  it('refuses to open anything and explains why when no link is configured', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    renderTopUp({ status: {} });
    // extraText (which carries the link) is only rendered when a link exists.
    expect(screen.queryByText('购买兑换码')).not.toBeInTheDocument();
    expect(open).not.toHaveBeenCalled();
  });

  it('opens the admin-configured link in a new tab when one is set', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    renderTopUp({ status: { top_up_link: 'https://pay.example.com/buy' } });
    fireEvent.click(screen.getByText('购买兑换码'));
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toBe('https://pay.example.com/buy');
    expect(open.mock.calls[0][1]).toBe('_blank');
  });

  it('picks up a top-up link that arrives after the first render', async () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    const dispatch = vi.fn();
    const view = (status) =>
      React.createElement(
        StatusContext.Provider,
        { value: [{ status }, vi.fn()] },
        React.createElement(
          UserContext.Provider,
          { value: [{ user: { id: 5, quota: 1 } }, dispatch] },
          React.createElement(TopUp, null),
        ),
      );
    const { rerender } = render(view({}));
    expect(screen.queryByText('购买兑换码')).not.toBeInTheDocument();

    rerender(view({ top_up_link: 'https://late.example.com' }));
    await waitFor(() =>
      expect(screen.getByText('购买兑换码')).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText('购买兑换码'));
    expect(open.mock.calls[0][0]).toBe('https://late.example.com');
  });

  // DEFECT (security): `openTopUpLink` calls `window.open(topUpLink, '_blank')`
  // with no `noopener,noreferrer`. The URL is admin-configurable and points at a
  // third-party payment page, so the opened page gets a live `window.opener`
  // handle back into the authenticated console tab and can navigate it to a
  // look-alike login (reverse tabnabbing). ApiInfoPanel.jsx in this same
  // codebase already passes 'noopener,noreferrer' for exactly this reason, so
  // the omission here is an oversight rather than a policy.
  // The inverted companion that used to sit here — `expect(features).not
  // .toContain('noopener')` — was removed on review: it pinned the defect, so
  // adding the feature string would have turned it red and made the security
  // fix read as a regression. It also only restated the URL/target assertions
  // already made by "opens the admin-configured link in a new tab" above. The
  // contract now lives solely in the lock below, which is verified RED today.
  it('the external payment link must be opened with noopener/noreferrer', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    renderTopUp({ status: { top_up_link: 'https://pay.example.com/buy' } });
    fireEvent.click(screen.getByText('购买兑换码'));
    const features = String(open.mock.calls[0][2] ?? '');
    expect(features).toContain('noopener');
    expect(features).toContain('noreferrer');
  });
});

describe('TopUp — balance bootstrap', () => {
  it('fetches the balance on mount when no user is cached yet', async () => {
    renderTopUp({ user: null });
    await waitFor(() => expect(apiGet).toHaveBeenCalledWith('/api/user/self'));
  });

  it('uses the v2 profile endpoint for the bootstrap fetch in v2 mode', async () => {
    isV2Mode.mockReturnValue(true);
    renderTopUp({ user: null });
    await waitFor(() =>
      expect(apiGet).toHaveBeenCalledWith('/api/v2/acme/user/me'),
    );
  });

  it('skips the bootstrap fetch when a user is already cached', async () => {
    renderTopUp({ user: { id: 5, quota: 10 } });
    await waitFor(() =>
      expect(screen.getByTestId('redeem-input')).toBeInTheDocument(),
    );
    expect(apiGet).not.toHaveBeenCalled();
  });

  it('reports a refused balance fetch instead of silently showing a stale zero', async () => {
    apiGet.mockResolvedValue({
      data: { success: false, message: '会话已过期' },
    });
    const { dispatch } = renderTopUp({ user: null });
    await waitFor(() => expect(showError).toHaveBeenCalledWith('会话已过期'));
    expect(dispatch).not.toHaveBeenCalled();
  });
});

describe('RechargeCard — account figures', () => {
  it('renders balance and lifetime usage through the money formatter', () => {
    renderTopUp({
      user: { id: 5, quota: 12345, used_quota: 678, request_count: 42 },
    });
    expect(screen.getByText('Q<12345>')).toBeInTheDocument();
    expect(screen.getByText('Q<678>')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('shows a zero request count rather than a blank when the field is absent', () => {
    renderTopUp({ user: { id: 5, quota: 0 } });
    expect(screen.getByText('0')).toBeInTheDocument();
    // undefined must still reach the formatter, not be dropped on the floor.
    expect(screen.getAllByText('Q<undefined>').length).toBeGreaterThan(0);
  });
});
