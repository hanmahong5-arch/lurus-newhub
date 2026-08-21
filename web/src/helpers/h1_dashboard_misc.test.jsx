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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

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

// Semi UI's barrel (and its illustration package) need a canvas jsdom lacks.
vi.mock('@douyinfe/semi-ui', () => ({
  Toast: { error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn() },
  Pagination: () => null,
  Progress: (props) =>
    React.createElement('div', {
      'data-testid': 'progress',
      'data-percent': String(props.percent),
    }),
  Divider: () => React.createElement('hr'),
  Empty: (props) =>
    React.createElement('div', { 'data-testid': 'empty' }, props.description),
}));
vi.mock('@douyinfe/semi-illustrations', () => ({
  IllustrationConstruction: () => null,
  IllustrationConstructionDark: () => null,
}));
vi.mock('react-toastify', () => ({ toast: vi.fn() }));
vi.mock('../components/hifi/HfToast', () => ({
  hfToast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}));

// dashboard.jsx only reaches into utils for the clipboard + success toast.
vi.mock('./utils', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    copy: vi.fn().mockResolvedValue(true),
    showSuccess: vi.fn(),
  };
});

// auth.jsx renders react-router Navigate; a stub keeps the assertions about
// WHERE it redirects rather than about router internals.
vi.mock('react-router-dom', () => ({
  Navigate: ({ to, replace, state }) =>
    React.createElement('div', {
      'data-testid': 'navigate',
      'data-to': to,
      'data-replace': String(!!replace),
      'data-has-from': state && 'from' in state ? 'yes' : 'no',
    }),
}));

import { copy, showSuccess } from './utils';
import { setStatusData, setUserData } from './data';
import {
  isVerificationRequiredError,
  extractVerificationInfo,
} from './secureApiCall';
import { authHeader, AuthRedirect, PrivateRoute, AdminRoute } from './auth';
import {
  getDefaultTime,
  getTimeInterval,
  getInitialTimestamp,
  updateMapValue,
  initializeMaps,
  updateChartSpec,
  getTrendSpec,
  getUptimeStatusColor,
  getUptimeStatusText,
  handleCopyUrl,
  handleSpeedTest,
  processRawData,
  calculateTrendData,
  aggregateDataByTimeAndModel,
  generateChartTimePoints,
} from './dashboard';

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
});

// ── data.js ─────────────────────────────────────────────────────────────
describe('setStatusData', () => {
  const full = {
    system_name: 'Acme Hub',
    logo: '/brand.svg',
    footer_html: '<p>c</p>',
    quota_per_unit: 500000,
    display_in_currency: true,
    quota_display_type: 'CNY',
    enable_drawing: false,
    enable_task: true,
    enable_data_export: true,
    chats: [{ id: 1 }],
    data_export_default_time: 'day',
    default_collapse_sidebar: false,
    mj_notify_enabled: true,
    docs_link: 'https://docs.example.test',
  };

  it('persists the whole blob plus the individual hot keys', () => {
    setStatusData(full);
    expect(JSON.parse(localStorage.getItem('status')).system_name).toBe(
      'Acme Hub',
    );
    expect(localStorage.getItem('system_name')).toBe('Acme Hub');
    expect(localStorage.getItem('logo')).toBe('/brand.svg');
    expect(localStorage.getItem('quota_per_unit')).toBe('500000');
    expect(localStorage.getItem('quota_display_type')).toBe('CNY');
    expect(JSON.parse(localStorage.getItem('chats'))).toEqual([{ id: 1 }]);
    expect(localStorage.getItem('data_export_default_time')).toBe('day');
    expect(localStorage.getItem('docs_link')).toBe('https://docs.example.test');
  });

  it('defaults the display type to USD when the server omits it', () => {
    setStatusData({ ...full, quota_display_type: undefined });
    expect(localStorage.getItem('quota_display_type')).toBe('USD');
  });

  it('clears stale chat and docs links when the server drops them', () => {
    localStorage.setItem('chat_link', 'old');
    localStorage.setItem('chat_link2', 'old2');
    localStorage.setItem('docs_link', 'old3');
    setStatusData({ ...full, docs_link: undefined });
    expect(localStorage.getItem('chat_link')).toBeNull();
    expect(localStorage.getItem('chat_link2')).toBeNull();
    expect(localStorage.getItem('docs_link')).toBeNull();
  });

  it('should not persist the literal text "undefined" for missing fields', () => {
    // DEFECT: every setItem here is unconditional, and Storage stringifies
    // its argument. A /api/status response missing `logo`, `system_name` or
    // `quota_per_unit` therefore stores the 9-character string "undefined",
    // which is TRUTHY -- so getLogo()/getSystemName() never reach their
    // fallbacks, and parseFloat("undefined") is NaN, which is what turns
    // every renderQuota() call into "$NaN".
    setStatusData({});
    expect(localStorage.getItem('logo')).toBeNull();
    expect(localStorage.getItem('system_name')).toBeNull();
    expect(localStorage.getItem('quota_per_unit')).toBeNull();
  });
});

