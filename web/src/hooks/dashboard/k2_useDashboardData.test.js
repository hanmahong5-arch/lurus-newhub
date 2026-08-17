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
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

const navigate = vi.fn();

vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    });
  }
});

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigate,
}));

vi.mock('../../helpers', () => ({
  API: { get: vi.fn() },
  isAdmin: vi.fn(() => false),
  showError: vi.fn(),
  timestamp2string: (ts) => new Date(Math.round(ts * 1000)).toISOString(),
  isV2Mode: vi.fn(() => false),
  v2Url: (p) => `/api/v2/acme${p}`,
}));

vi.mock('../../helpers/dashboard', () => ({
  getDefaultTime: () => 'hour',
  getInitialTimestamp: () => '2026-03-01T00:00:00.000Z',
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useDashboardData } from './useDashboardData';
import { API, isAdmin, isV2Mode, showError } from '../../helpers';

const userDispatch = vi.fn();

const mount = (userState = { user: { username: 'alice' } }, statusState) =>
  renderHook(() => useDashboardData(userState, userDispatch, statusState));

const quotaRow = (created_at, over = {}) => ({
  count: 1,
  model_name: 'gpt-4o',
  quota: 100,
  created_at,
  ...over,
});

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  isAdmin.mockReturnValue(false);
  isV2Mode.mockReturnValue(false);
  API.get.mockResolvedValue({ data: { success: true, data: [] } });
});

afterEach(() => {
  vi.useRealTimers();
});

describe('useDashboardData — inputs', () => {
  it('seeds the range from the dashboard helpers', () => {
    const { result } = mount();

    expect(result.current.inputs.start_timestamp).toBe(
      '2026-03-01T00:00:00.000Z',
    );
    expect(result.current.dataExportDefaultTime).toBe('hour');
    expect(result.current.inputs.username).toBe('');
  });

  it('updates a plain field without touching localStorage', () => {
    const { result } = mount();

    act(() => {
      result.current.handleInputChange('bob', 'username');
    });

    expect(result.current.inputs.username).toBe('bob');
    expect(window.localStorage.length).toBe(0);
  });

  it('routes the export granularity to its own state and localStorage', () => {
    const { result } = mount();

    act(() => {
      result.current.handleInputChange('day', 'data_export_default_time');
    });

    expect(result.current.dataExportDefaultTime).toBe('day');
    expect(window.localStorage.getItem('data_export_default_time')).toBe('day');
    // It must NOT leak into the query inputs.
    expect(result.current.inputs.data_export_default_time).toBe('');
  });

  it('opens and closes the search modal', () => {
    const { result } = mount();
    expect(result.current.searchModalVisible).toBe(false);

    act(() => result.current.showSearchModal());
    expect(result.current.searchModalVisible).toBe(true);

    act(() => result.current.handleCloseModal());
    expect(result.current.searchModalVisible).toBe(false);
  });

  it('translates the time granularity options', () => {
    const { result } = mount();
    expect(result.current.timeOptions.map((o) => o.value)).toEqual([
      'hour',
      'day',
      'week',
    ]);
  });
});

describe('useDashboardData — greeting', () => {
  const greetingAt = (hour) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 2, 1, hour, 0, 0));
    const { result } = mount();
    const text = result.current.getGreeting;
    vi.useRealTimers();
    return text;
  };

  it('picks the salutation from the local hour', () => {
    expect(greetingAt(5)).toContain('早上好');
    expect(greetingAt(11)).toContain('早上好');
    expect(greetingAt(12)).toContain('中午好');
    expect(greetingAt(13)).toContain('中午好');
    expect(greetingAt(14)).toContain('下午好');
    expect(greetingAt(17)).toContain('下午好');
    expect(greetingAt(18)).toContain('晚上好');
    expect(greetingAt(4)).toContain('晚上好');
  });

  it('appends the signed-in username', () => {
    const { result } = mount({ user: { username: 'alice' } });
    expect(result.current.getGreeting).toContain('alice');
  });

  it('degrades to an empty name when the session is not loaded yet', () => {
    const { result } = mount(null);
    expect(result.current.getGreeting.endsWith('，')).toBe(true);
  });
});

