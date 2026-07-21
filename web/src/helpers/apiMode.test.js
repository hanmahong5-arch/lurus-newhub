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
import { describe, it, expect, afterEach } from 'vitest';

import {
  isV2Mode,
  getTenantSlug,
  setTenantSlug,
  clearTenantSlug,
  v2Url,
} from './apiMode';

// These helpers persist their one piece of state in jsdom's localStorage
// (not network/timers), so behavior is fully deterministic per test as long
// as we clear it between cases.
describe('tenant slug helpers (apiMode)', () => {
  afterEach(() => clearTenantSlug());

  it('isV2Mode is false with no tenant slug set', () => {
    clearTenantSlug();
    expect(isV2Mode()).toBe(false);
  });

  it('getTenantSlug returns an empty string when unset', () => {
    clearTenantSlug();
    expect(getTenantSlug()).toBe('');
  });

  it('setTenantSlug persists the slug and flips isV2Mode to true', () => {
    setTenantSlug('acme');
    expect(getTenantSlug()).toBe('acme');
    expect(isV2Mode()).toBe(true);
  });

  it('setTenantSlug ignores a falsy value (does not clear or overwrite)', () => {
    setTenantSlug('acme');
    setTenantSlug('');
    expect(getTenantSlug()).toBe('acme');
    setTenantSlug(null);
    expect(getTenantSlug()).toBe('acme');
  });

  it('clearTenantSlug removes the slug and flips isV2Mode back to false', () => {
    setTenantSlug('acme');
    clearTenantSlug();
    expect(getTenantSlug()).toBe('');
    expect(isV2Mode()).toBe(false);
  });
});

describe('v2Url', () => {
  afterEach(() => clearTenantSlug());

  it('builds the path using the configured tenant slug', () => {
    setTenantSlug('acme');
    expect(v2Url('/tokens?p=1&size=10')).toBe(
      '/api/v2/acme/tokens?p=1&size=10',
    );
  });

  it('falls back to the "lurus" default tenant when no slug is set', () => {
    clearTenantSlug();
    expect(v2Url('/tenants')).toBe('/api/v2/lurus/tenants');
  });

  it('appends an empty path segment unchanged', () => {
    setTenantSlug('acme');
    expect(v2Url('')).toBe('/api/v2/acme');
  });
});