describe('setUserData', () => {
  it('stores the user object as JSON under the canonical key', () => {
    setUserData({ id: 3, role: 10, username: 'ops' });
    expect(JSON.parse(localStorage.getItem('user'))).toEqual({
      id: 3,
      role: 10,
      username: 'ops',
    });
  });
});

// ── secureApiCall.js ────────────────────────────────────────────────────
describe('isVerificationRequiredError', () => {
  const err = (status, code) => ({ response: { status, data: { code } } });

  it('recognises all three step-up verification codes on a 403', () => {
    expect(isVerificationRequiredError(err(403, 'VERIFICATION_REQUIRED'))).toBe(
      true,
    );
    expect(isVerificationRequiredError(err(403, 'VERIFICATION_EXPIRED'))).toBe(
      true,
    );
    expect(isVerificationRequiredError(err(403, 'VERIFICATION_INVALID'))).toBe(
      true,
    );
  });

  it('ignores a 403 that is a plain permission denial', () => {
    expect(isVerificationRequiredError(err(403, 'FORBIDDEN'))).toBe(false);
    expect(isVerificationRequiredError(err(403, undefined))).toBe(false);
  });

  it('ignores the same code on any other status', () => {
    expect(isVerificationRequiredError(err(401, 'VERIFICATION_REQUIRED'))).toBe(
      false,
    );
    expect(isVerificationRequiredError(err(500, 'VERIFICATION_REQUIRED'))).toBe(
      false,
    );
  });

  it('is false for a transport-level error with no response', () => {
    expect(isVerificationRequiredError(new Error('offline'))).toBe(false);
    expect(
      isVerificationRequiredError({ response: { status: 403, data: null } }),
    ).toBe(false);
  });
});

describe('extractVerificationInfo', () => {
  it('lifts the code and message off the response', () => {
    expect(
      extractVerificationInfo({
        response: {
          data: { code: 'VERIFICATION_EXPIRED', message: '请重新验证' },
        },
      }),
    ).toEqual({
      code: 'VERIFICATION_EXPIRED',
      message: '请重新验证',
      required: true,
    });
  });

  it('supplies a default prompt when the server sent no message', () => {
    expect(extractVerificationInfo({ response: { data: {} } })).toEqual({
      code: undefined,
      message: '需要安全验证',
      required: true,
    });
  });

  it('tolerates an error with no response at all', () => {
    expect(extractVerificationInfo({}).message).toBe('需要安全验证');
  });
});

// ── auth.jsx ────────────────────────────────────────────────────────────
describe('authHeader', () => {
  it('is empty when nobody is signed in', () => {
    expect(authHeader()).toEqual({});
  });

  it('is empty for a session-cookie user that carries no bearer token', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    expect(authHeader()).toEqual({});
  });

  it('emits a bearer header when a token is present', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, token: 'abc123' }));
    expect(authHeader()).toEqual({ Authorization: 'Bearer abc123' });
  });

  it('should degrade gracefully when the user shim is corrupted', () => {
    // DEFECT: authHeader parses localStorage without a try/catch, so a
    // truncated or hand-edited `user` entry throws a SyntaxError out of a
    // function whose documented contract is "return {} when unauthenticated".
    // Every caller that builds request headers dies with it.
    localStorage.setItem('user', '{"id":1,');
    expect(authHeader()).toEqual({});
  });
});

