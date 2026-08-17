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
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  getTableCompactMode: () => false,
  setTableCompactMode: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useUsersData } from './useUsersData';
import { API, showError, showSuccess } from '../../helpers';

const user = (id, over = {}) => ({
  id,
  username: `user-${id}`,
  status: 1,
  role: 1,
  group: 'default',
  DeletedAt: null,
  ...over,
});

const page = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, page: 1, ...extra },
  },
});

const routeGet = (userPayload) => {
  API.get.mockImplementation((url) => {
    if (url.startsWith('/api/group/')) {
      return Promise.resolve({ data: { data: ['default', 'vip'] } });
    }
    return Promise.resolve(userPayload);
  });
};

const mount = async () => {
  const hook = renderHook(() => useUsersData());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

const withForm = (hook, values) =>
  act(() => {
    hook.result.current.setFormApi({ getValues: () => values });
  });

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  routeGet(page([user(1), user(2)]));
});

describe('useUsersData — listing', () => {
  it('loads users and stamps a table key from the id', async () => {
    const { result } = await mount();

    expect(API.get).toHaveBeenCalledWith('/api/user/?p=0&page_size=10');
    expect(result.current.users.map((u) => u.key)).toEqual([1, 2]);
    expect(result.current.userCount).toBe(2);
  });

  it('loads the group options alongside the first page', async () => {
    const { result } = await mount();

    await waitFor(() =>
      expect(result.current.groupOptions).toEqual([
        { label: 'default', value: 'default' },
        { label: 'vip', value: 'vip' },
      ]),
    );
  });

  it('reports a failed list without rendering rows', async () => {
    routeGet({ data: { success: false, message: 'user list denied' } });
    const { result } = await mount();

    expect(showError).toHaveBeenCalledWith('user list denied');
    expect(result.current.users).toEqual([]);
  });

  it('reports a group fetch failure but still shows the user table', async () => {
    API.get.mockImplementation((url) => {
      if (url.startsWith('/api/group/')) {
        return Promise.reject(new Error('groups unavailable'));
      }
      return Promise.resolve(page([user(1)]));
    });

    const { result } = await mount();

    await waitFor(() =>
      expect(showError).toHaveBeenCalledWith('groups unavailable'),
    );
    expect(result.current.users).toHaveLength(1);
    expect(result.current.groupOptions).toEqual([]);
  });

  it('bails out of the group fetch when the interceptor swallows the response', async () => {
    API.get.mockImplementation((url) => {
      if (url.startsWith('/api/group/')) return Promise.resolve(undefined);
      return Promise.resolve(page([user(1)]));
    });

    const { result } = await mount();

    expect(result.current.groupOptions).toEqual([]);
    expect(showError).not.toHaveBeenCalled();
  });
});

describe('useUsersData — search', () => {
  it('reads keyword and group off the form when called with two args', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'ali', searchGroup: 'vip' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.searchUsers(1, 10);
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/user/search?keyword=ali&group=vip&p=1&page_size=10',
    );
  });

  it('falls back to the plain list when both filters are blank', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '', searchGroup: '' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.searchUsers(2, 10);
    });

    expect(API.get).toHaveBeenCalledWith('/api/user/?p=2&page_size=10');
    expect(hook.result.current.searching).toBe(false);
  });

  it('searches on group alone when the keyword is empty', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '', searchGroup: 'vip' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.searchUsers(1, 10);
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/user/search?keyword=&group=vip&p=1&page_size=10',
    );
  });

  it('carries the page index into the search query', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'ali', searchGroup: '' });
    API.get.mockClear();

    await act(async () => {
      hook.result.current.handlePageChange(4);
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/user/search?keyword=ali&group=&p=4&page_size=10',
    );
    expect(hook.result.current.activePage).toBe(1); // server echo wins
  });

  it('reports a failed search and clears the searching flag', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'ali', searchGroup: '' });

    API.get.mockResolvedValue({
      data: { success: false, message: 'search denied' },
    });
    await act(async () => {
      await hook.result.current.searchUsers(1, 10);
    });

    expect(showError).toHaveBeenCalledWith('search denied');
    expect(hook.result.current.searching).toBe(false);
  });

  it('refresh routes to search or plain list depending on the filters', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '', searchGroup: '' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.refresh(2);
    });
    expect(API.get).toHaveBeenCalledWith('/api/user/?p=2&page_size=10');

    withForm(hook, { searchKeyword: 'ali', searchGroup: '' });
    API.get.mockClear();
    await act(async () => {
      await hook.result.current.refresh(2);
    });
    expect(API.get).toHaveBeenCalledWith(
      '/api/user/search?keyword=ali&group=&p=2&page_size=10',
    );
  });
});

