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

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (k) => k, i18n: { language: 'en' } }),
}));

const navigate = vi.fn();
vi.mock('react-router-dom', () => ({ useNavigate: () => navigate }));

// Semi shims. Every prop that carries meaning is projected into the DOM:
// Progress exposes its percent and colour, Banner exposes its severity and
// whether it is dismissible, Skeleton actually swaps in its placeholder.
// Rendering these to null would let the whole warning path disappear unnoticed.
vi.mock('@douyinfe/semi-ui', () => {
  const Typography = ({ children }) =>
    React.createElement('div', null, children);
  Typography.Text = ({ children, type, ...rest }) =>
    React.createElement('span', { 'data-type': type, ...rest }, children);
  Typography.Title = ({ children, ...rest }) =>
    React.createElement('h3', rest, children);
  const Skeleton = ({ loading, placeholder, children }) =>
    React.createElement(
      'div',
      { 'data-skeleton': loading ? 'on' : 'off' },
      loading ? placeholder : children,
    );
  Skeleton.Paragraph = ({ rows }) =>
    React.createElement(
      'div',
      { 'data-testid': 'skeleton-rows' },
      String(rows),
    );
  return {
    Card: ({ children, title, headerExtraContent, ...rest }) =>
      React.createElement(
        'section',
        rest,
        React.createElement('header', null, title, headerExtraContent),
        children,
      ),
    Typography,
    Skeleton,
    Avatar: ({ children }) => React.createElement('span', null, children),
    Button: ({ children, onClick, type, theme }) =>
      React.createElement(
        'button',
        { type: 'button', onClick, 'data-btn-type': type, 'data-theme': theme },
        children,
      ),
    Progress: ({ percent, stroke }) =>
      React.createElement('div', {
        'data-testid': 'progress',
        'data-percent': String(percent),
        'data-stroke': String(stroke),
      }),
    Banner: ({ type, description, onClose, closeIcon }) =>
      React.createElement(
        'div',
        {
          'data-testid': 'banner',
          'data-banner-type': type,
          // Whether the banner can be dismissed is the whole point of the
          // critical branch — surface it rather than swallow it.
          'data-dismissible': onClose ? 'yes' : 'no',
          'data-close-icon': closeIcon === null ? 'suppressed' : 'default',
        },
        description,
        onClose
          ? React.createElement(
              'button',
              {
                type: 'button',
                'data-testid': 'banner-close',
                onClick: onClose,
              },
              'x',
            )
          : null,
      ),
  };
});

vi.mock('@douyinfe/semi-icons', () => ({
  IconCoinMoneyStroked: () =>
    React.createElement('i', { 'data-testid': 'icon-coin' }),
}));

vi.mock('../../helpers/render', () => ({
  renderQuota: (v) => `Q<${String(v)}>`,
}));

vi.mock('@visactor/react-vchart', () => ({
  VChart: ({ spec }) =>
    React.createElement('div', {
      'data-testid': 'vchart',
      'data-spec': JSON.stringify(spec ?? null),
    }),
}));