describe('route guards', () => {
  const child = <span data-testid='child'>secret</span>;

  it('AuthRedirect sends a signed-in visitor to the console', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    render(<AuthRedirect>{child}</AuthRedirect>);
    const nav = screen.getByTestId('navigate');
    expect(nav.getAttribute('data-to')).toBe('/console');
    expect(nav.getAttribute('data-replace')).toBe('true');
  });

  it('AuthRedirect renders the login page for an anonymous visitor', () => {
    render(<AuthRedirect>{child}</AuthRedirect>);
    expect(screen.getByTestId('child')).toBeTruthy();
  });

  it('PrivateRoute bounces an anonymous visitor to login, remembering where', () => {
    render(<PrivateRoute>{child}</PrivateRoute>);
    const nav = screen.getByTestId('navigate');
    expect(nav.getAttribute('data-to')).toBe('/login');
    expect(nav.getAttribute('data-has-from')).toBe('yes');
  });

  it('PrivateRoute admits any signed-in user', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 1 }));
    render(<PrivateRoute>{child}</PrivateRoute>);
    expect(screen.getByTestId('child')).toBeTruthy();
  });

  it('AdminRoute admits role 10 and above', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 10 }));
    render(<AdminRoute>{child}</AdminRoute>);
    expect(screen.getByTestId('child')).toBeTruthy();
  });

  it('AdminRoute sends a non-admin to /forbidden, not to /login', () => {
    localStorage.setItem('user', JSON.stringify({ id: 1, role: 1 }));
    render(<AdminRoute>{child}</AdminRoute>);
    expect(screen.getByTestId('navigate').getAttribute('data-to')).toBe(
      '/forbidden',
    );
  });

  it('AdminRoute sends an anonymous visitor to /login', () => {
    render(<AdminRoute>{child}</AdminRoute>);
    expect(screen.getByTestId('navigate').getAttribute('data-to')).toBe(
      '/login',
    );
  });

  it('AdminRoute rejects a non-numeric role rather than coercing it', () => {
    // A string "10" must NOT clear the gate: >= on mixed types is exactly how
    // privilege checks get bypassed.
    localStorage.setItem('user', JSON.stringify({ id: 1, role: '10' }));
    render(<AdminRoute>{child}</AdminRoute>);
    expect(screen.getByTestId('navigate').getAttribute('data-to')).toBe(
      '/forbidden',
    );
  });

  it('AdminRoute fails closed on a corrupted user shim', () => {
    localStorage.setItem('user', '{"role":10,');
    render(<AdminRoute>{child}</AdminRoute>);
    expect(screen.getByTestId('navigate').getAttribute('data-to')).toBe(
      '/forbidden',
    );
  });
});

