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

import { useTenantSlug, readTenantSlug } from './useTenantSlug';
import { SELF_TENANT_SLUG } from '../../helpers/apiMode';

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('useTenantSlug', () => {
  it('is the self-tenant alias on the very first render, and never changes', () => {
    // Pages key their mount fetches on this value. Asserting result.current
    // would not catch a value that settles late (renderHook flushes effects
    // before it is read); recording what each render saw does.
    const seen = [];
    const { rerender } = renderHook(() => {
      const slug = useTenantSlug();
      seen.push(slug);
      return slug;
    });
    rerender();
    expect(new Set(seen)).toEqual(new Set([SELF_TENANT_SLUG]));
  });

  it('does not route by whatever slug a login stored', () => {
    // The stored slug was the routing value until 2026-09-22; a missing one
    // fell back to 'default' (a tenant id) and every panel 404'd, and the
    // root tenant switcher wrote another tenant's slug that the server then
    // refused with 403 TENANT_MISMATCH on every request.
    localStorage.setItem('tenant_slug', 'acme');
    const { result } = renderHook(() => useTenantSlug());
    expect(result.current).toBe(SELF_TENANT_SLUG);
  });

  it('routes the same with nothing stored and with storage denied', () => {
    expect(renderHook(() => useTenantSlug()).result.current).toBe(
      SELF_TENANT_SLUG,
    );
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('access denied');
    });
    expect(renderHook(() => useTenantSlug()).result.current).toBe(
      SELF_TENANT_SLUG,
    );
  });
});

describe('readTenantSlug (display only)', () => {
  it('returns the slug the last login reported', () => {
    localStorage.setItem('tenant_slug', 'acme');
    expect(readTenantSlug()).toBe('acme');
  });

  it("returns '' when none was reported or storage is denied", () => {
    expect(readTenantSlug()).toBe('');
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('access denied');
    });
    expect(readTenantSlug()).toBe('');
  });
});