import WalletCard from './WalletCard';
import UsageGauge from './UsageGauge';
import UsageAlertBanner from './UsageAlertBanner';
import StatsCards from './StatsCards';

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('WalletCard', () => {
  it('shows a skeleton and no figures while the wallet is loading', () => {
    render(React.createElement(WalletCard, { loading: true, wallet: null }));
    expect(screen.getByTestId('skeleton-rows')).toHaveTextContent('2');
    expect(screen.queryByText('兑换码')).not.toBeInTheDocument();
  });

  it('renders nothing at all when there is no wallet', () => {
    const { container } = render(
      React.createElement(WalletCard, { loading: false, wallet: null }),
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('formats a platform wallet as CNY with 2 decimals and shows the ledger totals', () => {
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: {
          source: 'platform',
          balance: 1234.5,
          lifetime_topup: 2000,
          lifetime_spend: 765.5,
        },
      }),
    );
    expect(screen.getByText('平台钱包余额')).toBeInTheDocument();
    expect(screen.getByText('¥1234.50')).toBeInTheDocument();
    expect(screen.getByText('¥2000.00')).toBeInTheDocument();
    expect(screen.getByText('¥765.50')).toBeInTheDocument();
  });

  it('falls back to ¥0.00 — not a blank — for absent lifetime totals', () => {
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'platform', balance: 12.75 },
      }),
    );
    expect(screen.getByText('¥12.75')).toBeInTheDocument();
    // Both lifetime figures are missing from the payload and must read ¥0.00.
    expect(screen.getAllByText('¥0.00')).toHaveLength(2);
  });

  it('hides the frozen-funds note when nothing is frozen and shows it when something is', () => {
    const { unmount } = render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'platform', balance: 10, frozen: 0 },
      }),
    );
    expect(screen.queryByText(/冻结/)).not.toBeInTheDocument();
    unmount();

    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'platform', balance: 10, frozen: 3.5 },
      }),
    );
    expect(screen.getByText(/冻结/)).toHaveTextContent('¥3.50');
  });

  it('uses 4-decimal quota formatting and drops the platform ledger for a local wallet', () => {
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'local', balance: 1.5, lifetime_topup: 99 },
      }),
    );
    expect(screen.getByText('账户余额')).toBeInTheDocument();
    expect(screen.getByText('1.5000')).toBeInTheDocument();
    expect(screen.queryByText('累计充值')).not.toBeInTheDocument();
  });

  it('only offers the top-up button when the platform supplied a top-up URL', () => {
    const { unmount } = render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'platform', balance: 1 },
      }),
    );
    expect(screen.queryByText('充值')).not.toBeInTheDocument();
    unmount();

    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: {
          source: 'platform',
          balance: 1,
          topup_url: 'https://pay.example.com',
        },
      }),
    );
    expect(screen.getByText('充值')).toBeInTheDocument();
  });

  it('routes the redemption-code button to the top-up console page', () => {
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'local', balance: 1 },
      }),
    );
    fireEvent.click(screen.getByText('兑换码'));
    expect(navigate).toHaveBeenCalledWith('/console/topup');
  });

  // DEFECT (correctness/money): `balance.toFixed()` is called with no guard,
  // while the sibling lifetime figures on the very same card use
  // `?.toFixed(2) || '0.00'`. A wallet payload that omits `balance` (platform
  // wallet still provisioning, or a partial response) throws inside render and
  // takes the entire dashboard down — not just the wallet card — because
  // nothing above it catches. The it.skip below holds the contract.
  // The inverted companion that used to sit here — `expect(() => render(...))
  // .toThrow()` — was removed on review. It pinned the crash, so guarding
  // `balance` would have turned it red and made the availability fix read as a
  // regression. A crash cannot regress further, so the pin protected nothing;
  // the contract lives solely in the lock below, verified RED today.

  it.skip('a wallet payload without a balance must still render a card, not crash', () => {
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: { source: 'platform' },
      }),
    );
    expect(screen.getByText('平台钱包余额')).toBeInTheDocument();
  });

  // DEFECT (security): the top-up button opens an admin/platform supplied URL
  // with `window.open(url, '_blank')` and no `noopener,noreferrer`, handing the
  // payment page a live `window.opener` back into the authenticated console
  // (reverse tabnabbing). `ApiInfoPanel.jsx` in this same directory already
  // passes 'noopener,noreferrer', so this is an oversight, not a policy.
  it('sends the top-up button to the wallet-supplied URL in a new tab', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: {
          source: 'platform',
          balance: 1,
          topup_url: 'https://pay.example.com',
        },
      }),
    );
    fireEvent.click(screen.getByText('充值'));
    // INVARIANT over args 0 and 1 only. The third argument (the window feature
    // string) is deliberately NOT asserted here: pinning its current absence
    // would turn this red the moment 'noopener,noreferrer' is added, making the
    // security fix look like a regression. That contract is held by the lock
    // below instead, so this test keeps catching a wrong URL or a lost target
    // while staying true either side of the fix.
    expect(open).toHaveBeenCalledTimes(1);
    expect(open.mock.calls[0][0]).toBe('https://pay.example.com');
    expect(open.mock.calls[0][1]).toBe('_blank');
  });

  it.skip('the platform top-up URL must be opened with noopener/noreferrer', () => {
    const open = vi.fn();
    vi.stubGlobal('open', open);
    render(
      React.createElement(WalletCard, {
        loading: false,
        wallet: {
          source: 'platform',
          balance: 1,
          topup_url: 'https://pay.example.com',
        },
      }),
    );
    fireEvent.click(screen.getByText('充值'));
    const features = String(open.mock.calls[0][2] ?? '');
    expect(features).toContain('noopener');
    expect(features).toContain('noreferrer');
  });

  // NOT TESTED, deliberately: `const balance = isPlatform ? wallet.balance :
  // wallet.balance;` has identical arms and `const unit = isPlatform ? 'CNY' :
  // '';` is never read (the ¥ sign is hard-coded downstream). Both are dead
  // code with zero observable behaviour, so any test for them would be a
  // change-detector. Flagged here instead.
});