// ── dashboard.jsx ───────────────────────────────────────────────────────
describe('dashboard time helpers', () => {
  it('getDefaultTime falls back to hourly granularity', () => {
    expect(getDefaultTime()).toBe('hour');
    localStorage.setItem('data_export_default_time', 'week');
    expect(getDefaultTime()).toBe('week');
  });

  it('getTimeInterval returns minutes by default and seconds on request', () => {
    expect(getTimeInterval('hour')).toBe(60);
    expect(getTimeInterval('hour', true)).toBe(3600);
    expect(getTimeInterval('day')).toBe(1440);
    expect(getTimeInterval('week', true)).toBe(604800);
  });

  it('getTimeInterval falls back to the hourly bucket for an unknown type', () => {
    expect(getTimeInterval('fortnight')).toBe(60);
    expect(getTimeInterval(undefined, true)).toBe(3600);
  });

  it('getInitialTimestamp looks back further for coarser granularities', () => {
    const NOW = new Date(2026, 5, 15, 12, 0, 0).getTime();
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    try {
      // hour -> 1 day back
      localStorage.setItem('data_export_default_time', 'hour');
      expect(getInitialTimestamp()).toBe('2026-06-14 12:00:00');
      // week -> 30 days back
      localStorage.setItem('data_export_default_time', 'week');
      expect(getInitialTimestamp()).toBe('2026-05-16 12:00:00');
      // anything else -> 7 days back
      localStorage.setItem('data_export_default_time', 'day');
      expect(getInitialTimestamp()).toBe('2026-06-08 12:00:00');
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('map accumulation helpers', () => {
  it('updateMapValue seeds a missing key at 0 before adding', () => {
    const m = new Map();
    updateMapValue(m, 'a', 5);
    updateMapValue(m, 'a', 3);
    expect(m.get('a')).toBe(8);
  });

  it('updateMapValue preserves an existing key', () => {
    const m = new Map([['a', 10]]);
    updateMapValue(m, 'a', -4);
    expect(m.get('a')).toBe(6);
  });

  it('initializeMaps seeds the key in every map but never overwrites', () => {
    const a = new Map([['k', 42]]);
    const b = new Map();
    initializeMaps('k', a, b);
    expect(a.get('k')).toBe(42);
    expect(b.get('k')).toBe(0);
  });
});

describe('chart spec helpers', () => {
  it('updateChartSpec merges data, subtitle and colours into the previous spec', () => {
    let spec = {
      type: 'bar',
      title: { text: 'Usage', subtext: 'old' },
      keep: 1,
    };
    updateChartSpec(
      (fn) => {
        spec = fn(spec);
      },
      [{ x: 1 }],
      'last 24h',
      ['#fff'],
      'series-1',
    );
    expect(spec.keep).toBe(1);
    expect(spec.type).toBe('bar');
    expect(spec.title).toEqual({ text: 'Usage', subtext: 'last 24h' });
    expect(spec.data).toEqual([{ id: 'series-1', values: [{ x: 1 }] }]);
    expect(spec.color).toEqual({ specified: ['#fff'] });
  });

  it('getTrendSpec indexes the series and hides both axes', () => {
    const spec = getTrendSpec([5, 7, 9], '#f00');
    expect(spec.data[0].values).toEqual([
      { x: 0, y: 5 },
      { x: 1, y: 7 },
      { x: 2, y: 9 },
    ]);
    expect(spec.type).toBe('line');
    expect(spec.axes.every((a) => a.visible === false)).toBe(true);
  });

  it('getTrendSpec yields an empty series for empty input', () => {
    expect(getTrendSpec([], '#f00').data[0].values).toEqual([]);
  });
});

describe('uptime status mapping', () => {
  const map = {
    1: { color: '#0f0', text: 'Up' },
    0: { color: '#f00', text: 'Down' },
  };
  const t = (k) => k;

  it('resolves a known status', () => {
    expect(getUptimeStatusColor(1, map)).toBe('#0f0');
    expect(getUptimeStatusText(0, map, t)).toBe('Down');
  });

  it('falls back to a neutral colour and an "unknown" label', () => {
    expect(getUptimeStatusColor(99, map)).toBe('#8b9aa7');
    expect(getUptimeStatusText(99, map, t)).toBe('未知');
  });
});

describe('dashboard actions', () => {
  it('handleCopyUrl confirms only after the copy succeeded', async () => {
    await handleCopyUrl('https://hub.example.test', (k) => k);
    expect(copy).toHaveBeenCalledWith('https://hub.example.test');
    expect(showSuccess).toHaveBeenCalledWith('复制成功');
  });

  it('handleCopyUrl stays silent when the copy failed', async () => {
    copy.mockResolvedValueOnce(false);
    await handleCopyUrl('https://hub.example.test', (k) => k);
    expect(showSuccess).not.toHaveBeenCalled();
  });

  it('handleSpeedTest opens a hardened tab with the url encoded', () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null);
    handleSpeedTest('https://hub.example.test/api?x=1');
    expect(open).toHaveBeenCalledWith(
      'https://www.tcptest.cn/http/https%3A%2F%2Fhub.example.test%2Fapi%3Fx%3D1',
      '_blank',
      'noopener,noreferrer',
    );
    open.mockRestore();
  });
});

describe('processRawData', () => {
  // Two hourly buckets, three rows, two distinct models.
  const h = (hour) =>
    Math.floor(new Date(2026, 5, 15, hour, 30).getTime() / 1000);
  const rows = [
    {
      created_at: h(9),
      model_name: 'gpt-4o',
      quota: 100,
      token_used: 10,
      count: 1,
    },
    {
      created_at: h(9),
      model_name: 'claude-3',
      quota: 250,
      token_used: 20,
      count: 2,
    },
    {
      created_at: h(10),
      model_name: 'gpt-4o',
      quota: 50,
      token_used: 5,
      count: 1,
    },
  ];

  it('totals quota, tokens and calls across every row', () => {
    const out = processRawData(rows, 'hour', initializeMaps, updateMapValue);
    expect(out.totalQuota).toBe(400);
    expect(out.totalTokens).toBe(35);
    expect(out.totalTimes).toBe(4);
    expect([...out.uniqueModels].sort()).toEqual(['claude-3', 'gpt-4o']);
  });

  it('buckets by time key and sorts the axis', () => {
    const out = processRawData(rows, 'hour', initializeMaps, updateMapValue);
    expect(out.timePoints).toEqual(['06-15 09:00', '06-15 10:00']);
    expect(out.timeQuotaMap.get('06-15 09:00')).toBe(350);
    expect(out.timeQuotaMap.get('06-15 10:00')).toBe(50);
    expect(out.timeCountMap.get('06-15 09:00')).toBe(3);
    expect(out.timeTokensMap.get('06-15 10:00')).toBe(5);
  });

  it('adds the year to the axis labels once the range crosses one', () => {
    const crossing = [
      {
        created_at: Math.floor(new Date(2025, 11, 31, 23, 0).getTime() / 1000),
        model_name: 'm',
        quota: 1,
        token_used: 1,
        count: 1,
      },
      {
        created_at: Math.floor(new Date(2026, 0, 1, 1, 0).getTime() / 1000),
        model_name: 'm',
        quota: 1,
        token_used: 1,
        count: 1,
      },
    ];
    const out = processRawData(
      crossing,
      'hour',
      initializeMaps,
      updateMapValue,
    );
    expect(out.timePoints).toEqual(['2025-12-31 23:00', '2026-01-01 01:00']);
  });

  it('returns zeroed totals for no data', () => {
    const out = processRawData([], 'hour', initializeMaps, updateMapValue);
    expect(out.totalQuota).toBe(0);
    expect(out.timePoints).toEqual([]);
    expect(out.uniqueModels.size).toBe(0);
  });
});

describe('calculateTrendData', () => {
  const timePoints = ['t1', 't2'];
  const quota = new Map([
    ['t1', 100],
    ['t2', 300],
  ]);
  const tokens = new Map([
    ['t1', 60],
    ['t2', 120],
  ]);
  const counts = new Map([
    ['t1', 120],
    ['t2', 240],
  ]);

  it('projects each map onto the shared time axis', () => {
    const out = calculateTrendData(timePoints, quota, tokens, counts, 'hour');
    expect(out.consumeQuota).toEqual([100, 300]);
    expect(out.tokens).toEqual([60, 120]);
    expect(out.times).toEqual([120, 240]);
  });

  it('derives per-minute rates from the bucket interval', () => {
    // hourly buckets are 60 minutes wide: 120 calls -> 2 rpm
    const out = calculateTrendData(timePoints, quota, tokens, counts, 'hour');
    expect(out.rpm).toEqual([2, 4]);
    expect(out.tpm).toEqual([1, 2]);
  });

  it('suppresses the rate series when there is only one bucket', () => {
    const out = calculateTrendData(['t1'], quota, tokens, counts, 'hour');
    expect(out.rpm).toEqual([]);
    expect(out.tpm).toEqual([]);
    expect(out.consumeQuota).toEqual([100]);
  });

  it('substitutes 0 for a bucket missing from a map', () => {
    const out = calculateTrendData(
      ['t1', 'gap'],
      quota,
      tokens,
      counts,
      'hour',
    );
    expect(out.consumeQuota).toEqual([100, 0]);
  });
});

describe('aggregateDataByTimeAndModel', () => {
  const h = (hour) =>
    Math.floor(new Date(2026, 5, 15, hour, 30).getTime() / 1000);

  it('sums quota and count per (time, model) pair', () => {
    const agg = aggregateDataByTimeAndModel(
      [
        { created_at: h(9), model_name: 'gpt-4o', quota: 100, count: 1 },
        { created_at: h(9), model_name: 'gpt-4o', quota: 50, count: 2 },
        { created_at: h(9), model_name: 'claude-3', quota: 7, count: 1 },
        { created_at: h(10), model_name: 'gpt-4o', quota: 1, count: 1 },
      ],
      'hour',
    );
    expect(agg.size).toBe(3);
    expect(agg.get('06-15 09:00-gpt-4o')).toEqual({
      time: '06-15 09:00',
      model: 'gpt-4o',
      quota: 150,
      count: 3,
    });
    expect(agg.get('06-15 09:00-claude-3').quota).toBe(7);
  });

  it('is empty for no rows', () => {
    expect(aggregateDataByTimeAndModel([], 'hour').size).toBe(0);
  });
});

describe('generateChartTimePoints', () => {
  const h = (hour) =>
    Math.floor(new Date(2026, 5, 15, hour, 30).getTime() / 1000);

  it('back-fills a 7-point axis ending at the latest sample when data is sparse', () => {
    const rows = [{ created_at: h(12), model_name: 'm', quota: 1, count: 1 }];
    const agg = aggregateDataByTimeAndModel(rows, 'hour');
    const points = generateChartTimePoints(agg, rows, 'hour');
    expect(points).toHaveLength(7);
    // 6 hourly steps back from 12:30 through 12:30 itself
    expect(points[0]).toBe('06-15 06:00');
    expect(points[6]).toBe('06-15 12:00');
  });

  it('keeps the observed axis once there are enough real buckets', () => {
    const rows = Array.from({ length: 8 }, (_, i) => ({
      created_at: h(i + 1),
      model_name: 'm',
      quota: 1,
      count: 1,
    }));
    const agg = aggregateDataByTimeAndModel(rows, 'hour');
    const points = generateChartTimePoints(agg, rows, 'hour');
    expect(points).toHaveLength(8);
    expect(points).toContain('06-15 01:00');
    expect(points).toContain('06-15 08:00');
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});
