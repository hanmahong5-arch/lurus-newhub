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
import { renderHook, waitFor, act } from '@testing-library/react';

vi.mock('../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
}));

import { API } from '../../helpers';
import { useModelDeploymentSettings } from './useModelDeploymentSettings';

const settingsOk = (enabled) => ({
  data: { success: true, data: { enabled } },
});

beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useModelDeploymentSettings – settings load', () => {
  it('exposes the enabled flag and drops loading once the fetch resolves', async () => {
    API.get.mockResolvedValue(settingsOk(true));
    API.post.mockResolvedValue({ data: { success: true } });

    const { result } = renderHook(() => useModelDeploymentSettings());
    expect(result.current.loading).toBe(true);

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.isIoNetEnabled).toBe(true);
    expect(result.current.settings['model_deployment.ionet.enabled']).toBe(
      true,
    );
    expect(API.get).toHaveBeenCalledWith('/api/deployments/settings');
  });

  it('treats a non-boolean enabled payload as disabled (strict === true)', async () => {
    // The backend flag is a real bool; a string 'true' must NOT enable the
    // integration, otherwise a config drift silently turns the feature on.
    API.get.mockResolvedValue(settingsOk('true'));

    const { result } = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.isIoNetEnabled).toBe(false);
    expect(API.post).not.toHaveBeenCalled();
  });

  it('clears loading and stays disabled when the request rejects', async () => {
    API.get.mockRejectedValue(new Error('boom'));

    const { result } = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.isIoNetEnabled).toBe(false);
    expect(result.current.connectionOk).toBeNull();
    expect(result.current.connectionError).toBeNull();
  });

  it('keeps the previous settings when the payload reports success=false', async () => {
    API.get.mockResolvedValue({ data: { success: false, message: 'nope' } });

    const { result } = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.isIoNetEnabled).toBe(false);
  });

  it('refresh() re-reads the settings endpoint', async () => {
    API.get.mockResolvedValue(settingsOk(false));
    const { result } = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(result.current.loading).toBe(false));

    API.get.mockResolvedValue(settingsOk(true));
    await act(async () => {
      await result.current.refresh();
    });

    expect(result.current.isIoNetEnabled).toBe(true);
    expect(API.get).toHaveBeenCalledTimes(2);
  });
});

describe('useModelDeploymentSettings – connection probe', () => {
  const mountDisabled = async () => {
    API.get.mockResolvedValue(settingsOk(false));
    const hook = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(hook.result.current.loading).toBe(false));
    return hook;
  };

  it('does not probe while the integration is disabled', async () => {
    const { result } = await mountDisabled();
    expect(API.post).not.toHaveBeenCalled();
    expect(result.current.connectionOk).toBeNull();
  });

  it('probes automatically once enabled and reports ok', async () => {
    API.get.mockResolvedValue(settingsOk(true));
    API.post.mockResolvedValue({ data: { success: true } });

    const { result } = renderHook(() => useModelDeploymentSettings());
    await waitFor(() => expect(result.current.connectionOk).toBe(true));

    expect(API.post).toHaveBeenCalledWith(
      '/api/deployments/settings/test-connection',
      {},
      { skipErrorHandler: true },
    );
    expect(result.current.connectionLoading).toBe(false);
    expect(result.current.connectionError).toBeNull();
  });

  it.each([
    ['API key expired yesterday', 'expired'],
    ['Invalid credentials', 'invalid'],
    ['unauthorized', 'invalid'],
    ['bad api key', 'invalid'],
    ['network unreachable', 'network'],
    ['request timeout', 'network'],
    ['upstream exploded', 'unknown'],
  ])('classifies %s as %s', async (message, type) => {
    const { result } = await mountDisabled();
    API.post.mockResolvedValue({ data: { success: false, message } });

    await act(async () => {
      await result.current.testConnection();
    });

    expect(result.current.connectionOk).toBe(false);
    expect(result.current.connectionError).toEqual({ type, message });
  });

  it('lets "expired" win over "invalid" when both words appear', async () => {
    const { result } = await mountDisabled();
    API.post.mockResolvedValue({
      data: { success: false, message: 'invalid token: expired' },
    });

    await act(async () => {
      await result.current.testConnection();
    });

    expect(result.current.connectionError.type).toBe('expired');
  });

  it('trims the message and falls back when the payload carries none', async () => {
    const { result } = await mountDisabled();
    API.post.mockResolvedValue({ data: { success: false } });

    await act(async () => {
      await result.current.testConnection();
    });

    expect(result.current.connectionError).toEqual({
      type: 'unknown',
      message: 'Connection failed',
    });
  });

  it('maps an axios ERR_NETWORK rejection to a fixed network error', async () => {
    const { result } = await mountDisabled();
    const err = new Error('Network Error');
    err.code = 'ERR_NETWORK';
    API.post.mockRejectedValue(err);

    await act(async () => {
      await result.current.testConnection();
    });

    expect(result.current.connectionError).toEqual({
      type: 'network',
      message: 'Network connection failed',
    });
    expect(result.current.connectionLoading).toBe(false);
  });

  it('classifies a rejected response body over the raw error message', async () => {
    const { result } = await mountDisabled();
    API.post.mockRejectedValue({
      message: 'Request failed with status code 401',
      response: { data: { message: 'api key expired' } },
    });

    await act(async () => {
      await result.current.testConnection();
    });

    expect(result.current.connectionError).toEqual({
      type: 'expired',
      message: 'api key expired',
    });
  });
});
