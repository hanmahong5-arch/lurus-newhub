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

vi.mock('../../helpers', () => ({
  API: { get: vi.fn() },
}));

import { useUserPermissions } from './useUserPermissions';
import { API } from '../../helpers';

const ok = (permissions) => ({
  data: { success: true, data: { permissions } },
});

let logSpy;
let errorSpy;

beforeEach(() => {
  vi.clearAllMocks();
  logSpy = vi.spyOn(console, 'log').mockImplementation(() => {});
  errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  logSpy.mockRestore();
  errorSpy.mockRestore();
});

const mount = async (response) => {
  API.get.mockResolvedValue(response);
  const hook = renderHook(() => useUserPermissions());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

describe('useUserPermissions — loading', () => {
  it('fetches from the self endpoint and exposes the permissions blob', async () => {
    const perms = { sidebar_settings: true, sidebar_modules: {} };
    const { result } = await mount(ok(perms));

    expect(API.get).toHaveBeenCalledWith('/api/user/self');
    expect(result.current.permissions).toEqual(perms);
    expect(result.current.error).toBeNull();
  });

  it('surfaces the server message when the call reports failure', async () => {
    const { result } = await mount({
      data: { success: false, message: 'session expired' },
    });

    expect(result.current.permissions).toBeNull();
    expect(result.current.error).toBe('session expired');
  });

  it('falls back to a generic message when failure carries no message', async () => {
    const { result } = await mount({ data: { success: false } });
    expect(result.current.error).toBe('获取权限失败');
  });

  it('reports a network error and still clears loading', async () => {
    API.get.mockRejectedValue(new Error('ECONNREFUSED'));
    const { result } = renderHook(() => useUserPermissions());

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe('网络错误，请重试');
    expect(result.current.permissions).toBeNull();
  });

  it('clears a previous error when a retry succeeds', async () => {
    API.get.mockRejectedValueOnce(new Error('boom'));
    const { result } = renderHook(() => useUserPermissions());
    await waitFor(() => expect(result.current.error).toBe('网络错误，请重试'));

    API.get.mockResolvedValue(ok({ sidebar_settings: true }));
    await act(async () => {
      await result.current.loadPermissions();
    });

    expect(result.current.error).toBeNull();
    expect(result.current.permissions).toEqual({ sidebar_settings: true });
  });
});

describe('useUserPermissions — sidebar gates', () => {
  it('requires a literal true for the settings permission', async () => {
    const yes = await mount(ok({ sidebar_settings: true }));
    expect(yes.result.current.hasSidebarSettingsPermission()).toBe(true);

    const truthy = await mount(ok({ sidebar_settings: 'yes' }));
    expect(truthy.result.current.hasSidebarSettingsPermission()).toBe(false);

    const missing = await mount(ok({}));
    expect(missing.result.current.hasSidebarSettingsPermission()).toBe(false);
  });

  it('fails OPEN when the server sends no sidebar_modules map', async () => {
    // No map == no restrictions configured, so everything is allowed.
    const { result } = await mount(ok({ sidebar_settings: false }));

    expect(result.current.isSidebarSectionAllowed('admin')).toBe(true);
    expect(result.current.isSidebarModuleAllowed('admin', 'users')).toBe(true);
    expect(result.current.getAllowedSidebarSections()).toEqual([]);
    expect(result.current.getAllowedSidebarModules('admin')).toEqual([]);
  });

  it('blocks a section only when it is explicitly false', async () => {
    const { result } = await mount(
      ok({
        sidebar_modules: {
          admin: false,
          console: { users: true },
          chat: true,
        },
      }),
    );

    expect(result.current.isSidebarSectionAllowed('admin')).toBe(false);
    expect(result.current.isSidebarSectionAllowed('console')).toBe(true);
    expect(result.current.isSidebarSectionAllowed('chat')).toBe(true);
    // Unknown sections are not blocked.
    expect(result.current.isSidebarSectionAllowed('unknown')).toBe(true);
  });

  it('blocks a module when its own flag is false or its section is off', async () => {
    const { result } = await mount(
      ok({
        sidebar_modules: {
          admin: false,
          console: { users: true, billing: false },
        },
      }),
    );

    expect(result.current.isSidebarModuleAllowed('admin', 'anything')).toBe(
      false,
    );
    expect(result.current.isSidebarModuleAllowed('console', 'billing')).toBe(
      false,
    );
    expect(result.current.isSidebarModuleAllowed('console', 'users')).toBe(
      true,
    );
    // A module the server never mentioned defaults to allowed.
    expect(result.current.isSidebarModuleAllowed('console', 'ghost')).toBe(
      true,
    );
  });

  it('lists only the sections that are not switched off', async () => {
    const { result } = await mount(
      ok({
        sidebar_modules: {
          admin: false,
          console: { users: true },
          chat: true,
        },
      }),
    );

    expect(result.current.getAllowedSidebarSections()).toEqual([
      'console',
      'chat',
    ]);
  });

  it('lists only modules flagged true and hides the section enabled marker', async () => {
    const { result } = await mount(
      ok({
        sidebar_modules: {
          console: {
            enabled: true,
            users: true,
            billing: false,
            tokens: true,
            drafts: 'true',
          },
        },
      }),
    );

    expect(result.current.getAllowedSidebarModules('console')).toEqual([
      'users',
      'tokens',
    ]);
  });

  it('returns no modules for a disabled or non-object section', async () => {
    const { result } = await mount(
      ok({ sidebar_modules: { admin: false, chat: true } }),
    );

    expect(result.current.getAllowedSidebarModules('admin')).toEqual([]);
    // `chat: true` enables the section but declares no module map.
    expect(result.current.getAllowedSidebarModules('chat')).toEqual([]);
    expect(result.current.getAllowedSidebarModules('missing')).toEqual([]);
  });
});