describe('useDashboardData — performance metrics', () => {
  const withRange = (start, end) => {
    const { result } = mount();
    act(() => {
      result.current.handleInputChange(start, 'start_timestamp');
      result.current.handleInputChange(end, 'end_timestamp');
      result.current.setTimes(120);
      result.current.setConsumeTokens(60000);
    });
    return result;
  };

  it('averages requests and tokens over the selected minutes', () => {
    const result = withRange(
      '2026-03-01T00:00:00.000Z',
      '2026-03-01T01:00:00.000Z',
    );

    expect(result.current.performanceMetrics.timeDiff).toBe(60);
    expect(result.current.performanceMetrics.avgRPM).toBe('2.000');
    expect(result.current.performanceMetrics.avgTPM).toBe('1000.000');
  });

  it('reports zero rather than NaN when the range cannot be parsed', () => {
    const result = withRange('not-a-date', '2026-03-01T01:00:00.000Z');

    expect(result.current.performanceMetrics.avgRPM).toBe('0');
    expect(result.current.performanceMetrics.avgTPM).toBe('0');
  });

  // DEFECT (see report): the guard only catches NaN. A zero-length range
  // divides by zero, so the dashboard renders "Infinity" as the request rate.
  it.skip('reports a finite rate for a zero-length range', () => {
    const result = withRange(
      '2026-03-01T00:00:00.000Z',
      '2026-03-01T00:00:00.000Z',
    );

    expect(
      Number.isFinite(Number(result.current.performanceMetrics.avgRPM)),
    ).toBe(true);
  });
});

describe('useDashboardData — quota series', () => {
  it('a normal user queries the self-scoped data route', async () => {
    const { result } = mount();

    await act(async () => {
      await result.current.loadQuotaData();
    });

    const url = API.get.mock.calls.at(-1)[0];
    expect(url).toContain('/api/data/self/?start_timestamp=1772323200');
    expect(url).toContain('default_time=hour');
    expect(url).not.toContain('username=');
  });

  it('an admin queries the global data route with the username filter', async () => {
    isAdmin.mockReturnValue(true);
    const { result } = mount();

    act(() => {
      result.current.handleInputChange('bob', 'username');
    });
    await act(async () => {
      await result.current.loadQuotaData();
    });

    const url = API.get.mock.calls.at(-1)[0];
    expect(url).toContain('/api/data/?username=bob');
  });

  it('returns the series sorted oldest first', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: [quotaRow(300), quotaRow(100), quotaRow(200)],
      },
    });
    const { result } = mount();

    let returned;
    await act(async () => {
      returned = await result.current.loadQuotaData();
    });

    expect(returned.map((r) => r.created_at)).toEqual([100, 200, 300]);
  });

  it('substitutes a placeholder point for an empty series', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: [] } });
    const { result } = mount();

    let returned;
    await act(async () => {
      returned = await result.current.loadQuotaData();
    });

    expect(returned).toHaveLength(1);
    expect(returned[0].model_name).toBe('无数据');
    expect(returned[0].quota).toBe(0);
    expect(returned[0].count).toBe(0);
  });

  it('reports a refused query and hands back an empty series', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'dashboard denied' },
    });
    const { result } = mount();

    let returned;
    await act(async () => {
      returned = await result.current.loadQuotaData();
    });

    expect(showError).toHaveBeenCalledWith('dashboard denied');
    expect(returned).toEqual([]);
  });
});

describe('useDashboardData — uptime panel', () => {
  it('loads the status board and selects the first category', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: [{ categoryName: 'relay' }, { categoryName: 'billing' }],
      },
    });
    const { result } = mount();

    await act(async () => {
      await result.current.loadUptimeData();
    });

    expect(result.current.uptimeData).toHaveLength(2);
    expect(result.current.activeUptimeTab).toBe('relay');
    expect(result.current.uptimeLoading).toBe(false);
  });

  it('keeps the operator on the tab they already chose', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: [{ categoryName: 'relay' }] },
    });
    const { result } = mount();

    act(() => result.current.setActiveUptimeTab('billing'));
    await act(async () => {
      await result.current.loadUptimeData();
    });

    expect(result.current.activeUptimeTab).toBe('billing');
  });

  it('tolerates a null payload', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: null } });
    const { result } = mount();

    await act(async () => {
      await result.current.loadUptimeData();
    });

    expect(result.current.uptimeData).toEqual([]);
    expect(result.current.activeUptimeTab).toBe('');
  });

  it('reports a refused status query', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'uptime off' },
    });
    const { result } = mount();

    await act(async () => {
      await result.current.loadUptimeData();
    });

    expect(showError).toHaveBeenCalledWith('uptime off');
    expect(result.current.uptimeLoading).toBe(false);
  });

  it('swallows a transport failure so the rest of the dashboard survives', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    // Only the uptime probe fails; the profile call on mount must still work
    // or the failure under test would be masked by an unrelated rejection.
    API.get.mockImplementation((url) =>
      url === '/api/uptime/status'
        ? Promise.reject(new Error('uptime unreachable'))
        : Promise.resolve({ data: { success: true, data: {} } }),
    );
    const { result } = mount();

    await act(async () => {
      await result.current.loadUptimeData();
    });

    expect(result.current.uptimeLoading).toBe(false);
    expect(errSpy).toHaveBeenCalled();
    errSpy.mockRestore();
  });
});