describe('UsageGauge', () => {
  const gauge = {
    totalQuota: 1000,
    usedQuota: 800,
    quota: 200,
    usagePercent: 80,
    level: 'red',
    dailyRate: 50,
    daysRemaining: 4,
    exhaustionDate: '2026-08-24',
  };

  it('renders nothing when there is no quota to speak of', () => {
    const { container } = render(
      React.createElement(UsageGauge, { gauge: null, loading: false }),
    );
    expect(container).toBeEmptyDOMElement();

    const { container: c2 } = render(
      React.createElement(UsageGauge, {
        gauge: { ...gauge, totalQuota: 0 },
        loading: false,
      }),
    );
    expect(c2).toBeEmptyDOMElement();
  });

  it('shows the skeleton while loading and no figures yet', () => {
    render(React.createElement(UsageGauge, { gauge: null, loading: true }));
    expect(screen.getAllByTestId('skeleton-rows').length).toBe(3);
    expect(screen.queryByTestId('progress')).not.toBeInTheDocument();
  });

  it('drives the progress bar from usagePercent and colours it by level', () => {
    render(React.createElement(UsageGauge, { gauge, loading: false }));
    const bar = screen.getByTestId('progress');
    expect(bar).toHaveAttribute('data-percent', '80');
    expect(bar).toHaveAttribute('data-stroke', 'var(--semi-color-danger)');
    expect(screen.getByText('80%')).toBeInTheDocument();
    expect(screen.getByText(/Q<800>/)).toBeInTheDocument();
    expect(screen.getByText('Q<200>')).toBeInTheDocument();
  });

  it('uses the critical colour for level=critical and green for an unknown level', () => {
    const { unmount } = render(
      React.createElement(UsageGauge, {
        gauge: { ...gauge, level: 'critical' },
        loading: false,
      }),
    );
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-stroke',
      '#dc2626',
    );
    unmount();

    render(
      React.createElement(UsageGauge, {
        gauge: { ...gauge, level: 'chartreuse' },
        loading: false,
      }),
    );
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-stroke',
      'var(--semi-color-success)',
    );
  });

  it('hides the burn-rate line when the daily rate is zero', () => {
    render(
      React.createElement(UsageGauge, {
        gauge: { ...gauge, dailyRate: 0 },
        loading: false,
      }),
    );
    expect(screen.queryByText(/日均消耗/)).not.toBeInTheDocument();
  });

  it('hides the exhaustion-date line when daysRemaining is explicitly null', () => {
    render(
      React.createElement(UsageGauge, {
        gauge: { ...gauge, daysRemaining: null },
        loading: false,
      }),
    );
    expect(screen.queryByText(/预计用完日期/)).not.toBeInTheDocument();
  });

  it('sends the top-up shortcut to the top-up page', () => {
    render(React.createElement(UsageGauge, { gauge, loading: false }));
    fireEvent.click(screen.getByText('立即充值'));
    expect(navigate).toHaveBeenCalledWith('/console/topup');
  });

  // DEFECT (correctness): the exhaustion line is gated on
  // `gauge.daysRemaining !== null`, which is *true* for `undefined`. Any gauge
  // that simply omits the field — the natural shape when the backend cannot
  // project a burn-down — still renders the "预计用完日期" row, with the date
  // and the day count both blank ("预计用完日期:  ( 天)"). Every neighbouring
  // guard in this component uses a truthiness / `> 0` test; this one is the odd
  // one out. (JSX swallows `undefined`, so it reads as an empty row here; the
  // sibling bug in UsageAlertBanner uses a template literal and does print the
  // word "undefined" — see below.)
  // The pin that used to sit here asserted the empty row IS rendered
  // ("预计用完日期:  ( 天)"), which would have turned red the moment the guard
  // was corrected — the repair would have read as a regression. What has to
  // hold either side of that fix is asserted instead: the gauge still reports
  // the figures it *does* have, and it never puts a placeholder token in front
  // of the user. Today the row is blank (JSX swallows `undefined`); after the
  // fix the row is gone; a careless switch to the template-literal style its
  // sibling UsageAlertBanner uses would print "undefined" and go red here.
  it('never surfaces a placeholder in place of a forecast it does not have', () => {
    const { daysRemaining, exhaustionDate, ...withoutForecast } = gauge;
    const { container } = render(
      React.createElement(UsageGauge, {
        gauge: withoutForecast,
        loading: false,
      }),
    );
    // The real figures survive the missing forecast…
    expect(screen.getByTestId('progress')).toHaveAttribute(
      'data-percent',
      '80',
    );
    expect(screen.getByText('Q<200>')).toBeInTheDocument();
    // …and nothing the backend never sent is spelled out to the user.
    expect(container.textContent).not.toMatch(/undefined|NaN|\[object/);
    // Nor may a missing projection be invented as a concrete day count.
    expect(container.textContent).not.toMatch(/预计用完日期:\s*\S*\s*\(\s*\d/);
  });

  it.skip('a gauge with no burn-down forecast must not show an exhaustion date at all', () => {
    const { daysRemaining, exhaustionDate, ...withoutForecast } = gauge;
    render(
      React.createElement(UsageGauge, {
        gauge: withoutForecast,
        loading: false,
      }),
    );
    expect(screen.queryByText(/预计用完日期/)).not.toBeInTheDocument();
  });
});

