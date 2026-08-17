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
import { renderHook, act } from '@testing-library/react';

vi.mock('../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
}));

import { API, showError } from '../../helpers';
import { useDeploymentResources } from './useDeploymentResources';

const mount = () => renderHook(() => useDeploymentResources());

beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useDeploymentResources – fetchHardwareTypes normalisation', () => {
  it('coerces available_count and derives the availability flag from it', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          hardware_types: [
            { id: 'a100', available_count: '4' },
            { id: 'h100', available_count: 0 },
            // Unparseable count degrades to 0 rather than NaN.
            { id: 'l40', available_count: 'many' },
            // An explicit boolean wins over the derived value.
            { id: 'v100', available_count: 0, available: true },
          ],
        },
      },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchHardwareTypes();
    });

    expect(out.map((h) => [h.id, h.available_count, h.available])).toEqual([
      ['a100', 4, true],
      ['h100', 0, false],
      ['l40', 0, false],
      ['v100', 0, true],
    ]);
    expect(result.current.hardwareTypes).toHaveLength(4);
    // No total_available in the payload => sum of the normalised counts.
    expect(result.current.hardwareTotalAvailable).toBe(4);
  });

  it('prefers the server-provided total, including a legitimate zero', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          hardware_types: [{ id: 'a100', available_count: 4 }],
          total_available: 0,
        },
      },
    });

    const { result } = mount();
    await act(async () => {
      await result.current.fetchHardwareTypes();
    });

    expect(result.current.hardwareTotalAvailable).toBe(0);
  });

  it('falls back to the summed counts when the total is blank or unparseable', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          hardware_types: [
            { id: 'a100', available_count: 2 },
            { id: 'h100', available_count: 3 },
          ],
          total_available: '',
        },
      },
    });

    const { result } = mount();
    await act(async () => {
      await result.current.fetchHardwareTypes();
    });

    expect(result.current.hardwareTotalAvailable).toBe(5);
  });

  it('handles an empty data envelope without throwing', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: null } });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchHardwareTypes();
    });

    expect(out).toEqual([]);
    expect(result.current.hardwareTotalAvailable).toBe(0);
    expect(result.current.loadingHardware).toBe(false);
  });

  it('reports the server message and resets the total on success=false', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'provider down' },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchHardwareTypes();
    });

    expect(out).toEqual([]);
    expect(showError).toHaveBeenCalledWith('获取硬件类型失败: provider down');
    expect(result.current.hardwareTotalAvailable).toBe(0);
  });

  it('reports the thrown message and clears loading on rejection', async () => {
    API.get.mockRejectedValue(new Error('timeout'));

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchHardwareTypes();
    });

    expect(out).toEqual([]);
    expect(showError).toHaveBeenCalledWith('获取硬件类型失败: timeout');
    expect(result.current.loadingHardware).toBe(false);
  });
});

describe('useDeploymentResources – fetchLocations', () => {
  it('short-circuits without a request when no hardware is selected', async () => {
    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchLocations('');
    });

    expect(out).toEqual([]);
    expect(API.get).not.toHaveBeenCalled();
    expect(result.current.locationsTotalAvailable).toBe(0);
  });

  it('deduplicates replicas by location id, keeping the first occurrence', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          replicas: [
            {
              location_id: 5,
              location_name: 'Frankfurt',
              iso2: 'de',
              available_count: 2,
            },
            // Same location, second shard: must not create a second entry and
            // must not add its count to the total.
            {
              location_id: '5',
              location_name: 'Frankfurt-2',
              available_count: 7,
            },
            {
              location: { id: 9, name: 'Tokyo', iso2: 'jp' },
              available_count: 3,
            },
            // No usable id at all => dropped.
            { available_count: 100 },
          ],
        },
      },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchLocations('a100', 2);
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/deployments/available-replicas?hardware_id=a100&gpu_count=2',
    );
    expect(out.map((l) => [l.id, l.name, l.iso2, l.available])).toEqual([
      [5, 'Frankfurt', 'DE', 2],
      [9, 'Tokyo', 'JP', 3],
    ]);
    expect(result.current.locationsTotalAvailable).toBe(5);
  });

  it('falls back to the id as the name and blanks a missing iso2', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: { replicas: [{ location_id: 'edge-1', available_count: 'x' }] },
      },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchLocations('a100');
    });

    expect(out[0]).toMatchObject({
      id: 'edge-1',
      name: 'edge-1',
      iso2: '',
      // Number('x') is NaN, which the `|| 0` guard turns into 0.
      available: 0,
    });
    expect(result.current.locationsTotalAvailable).toBe(0);
  });

  it('defaults gpu_count to 1 and reports a failed payload', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'no stock' },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchLocations('a100');
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/deployments/available-replicas?hardware_id=a100&gpu_count=1',
    );
    expect(out).toEqual([]);
    expect(showError).toHaveBeenCalledWith('获取部署位置失败: no stock');
  });
});

