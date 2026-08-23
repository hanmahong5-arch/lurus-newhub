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
import { renderHook } from '@testing-library/react';

import {
  useTenantSlug,
  readTenantSlug,
  DEFAULT_TENANT_SLUG,
} from './useTenantSlug';

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('useTenantSlug', () => {
  it('is already correct on the very first render, and never changes after', () => {
    // The whole point. Ten v2 pages each seeded state with the literal
    // 'default' and only read localStorage in an effect, so their first render
    // — and every fetch keyed on the slug — used a tenant the browser was
    // never given. Where no tenant is named 'default', TenantSlugGuard answers
    // that first request 404.
    //
    // Asserting result.current would NOT catch that: renderHook wraps in
    // act(), which flushes effects before the value is read, so a hook that
    // resolves late looks identical from the outside. Recording what each
    // render actually saw is what distinguishes them — reverting this hook to
    // a useEffect makes `seen` ['default', 'acme'] and this test red.
    localStorage.setItem('tenant_slug', 'acme');
    const seen = [];
    renderHook(() => {
      const slug = useTenantSlug();
      seen.push(slug);
      return slug;
    });
    expect(seen).toEqual(['acme']);
  });

  it('does not re-render when nothing about the slug changed', () => {
    // Fetch effects list the slug in their dependencies; a value that settles
    // late re-fires every one of them.
    localStorage.setItem('tenant_slug', 'acme');
    const seen = [];
    const { rerender } = renderHook(() => {
      const slug = useTenantSlug();
      seen.push(slug);
      return slug;
    });
    const afterMount = seen.length;
    rerender();
    expect(seen.slice(afterMount)).toEqual(['acme']);
  });

  it('falls back when nothing is stored', () => {
    expect(readTenantSlug()).toBe(DEFAULT_TENANT_SLUG);
    const { result } = renderHook(() => useTenantSlug());
    expect(result.current).toBe(DEFAULT_TENANT_SLUG);
  });

  it('falls back when localStorage throws, as it does in private mode', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('access denied');
    });
    expect(readTenantSlug()).toBe(DEFAULT_TENANT_SLUG);
  });

  it('treats an empty stored slug as absent', () => {
    // '' would otherwise build /api/v2//channels, which routes nowhere.
    localStorage.setItem('tenant_slug', '');
    expect(readTenantSlug()).toBe(DEFAULT_TENANT_SLUG);
  });
});