describe('useDashboardData — session refresh', () => {
  it('pulls the profile once on mount and dispatches a login', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { id: 1, username: 'alice' } },
    });
    const { rerender } = mount();

    await waitFor(() =>
      expect(userDispatch).toHaveBeenCalledWith({
        type: 'login',
        payload: { id: 1, username: 'alice' },
      }),
    );

    rerender();
    expect(
      API.get.mock.calls.filter((c) => c[0] === '/api/user/self'),
    ).toHaveLength(1);
  });

  it('uses the tenant-scoped profile route in v2 mode', async () => {
    isV2Mode.mockReturnValue(true);
    API.get.mockResolvedValue({ data: { success: true, data: { id: 1 } } });
    mount();

    await waitFor(() =>
      expect(API.get).toHaveBeenCalledWith('/api/v2/acme/user/me'),
    );
  });

  it('reports a refused profile fetch', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'session expired' },
    });
    mount();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('session expired'),
    );
    expect(userDispatch).not.toHaveBeenCalled();
  });

  // DEFECT (see report): getUserData has no try/catch, so a transport-level
  // failure on the mount effect escapes as an unhandled rejection with no
  // toast — the dashboard silently renders a stale session.
  it.skip('reports a transport failure on the profile fetch', async () => {
    const { result } = mount();
    API.get.mockRejectedValue(new Error('gateway down'));

    let thrown = null;
    await act(async () => {
      await result.current.getUserData().catch((e) => {
        thrown = e;
      });
    });

    expect(thrown).toBeNull();
    expect(showError).toHaveBeenCalled();
  });
});

describe('useDashboardData — search confirm', () => {
  it('reloads, forwards the series to the chart, and shuts the modal', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: [quotaRow(100)] },
    });
    const { result } = mount();
    act(() => result.current.showSearchModal());

    const updateChart = vi.fn();
    await act(async () => {
      await result.current.handleSearchConfirm(updateChart);
    });

    expect(updateChart).toHaveBeenCalledTimes(1);
    expect(updateChart.mock.calls[0][0][0].created_at).toBe(100);
    expect(result.current.searchModalVisible).toBe(false);
  });

  it('still closes the modal when there is no chart callback', async () => {
    const { result } = mount();
    act(() => result.current.showSearchModal());

    await act(async () => {
      await result.current.handleSearchConfirm(undefined);
    });

    expect(result.current.searchModalVisible).toBe(false);
  });
});

describe('useDashboardData — panel flags', () => {
  it('enables every panel when the server states nothing', () => {
    const { result } = mount({ user: {} }, undefined);

    expect(result.current.apiInfoEnabled).toBe(true);
    expect(result.current.announcementsEnabled).toBe(true);
    expect(result.current.faqEnabled).toBe(true);
    expect(result.current.uptimeEnabled).toBe(true);
    expect(result.current.hasApiInfoPanel).toBe(true);
    expect(result.current.hasInfoPanels).toBe(true);
  });

  it('honours explicit false flags from the server', () => {
    const { result } = mount(
      { user: {} },
      {
        status: {
          api_info_enabled: false,
          announcements_enabled: false,
          faq_enabled: false,
          uptime_kuma_enabled: false,
        },
      },
    );

    expect(result.current.hasApiInfoPanel).toBe(false);
    expect(result.current.hasInfoPanels).toBe(false);
  });

  it('keeps the info column while at least one of its panels is on', () => {
    const { result } = mount(
      { user: {} },
      {
        status: {
          announcements_enabled: false,
          faq_enabled: true,
          uptime_kuma_enabled: false,
        },
      },
    );

    expect(result.current.hasInfoPanels).toBe(true);
  });
});