describe('useUsersData — page size', () => {
  it('remembers the chosen page size in localStorage', async () => {
    const hook = await mount();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(window.localStorage.getItem('page-size')).toBe('50');
    expect(hook.result.current.pageSize).toBe(50);
  });

  // DEFECT (see report): handlePageSizeChange calls setActivePage(1) but then
  // fetches `activePage` — the value captured by the closure at render time.
  // On page 3 with a smaller total the new query can land past the end and
  // show an empty table. Un-skip once it fetches page 1 like it declares.
  it.skip('resets to page 1 when the page size changes', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: '', searchGroup: '' });

    // The server echoes the page it served, so the hook really is sitting on
    // page 3 when the operator changes rows-per-page.
    routeGet(page([user(1)], { page: 3 }));
    await act(async () => {
      hook.result.current.handlePageChange(3);
    });
    expect(hook.result.current.activePage).toBe(3);
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(API.get).toHaveBeenCalledWith('/api/user/?p=1&page_size=50');
  });

  // DEFECT (see report): unlike handlePageChange, handlePageSizeChange never
  // consults the filters — changing rows-per-page silently discards an active
  // search and dumps the unfiltered directory back on screen.
  it.skip('keeps an active search when the page size changes', async () => {
    const hook = await mount();
    withForm(hook, { searchKeyword: 'ali', searchGroup: '' });
    API.get.mockClear();

    await act(async () => {
      await hook.result.current.handlePageSizeChange(50);
    });

    expect(API.get.mock.calls[0][0]).toContain('/api/user/search');
  });
});

describe('useUsersData — manage', () => {
  it('posts the action and applies the status the server returns', async () => {
    const { result } = await mount();
    API.post.mockResolvedValue({
      data: { success: true, data: { status: 2, role: 1 } },
    });

    await act(async () => {
      await result.current.manageUser(1, 'disable', result.current.users[0]);
    });

    expect(API.post).toHaveBeenCalledWith('/api/user/manage', {
      id: 1,
      action: 'disable',
    });
    expect(showSuccess).toHaveBeenCalled();
    expect(result.current.users[0].status).toBe(2);
    expect(result.current.users[1].status).toBe(1); // untouched
    expect(result.current.loading).toBe(false);
  });

  it('applies a promotion to the role field', async () => {
    const { result } = await mount();
    API.post.mockResolvedValue({
      data: { success: true, data: { status: 1, role: 10 } },
    });

    await act(async () => {
      await result.current.manageUser(2, 'promote', result.current.users[1]);
    });

    expect(result.current.users[1].role).toBe(10);
    expect(result.current.users[0].role).toBe(1);
  });

  it('tombstones the row locally on delete instead of touching status', async () => {
    const { result } = await mount();
    API.post.mockResolvedValue({ data: { success: true, data: {} } });

    await act(async () => {
      await result.current.manageUser(1, 'delete', result.current.users[0]);
    });

    expect(result.current.users[0].DeletedAt).toBeInstanceOf(Date);
    expect(result.current.users[0].status).toBe(1);
  });

  it('replaces the row objects so the table re-renders', async () => {
    const { result } = await mount();
    const before = result.current.users[0];
    API.post.mockResolvedValue({
      data: { success: true, data: { status: 2, role: 1 } },
    });

    await act(async () => {
      await result.current.manageUser(1, 'disable', before);
    });

    expect(result.current.users[0]).not.toBe(before);
    expect(before.status).toBe(1); // the original object is left alone
  });

  it('reports a rejected action and leaves the rows alone', async () => {
    const { result } = await mount();
    API.post.mockResolvedValue({
      data: { success: false, message: 'cannot demote yourself' },
    });

    await act(async () => {
      await result.current.manageUser(1, 'demote', result.current.users[0]);
    });

    expect(showError).toHaveBeenCalledWith('cannot demote yourself');
    expect(result.current.users[0].role).toBe(1);
    expect(result.current.loading).toBe(false);
  });
});

describe('useUsersData — row styling and modals', () => {
  it('greys deleted and disabled rows only', async () => {
    const { result } = await mount();
    const grey = 'var(--semi-color-disabled-border)';

    expect(result.current.handleRow(user(1))).toEqual({});
    expect(
      result.current.handleRow(user(1, { status: 2 })).style.background,
    ).toBe(grey);
    expect(
      result.current.handleRow(user(1, { DeletedAt: '2026-01-01T00:00:00Z' }))
        .style.background,
    ).toBe(grey);
  });

  it('closeEditUser clears the draft synchronously', async () => {
    const { result } = await mount();

    act(() => {
      result.current.setEditingUser({ id: 3 });
      result.current.setShowEditUser(true);
    });
    expect(result.current.showEditUser).toBe(true);

    act(() => {
      result.current.closeEditUser();
    });

    expect(result.current.showEditUser).toBe(false);
    expect(result.current.editingUser).toEqual({ id: undefined });
  });

  it('closeAddUser only hides the add dialog', async () => {
    const { result } = await mount();

    act(() => {
      result.current.setShowAddUser(true);
      result.current.setEditingUser({ id: 5 });
    });

    act(() => {
      result.current.closeAddUser();
    });

    expect(result.current.showAddUser).toBe(false);
    expect(result.current.editingUser).toEqual({ id: 5 });
  });

  it('getFormValues normalises both filters to empty strings', async () => {
    const hook = await mount();
    expect(hook.result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchGroup: '',
    });

    withForm(hook, { searchGroup: 'vip' });
    expect(hook.result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchGroup: 'vip',
    });
  });
});