describe('useDeploymentResources – fetchAvailableReplicas', () => {
  it('stores the raw replica list on success', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { replicas: [{ id: 1 }, { id: 2 }] } },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.fetchAvailableReplicas('a100', 4);
    });

    expect(out).toEqual([{ id: 1 }, { id: 2 }]);
    expect(result.current.availableReplicas).toHaveLength(2);
    expect(result.current.loadingReplicas).toBe(false);
  });

  it('clears the list when the request rejects', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: { replicas: [{ id: 1 }] } },
    });
    const { result } = mount();
    await act(async () => {
      await result.current.fetchAvailableReplicas('a100');
    });
    expect(result.current.availableReplicas).toHaveLength(1);

    API.get.mockRejectedValueOnce(new Error('down'));
    let out;
    await act(async () => {
      out = await result.current.fetchAvailableReplicas('a100');
    });

    expect(out).toEqual([]);
    expect(result.current.availableReplicas).toEqual([]);
  });

  it('clearAvailableReplicas empties the list', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { replicas: [{ id: 1 }] } },
    });
    const { result } = mount();
    await act(async () => {
      await result.current.fetchAvailableReplicas('a100');
    });

    act(() => {
      result.current.clearAvailableReplicas();
    });
    expect(result.current.availableReplicas).toEqual([]);
  });
});

describe('useDeploymentResources – calculatePrice', () => {
  const full = {
    locationIds: [1],
    hardwareId: 'a100',
    gpusPerContainer: 2,
    durationHours: 5,
    replicaCount: 3,
  };

  it.each([
    ['no locations', { locationIds: [] }],
    ['no hardware', { hardwareId: '' }],
    ['zero gpus', { gpusPerContainer: 0 }],
    ['zero duration', { durationHours: 0 }],
    ['zero replicas', { replicaCount: 0 }],
  ])('skips the request when there are %s', async (_label, patch) => {
    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.calculatePrice({ ...full, ...patch });
    });

    expect(out).toBeNull();
    expect(API.post).not.toHaveBeenCalled();
    expect(result.current.priceEstimation).toBeNull();
  });

  it('maps the camelCase params onto the snake_case request body', async () => {
    API.post.mockResolvedValue({
      data: { success: true, data: { total: 12.5 } },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.calculatePrice(full);
    });

    expect(API.post).toHaveBeenCalledWith('/api/deployments/price-estimation', {
      location_ids: [1],
      hardware_id: 'a100',
      gpus_per_container: 2,
      duration_hours: 5,
      replica_count: 3,
    });
    expect(out).toEqual({ total: 12.5 });
    expect(result.current.priceEstimation).toEqual({ total: 12.5 });
    expect(result.current.loadingPrice).toBe(false);
  });

  it('drops a stale estimation when the recalculation fails', async () => {
    API.post.mockResolvedValueOnce({
      data: { success: true, data: { total: 12.5 } },
    });
    const { result } = mount();
    await act(async () => {
      await result.current.calculatePrice(full);
    });
    expect(result.current.priceEstimation).toEqual({ total: 12.5 });

    API.post.mockResolvedValueOnce({
      data: { success: false, message: 'price unavailable' },
    });
    await act(async () => {
      await result.current.calculatePrice(full);
    });

    expect(showError).toHaveBeenCalledWith('价格计算失败: price unavailable');
    expect(result.current.priceEstimation).toBeNull();
  });

  it('clearPriceEstimation resets the estimate', async () => {
    API.post.mockResolvedValue({
      data: { success: true, data: { total: 1 } },
    });
    const { result } = mount();
    await act(async () => {
      await result.current.calculatePrice(full);
    });

    act(() => {
      result.current.clearPriceEstimation();
    });
    expect(result.current.priceEstimation).toBeNull();
  });
});

describe('useDeploymentResources – name check and creation', () => {
  it('rejects a blank name locally without hitting the API', async () => {
    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.checkClusterNameAvailability('   ');
    });

    expect(out).toBe(false);
    expect(API.get).not.toHaveBeenCalled();
  });

  it('trims and URL-encodes the candidate name', async () => {
    API.get.mockResolvedValue({
      data: { success: true, data: { available: true } },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.checkClusterNameAvailability('  my cluster ');
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/deployments/check-name?name=my%20cluster',
    );
    expect(out).toBe(true);
  });

  it('treats a failed availability check as unavailable', async () => {
    API.get.mockResolvedValue({
      data: { success: false, message: 'lookup failed' },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.checkClusterNameAvailability('x');
    });

    expect(out).toBe(false);
    expect(showError).toHaveBeenCalledWith('检查名称可用性失败: lookup failed');
  });

  it('createDeployment returns the payload on success', async () => {
    API.post.mockResolvedValue({
      data: { success: true, data: { id: 'dep-1' } },
    });

    const { result } = mount();
    let out;
    await act(async () => {
      out = await result.current.createDeployment({ name: 'x' });
    });

    expect(API.post).toHaveBeenCalledWith('/api/deployments', { name: 'x' });
    expect(out).toEqual({ id: 'dep-1' });
  });

  it('createDeployment throws the server message so the caller can react', async () => {
    API.post.mockResolvedValue({
      data: { success: false, message: 'quota exceeded' },
    });

    const { result } = mount();
    await act(async () => {
      await expect(result.current.createDeployment({})).rejects.toThrow(
        'quota exceeded',
      );
    });
    // Creation failures are surfaced by the caller, not toasted here.
    expect(showError).not.toHaveBeenCalled();
  });
});
