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
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../helpers', () => ({
  API: { get: vi.fn() },
}));

import { API } from '../../helpers';
import {
  useRoutableModels,
  routableFor,
  firstRoutableModel,
  defaultCompareModels,
  intersectTokenLimits,
  WIRE_OPENAI,
  WIRE_ANTHROPIC,
} from './useRoutableModels';

beforeEach(() => {
  API.get.mockReset();
});

describe('useRoutableModels', () => {
  it('parses the {items} wire shape from /models/routable', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          items: [
            {
              id: 'rt-alpha',
              owned_by: 'custom',
              supported_endpoint_types: ['openai'],
            },
          ],
        },
      },
    });

    const { result } = renderHook(() => useRoutableModels('acme'));

    await waitFor(() => expect(result.current.loading).toBe(false));

    expect(result.current.items).toEqual([
      {
        id: 'rt-alpha',
        owned_by: 'custom',
        supported_endpoint_types: ['openai'],
      },
    ]);
    expect(result.current.error).toBeNull();
    expect(result.current.resolved).toBe(true);
    expect(API.get).toHaveBeenCalledWith(
      '/api/v2/acme/models/routable',
      expect.objectContaining({ skipErrorHandler: false }),
    );
  });

  // resolved must distinguish "have not asked yet / still in flight" from
  // "asked, and the truthful answer is zero" — a page that renders an empty
  // state before the first response would flash it for every real customer
  // on every page load.
  it('resolved stays false until the first response lands, then true even for an empty list', async () => {
    let resolveFetch;
    API.get.mockImplementationOnce(
      () =>
        new Promise((r) => {
          resolveFetch = r;
        }),
    );

    const { result } = renderHook(() => useRoutableModels('acme'));

    expect(result.current.resolved).toBe(false);

    resolveFetch({ data: { success: true, data: { items: [] } } });

    await waitFor(() => expect(result.current.resolved).toBe(true));
    expect(result.current.items).toEqual([]);
  });

  it('success:false yields items=[] and a truthy error, resolved:true', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: false, message: 'tenant not found' },
    });

    const { result } = renderHook(() => useRoutableModels('acme'));

    await waitFor(() => expect(result.current.resolved).toBe(true));
    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeTruthy();
  });

  it('a rejected request yields items=[] and error set without throwing', async () => {
    API.get.mockRejectedValueOnce(new Error('network down'));

    const { result } = renderHook(() => useRoutableModels('acme'));

    await waitFor(() => expect(result.current.resolved).toBe(true));
    expect(result.current.items).toEqual([]);
    expect(result.current.error).toBeTruthy();
  });

  it('does not call API.get when tenantSlug is empty', async () => {
    renderHook(() => useRoutableModels(''));
    await new Promise((r) => setTimeout(r, 0));
    expect(API.get).not.toHaveBeenCalled();
  });

  it('enabled:false does not fetch until flipped on', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: { items: [] } } });
    const { rerender } = renderHook(
      ({ enabled }) => useRoutableModels('acme', { enabled }),
      { initialProps: { enabled: false } },
    );
    expect(API.get).not.toHaveBeenCalled();
    rerender({ enabled: true });
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
  });

  it('refetch re-issues exactly one additional request', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: { items: [] } } });
    const { result } = renderHook(() => useRoutableModels('acme'));
    await waitFor(() => expect(result.current.resolved).toBe(true));
    expect(API.get).toHaveBeenCalledTimes(1);
    act(() => {
      result.current.refetch();
    });
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(2));
  });

  it('skips the global error handler when the caller passes skipErrorHandler:true', async () => {
    API.get.mockResolvedValueOnce({
      data: { success: true, data: { items: [] } },
    });
    renderHook(() => useRoutableModels('acme', { skipErrorHandler: true }));
    await waitFor(() => expect(API.get).toHaveBeenCalledTimes(1));
    expect(API.get).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ skipErrorHandler: true }),
    );
  });
});

describe('routableFor', () => {
  const items = [
    { id: 'rt-alpha', supported_endpoint_types: ['openai'] },
    { id: 'rt-anthropic', supported_endpoint_types: ['anthropic', 'openai'] },
    { id: 'rt-no-wire', supported_endpoint_types: [] },
  ];

  it('filters to models that carry the given wire', () => {
    expect(routableFor(items, WIRE_ANTHROPIC).map((m) => m.id)).toEqual([
      'rt-anthropic',
    ]);
  });

  it('returns every item when no wire is given', () => {
    expect(routableFor(items, undefined)).toEqual(items);
  });

  it('a non-array input yields []', () => {
    expect(routableFor(null, WIRE_OPENAI)).toEqual([]);
  });
});

describe('firstRoutableModel', () => {
  it('returns the first model matching the wire, or null', () => {
    const items = [
      { id: 'rt-alpha', supported_endpoint_types: ['openai'] },
      { id: 'rt-anthropic', supported_endpoint_types: ['anthropic'] },
    ];
    expect(firstRoutableModel(items, WIRE_OPENAI).id).toBe('rt-alpha');
    expect(firstRoutableModel(items, WIRE_ANTHROPIC).id).toBe('rt-anthropic');
    expect(firstRoutableModel([], WIRE_OPENAI)).toBeNull();
  });
});

describe('defaultCompareModels', () => {
  it('takes the first N (default 3) items', () => {
    const items = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }];
    expect(defaultCompareModels(items).map((m) => m.id)).toEqual([
      'a',
      'b',
      'c',
    ]);
    expect(defaultCompareModels(items, 2).map((m) => m.id)).toEqual(['a', 'b']);
  });
});

describe('intersectTokenLimits', () => {
  const items = [{ id: 'rt-alpha' }, { id: 'rt-beta' }, { id: 'rt-gamma' }];

  it('returns every item unchanged when the token has no model_limits', () => {
    expect(intersectTokenLimits(items, { model_limits_enabled: false })).toBe(
      items,
    );
    expect(intersectTokenLimits(items, null)).toBe(items);
  });

  it('narrows to the CSV model_limits when enabled', () => {
    const token = {
      model_limits_enabled: true,
      model_limits: 'rt-alpha, rt-gamma',
    };
    expect(intersectTokenLimits(items, token).map((m) => m.id)).toEqual([
      'rt-alpha',
      'rt-gamma',
    ]);
  });
});