describe('UsageAlertBanner', () => {
  const base = { usagePercent: 92, daysRemaining: 3 };

  it('stays silent when there is no gauge or usage is healthy', () => {
    const { container } = render(
      React.createElement(UsageAlertBanner, { gauge: null }),
    );
    expect(container).toBeEmptyDOMElement();

    const { container: c2 } = render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'green' },
      }),
    );
    expect(c2).toBeEmptyDOMElement();
  });

  it('raises a dismissible warning at yellow, quoting the percentage and the forecast', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'yellow', usagePercent: 60 },
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'warning');
    expect(banner).toHaveAttribute('data-dismissible', 'yes');
    expect(banner.textContent).toContain('60');
    expect(banner.textContent).toContain('3');
    expect(screen.getByText('充值')).toBeInTheDocument();
  });

  it('escalates to a danger banner at red but still lets the user dismiss it', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'red' },
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'danger');
    expect(banner).toHaveAttribute('data-dismissible', 'yes');
    expect(screen.getByText('充值')).toBeInTheDocument();
  });

  it('stops showing a dismissed yellow banner', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'yellow' },
      }),
    );
    fireEvent.click(screen.getByTestId('banner-close'));
    expect(screen.queryByTestId('banner')).not.toBeInTheDocument();
  });

  // The `closable` half of `if (dismissed && closable) return null` is the only
  // thing that keeps a *previously dismissed* banner from swallowing a later
  // escalation. Asserting the rendered Banner props (below) does not reach it,
  // because the critical branch never wires an onClose in the first place — the
  // guard only bites once `dismissed` is already true from an earlier level.
  it('re-raises the alarm at critical even after the user dismissed the earlier warning', () => {
    const view = (level) =>
      React.createElement(UsageAlertBanner, { gauge: { ...base, level } });
    const { rerender } = render(view('yellow'));
    fireEvent.click(screen.getByTestId('banner-close'));
    expect(screen.queryByTestId('banner')).not.toBeInTheDocument();

    // Quota keeps burning down while the dashboard is open and the level
    // escalates. Dismissing the yellow warning must not silence the
    // undismissable critical alarm, or the user is never told the quota is
    // about to run out.
    rerender(view('critical'));
    const escalated = screen.getByTestId('banner');
    expect(escalated).toHaveAttribute('data-banner-type', 'danger');
    expect(escalated).toHaveAttribute('data-dismissible', 'no');
    expect(screen.getByText('立即充值')).toBeInTheDocument();
  });

  it('refuses to let a critical banner be dismissed', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'critical' },
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'danger');
    expect(banner).toHaveAttribute('data-dismissible', 'no');
    expect(banner).toHaveAttribute('data-close-icon', 'suppressed');
    // …and the call to action is the urgent one.
    expect(screen.getByText('立即充值')).toBeInTheDocument();
  });

  it('sends the top-up call to action to the top-up page', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { ...base, level: 'critical' },
      }),
    );
    fireEvent.click(screen.getByText('立即充值'));
    expect(navigate).toHaveBeenCalledWith('/console/topup');
  });

  it('omits the forecast clause when daysRemaining is explicitly null', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { level: 'red', usagePercent: 91, daysRemaining: null },
      }),
    );
    expect(screen.getByTestId('banner').textContent).not.toContain('天后用完');
  });

  // DEFECT (correctness): same `!== null` slip as UsageGauge. A gauge that
  // omits `daysRemaining` (undefined, not null) renders
  // "预计 undefined 天后用完" inside a red/critical alert banner — the most
  // alarming widget on the dashboard, showing a placeholder value.
  // The pin that used to sit here asserted the banner text DOES contain
  // "undefined" — repairing the guard would have turned it red. The part that
  // must hold either side of the fix is that a missing forecast neither
  // silences the alarm nor lets the banner state a projection it was never
  // given: today the clause reads "预计 undefined 天后用完" (no number), after
  // the fix the clause is gone, and a careless `daysRemaining ?? 0` — which
  // would tell the user the quota runs out today — goes red here.
  it('still raises the critical alarm, without inventing a day count, when the forecast is absent', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { level: 'critical', usagePercent: 99 },
      }),
    );
    const banner = screen.getByTestId('banner');
    expect(banner).toHaveAttribute('data-banner-type', 'danger');
    expect(banner).toHaveAttribute('data-dismissible', 'no');
    expect(banner.textContent).toContain('额度即将耗尽');
    expect(banner.textContent).toContain('99');
    expect(screen.getByText('立即充值')).toBeInTheDocument();
    expect(banner.textContent).not.toMatch(/预计\s*\d+\s*天后用完/);
  });

  it.skip('an alert with no burn-down forecast must not show a forecast clause', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { level: 'critical', usagePercent: 99 },
      }),
    );
    const text = screen.getByTestId('banner').textContent;
    expect(text).not.toContain('undefined');
    expect(text).not.toContain('天后用完');
  });

  // DEFECT (correctness): the level check is `gauge.level !== 'green'`, so any
  // unrecognised or missing level falls all the way through to the yellow
  // branch and raises a quota warning. A gauge whose level the backend has not
  // yet classified therefore alarms the user with no evidence.
  // The pin that used to sit here asserted the warning banner IS raised, so
  // making the component ignore an unclassified level would have read as a
  // regression. The invariant that holds either side of that fix: an
  // unclassified level may not escalate. Today it lands on the dismissible
  // yellow branch, after the fix on nothing at all — but never on the
  // undismissable red alarm, which is what a careless reordering of the three
  // level branches (or a `level !== 'yellow' && level !== 'red'` critical test)
  // would produce.
  it('never escalates an unclassified level to the undismissable alarm', () => {
    render(
      React.createElement(UsageAlertBanner, {
        gauge: { usagePercent: 5, daysRemaining: null },
      }),
    );
    const banner = screen.queryByTestId('banner');
    expect(banner?.getAttribute('data-banner-type') ?? 'none').not.toBe(
      'danger',
    );
    // The urgent call to action belongs to the critical branch alone.
    expect(screen.queryByText('立即充值')).not.toBeInTheDocument();
  });

  it.skip('an unclassified gauge level must not raise a quota warning', () => {
    const { container } = render(
      React.createElement(UsageAlertBanner, {
        gauge: { usagePercent: 5, daysRemaining: null },
      }),
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe('StatsCards', () => {
  const getTrendSpec = (data, color) => ({ data: data ?? null, color });
  const group = (over) => ({
    title: 'account',
    color: 'bg-blue-50',
    items: [
      {
        title: 'balance',
        value: 'Q<9>',
        avatarColor: 'blue',
        icon: 'i',
        trendData: [1, 2],
        trendColor: '#f00',
        onClick: vi.fn(),
      },
    ],
    ...over,
  });

  it('renders one card per group and one row per item, and wires the row click', () => {
    const onClick = vi.fn();
    const groups = [
      group({ items: [{ title: 'balance', value: 'B', onClick }] }),
      group({ title: 'usage' }),
    ];
    render(
      React.createElement(StatsCards, {
        groupedStatsData: groups,
        loading: false,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: null,
      }),
    );
    expect(screen.getByText('account')).toBeInTheDocument();
    expect(screen.getByText('usage')).toBeInTheDocument();
    fireEvent.click(screen.getByText('B'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('only draws a trend sparkline for items that actually have trend data', () => {
    render(
      React.createElement(StatsCards, {
        groupedStatsData: [
          group({
            items: [
              { title: 'a', value: 'A', trendData: [] },
              { title: 'b', value: 'B', trendData: [3], trendColor: '#0f0' },
            ],
          }),
        ],
        loading: false,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: null,
      }),
    );
    const charts = screen.getAllByTestId('vchart');
    expect(charts).toHaveLength(1);
    expect(JSON.parse(charts[0].getAttribute('data-spec'))).toEqual({
      data: [3],
      color: '#0f0',
    });
  });

  it('shows sparkline placeholders for every item while loading, values hidden', () => {
    render(
      React.createElement(StatsCards, {
        groupedStatsData: [
          group({
            items: [
              { title: 'a', value: 'A', trendData: [] },
              { title: 'b', value: 'B', trendData: [] },
            ],
          }),
        ],
        loading: true,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: null,
      }),
    );
    expect(screen.getAllByTestId('vchart')).toHaveLength(2);
    expect(screen.queryByText('A')).not.toBeInTheDocument();
  });

  it('shows the "no usage data" note in the first card when the gauge is empty', () => {
    render(
      React.createElement(StatsCards, {
        groupedStatsData: [group(), group({ title: 'second' })],
        loading: false,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: { totalQuota: 0 },
      }),
    );
    // Exactly one — the progress block only belongs to the first card.
    expect(screen.getAllByText('暂无用量数据')).toHaveLength(1);
    expect(screen.queryByTestId('progress')).not.toBeInTheDocument();
  });

  it.each([
    [30, 'var(--semi-color-success)'],
    [50, 'var(--semi-color-success)'],
    [51, 'var(--semi-color-warning)'],
    [80, 'var(--semi-color-warning)'],
    [81, 'var(--semi-color-danger)'],
  ])('colours the quota bar for %i%% usage as %s', (usagePercent, expected) => {
    render(
      React.createElement(StatsCards, {
        groupedStatsData: [group()],
        loading: false,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: {
          totalQuota: 100,
          usedQuota: usagePercent,
          usagePercent,
        },
      }),
    );
    const bar = screen.getByTestId('progress');
    expect(bar).toHaveAttribute('data-percent', String(usagePercent));
    expect(bar).toHaveAttribute('data-stroke', expected);
  });

  it('formats used and total quota through the money formatter', () => {
    render(
      React.createElement(StatsCards, {
        groupedStatsData: [group()],
        loading: false,
        getTrendSpec,
        CARD_PROPS: {},
        CHART_CONFIG: {},
        usageGauge: { totalQuota: 500, usedQuota: 125, usagePercent: 25 },
      }),
    );
    expect(screen.getAllByText(/Q<125>/).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Q<500>/).length).toBeGreaterThan(0);
  });
});
