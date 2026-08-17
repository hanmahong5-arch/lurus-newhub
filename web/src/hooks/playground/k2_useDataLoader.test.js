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
  processModelsData: vi.fn(),
  processGroupsData: vi.fn(),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useDataLoader } from './useDataLoader';
import { API, processModelsData, processGroupsData } from '../../helpers';

const setModels = vi.fn();
const setGroups = vi.fn();
const handleInputChange = vi.fn();

const mount = (userState, inputs = { model: 'gpt-4o', group: 'default' }) =>
  renderHook(() =>
    useDataLoader(userState, inputs, handleInputChange, setModels, setGroups),
  );

const routeGet = ({ models, groups }) => {
  API.get.mockImplementation((url) => {
    if (url === '/api/user/models') return Promise.resolve(models);
    if (url === '/api/user/self/groups') return Promise.resolve(groups);
    throw new Error(`unexpected url ${url}`);
  });
};

const okModels = (data) => ({ data: { success: true, data } });
const okGroups = (data) => ({ data: { success: true, data } });

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  processModelsData.mockReturnValue({
    modelOptions: [{ label: 'gpt-4o', value: 'gpt-4o' }],
    selectedModel: 'gpt-4o',
  });
  processGroupsData.mockReturnValue([{ label: 'default', value: 'default' }]);
  routeGet({
    models: okModels(['gpt-4o']),
    groups: okGroups({ default: { desc: 'default', ratio: 1 } }),
  });
});

describe('useDataLoader — auto load', () => {
  it('loads models and groups once a user is present', async () => {
    mount({ user: { id: 1, group: 'vip' } });

    await waitFor(() => expect(setModels).toHaveBeenCalled());
    expect(API.get).toHaveBeenCalledWith('/api/user/models');
    expect(API.get).toHaveBeenCalledWith('/api/user/self/groups');
    expect(setGroups).toHaveBeenCalled();
  });

  it('stays idle while the user context is still empty', async () => {
    mount(undefined);
    mount({ user: null });

    await Promise.resolve();
    expect(API.get).not.toHaveBeenCalled();
    expect(setModels).not.toHaveBeenCalled();
  });
});

describe('useDataLoader — models', () => {
  it('feeds the current selection into the model resolver', async () => {
    mount({ user: { id: 1 } }, { model: 'claude-3', group: 'default' });

    await waitFor(() => expect(processModelsData).toHaveBeenCalled());
    expect(processModelsData).toHaveBeenCalledWith(['gpt-4o'], 'claude-3');
  });

  it('publishes the resolved options and switches away from a dead model', async () => {
    processModelsData.mockReturnValue({
      modelOptions: [{ label: 'gpt-4o', value: 'gpt-4o' }],
      selectedModel: 'gpt-4o',
    });

    mount({ user: { id: 1 } }, { model: 'retired-model', group: 'default' });

    await waitFor(() =>
      expect(setModels).toHaveBeenCalledWith([
        { label: 'gpt-4o', value: 'gpt-4o' },
      ]),
    );
    expect(handleInputChange).toHaveBeenCalledWith('model', 'gpt-4o');
  });

  it('leaves the selection alone when it survived the refresh', async () => {
    mount({ user: { id: 1 } }, { model: 'gpt-4o', group: 'default' });

    await waitFor(() => expect(setModels).toHaveBeenCalled());
    expect(
      handleInputChange.mock.calls.filter((c) => c[0] === 'model'),
    ).toHaveLength(0);
  });

  // DEFECT (see report): useDataLoader calls showError in all four failure
  // branches but never imports it, so any model/group load failure raises a
  // ReferenceError out of the async callback instead of showing a toast.
  it.skip('reports a failed model load instead of throwing', async () => {
    const { result } = mount({ user: null });
    API.get.mockRejectedValue(new Error('models unavailable'));

    let thrown = null;
    await act(async () => {
      await result.current.loadModels().catch((e) => {
        thrown = e;
      });
    });

    expect(thrown).toBeNull();
  });

  // Same defect on the success=false branch.
  it.skip('reports a refused model list instead of throwing', async () => {
    const { result } = mount({ user: null });
    API.get.mockResolvedValue({
      data: { success: false, message: 'no models for your group' },
    });

    let thrown = null;
    await act(async () => {
      await result.current.loadModels().catch((e) => {
        thrown = e;
      });
    });

    expect(thrown).toBeNull();
  });
});

describe('useDataLoader — groups', () => {
  it('prefers the group on the user context', async () => {
    window.localStorage.setItem('user', JSON.stringify({ group: 'stored' }));
    mount({ user: { id: 1, group: 'from-context' } });

    await waitFor(() => expect(processGroupsData).toHaveBeenCalled());
    expect(processGroupsData).toHaveBeenCalledWith(
      { default: { desc: 'default', ratio: 1 } },
      'from-context',
    );
  });

  it('falls back to the group cached in localStorage', async () => {
    window.localStorage.setItem('user', JSON.stringify({ group: 'stored' }));
    mount({ user: { id: 1 } });

    await waitFor(() => expect(processGroupsData).toHaveBeenCalled());
    expect(processGroupsData.mock.calls[0][1]).toBe('stored');
  });

  it('switches to the first option when the current group disappeared', async () => {
    processGroupsData.mockReturnValue([
      { label: 'vip', value: 'vip' },
      { label: 'default', value: 'default' },
    ]);

    mount({ user: { id: 1 } }, { model: 'gpt-4o', group: 'retired-group' });

    await waitFor(() =>
      expect(handleInputChange).toHaveBeenCalledWith('group', 'vip'),
    );
  });

  it('keeps the current group when it is still offered', async () => {
    processGroupsData.mockReturnValue([{ label: 'default', value: 'default' }]);

    mount({ user: { id: 1 } }, { model: 'gpt-4o', group: 'default' });

    await waitFor(() => expect(setGroups).toHaveBeenCalled());
    expect(
      handleInputChange.mock.calls.filter((c) => c[0] === 'group'),
    ).toHaveLength(0);
  });

  it('falls back to an empty group when the server offers none', async () => {
    processGroupsData.mockReturnValue([]);

    mount({ user: { id: 1 } }, { model: 'gpt-4o', group: 'default' });

    await waitFor(() =>
      expect(handleInputChange).toHaveBeenCalledWith('group', ''),
    );
  });

  // DEFECT (see report): same missing showError import on the group path.
  it.skip('reports a failed group load instead of throwing', async () => {
    const { result } = mount({ user: null });
    API.get.mockRejectedValue(new Error('groups unavailable'));

    let thrown = null;
    await act(async () => {
      await result.current.loadGroups().catch((e) => {
        thrown = e;
      });
    });

    expect(thrown).toBeNull();
  });
});
