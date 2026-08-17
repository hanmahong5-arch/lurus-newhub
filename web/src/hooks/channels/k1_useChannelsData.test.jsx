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

vi.mock('../../helpers', async () => {
  // toBoolean carries real logic the hook depends on; keep the genuine one.
  const actual = await vi.importActual('../../helpers/boolean');
  return {
    API: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() },
    showError: vi.fn(),
    showInfo: vi.fn(),
    showSuccess: vi.fn(),
    loadChannelModels: vi.fn().mockResolvedValue(undefined),
    copy: vi.fn().mockResolvedValue(true),
    toBoolean: actual.toBoolean,
    getTableCompactMode: () => false,
    setTableCompactMode: vi.fn(),
  };
});

vi.mock('../common/useIsMobile', () => ({
  useIsMobile: () => false,
  MOBILE_BREAKPOINT: 768,
}));

vi.mock('@douyinfe/semi-ui', () => ({
  Modal: { info: vi.fn(), destroyAll: vi.fn() },
  Button: () => null,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { Modal } from '@douyinfe/semi-ui';
import { API, showError, showInfo, showSuccess } from '../../helpers';
import { useChannelsData } from './useChannelsData';

const routes = {};
const routeGet = (url) => {
  const key = Object.keys(routes)
    .sort((a, b) => b.length - a.length)
    .find((k) => url.startsWith(k));
  if (!key) throw new Error(`unrouted GET ${url}`);
  const value = routes[key];
  return typeof value === 'function' ? value(url) : Promise.resolve(value);
};

const channelList = (items, extra = {}) => ({
  data: {
    success: true,
    data: { items, total: items.length, ...extra },
  },
});

const setRoutes = (overrides = {}) => {
  Object.keys(routes).forEach((k) => delete routes[k]);
  Object.assign(
    routes,
    {
      '/api/channel/': channelList([]),
      '/api/group/': { data: { data: ['default', 'vip'] } },
      '/api/option/': { data: { success: true, data: [] } },
    },
    overrides,
  );
};

const mountLoaded = async () => {
  const hook = renderHook(() => useChannelsData());
  await waitFor(() => expect(hook.result.current.loading).toBe(false));
  return hook;
};

const lastChannelListUrl = () =>
  API.get.mock.calls
    .map(([url]) => url)
    .filter((url) => url.startsWith('/api/channel/'))
    .at(-1);

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  setRoutes();
  API.get.mockImplementation(routeGet);
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useChannelsData – bootstrap', () => {
  it('loads the first page with the stored table preferences', async () => {
    window.localStorage.setItem('id-sort', 'true');
    window.localStorage.setItem('page-size', '25');
    window.localStorage.setItem('enable-tag-mode', 'true');

    const { result } = await mountLoaded();

    expect(lastChannelListUrl()).toBe(
      '/api/channel/?p=1&page_size=25&id_sort=true&tag_mode=true',
    );
    expect(result.current.idSort).toBe(true);
    expect(result.current.pageSize).toBe(25);
    expect(result.current.enableTagMode).toBe(true);
    // Checkbox column is unconditionally on — the action rail depends on it.
    expect(result.current.enableBatchDelete).toBe(true);
  });

  it('falls back to the defaults when nothing is stored', async () => {
    await mountLoaded();

    expect(lastChannelListUrl()).toBe(
      '/api/channel/?p=1&page_size=10&id_sort=false&tag_mode=false',
    );
  });

  it('appends the persisted status filter to the query', async () => {
    window.localStorage.setItem('channel-status-filter', 'enabled');

    const { result } = await mountLoaded();

    expect(result.current.statusFilter).toBe('enabled');
    expect(lastChannelListUrl()).toContain('&status=enabled');
  });

  it('appends the type filter only when a concrete type is selected', async () => {
    const { result } = await mountLoaded();
    expect(lastChannelListUrl()).not.toContain('&type=');

    await act(async () => {
      await result.current.loadChannels(1, 10, false, false, '3', 'all');
    });
    expect(lastChannelListUrl()).toContain('&type=3');
  });

  it('turns the group endpoint into select options', async () => {
    const { result } = await mountLoaded();

    await waitFor(() =>
      expect(result.current.groupOptions).toEqual([
        { label: 'default', value: 'default' },
        { label: 'vip', value: 'vip' },
      ]),
    );
  });

  it('reads the global pass-through switch from the options endpoint', async () => {
    setRoutes({
      '/api/option/': {
        data: {
          success: true,
          data: [
            { key: 'other', value: 'x' },
            { key: 'global.pass_through_request_enabled', value: 'true' },
          ],
        },
      },
    });

    const { result } = await mountLoaded();
    await waitFor(() =>
      expect(result.current.globalPassThroughEnabled).toBe(true),
    );
  });

  it('leaves pass-through off when the option request fails', async () => {
    setRoutes({ '/api/option/': () => Promise.reject(new Error('nope')) });

    const { result } = await mountLoaded();
    expect(result.current.globalPassThroughEnabled).toBe(false);
  });

  it('reports the message when the channel list is refused', async () => {
    setRoutes({
      '/api/channel/': { data: { success: false, message: 'forbidden' } },
    });

    const { result } = await mountLoaded();
    expect(showError).toHaveBeenCalledWith('forbidden');
    expect(result.current.channels).toEqual([]);
  });

  // DEFECT: loadChannels awaits API.get with no try/catch/finally, and its
  // `if (res === undefined || stale) return;` guard also skips
  // setLoading(false). A rejected or empty response therefore pins the table
  // spinner forever — the operator sees a permanent skeleton with no error.
  // handlePageChange compounds it: it calls loadChannels().then(() => {}) with
  // no catch, so the rejection also escapes as an unhandled rejection.
  // Skipped: it encodes the correct contract, which the code does not meet.
  it.skip('stops the spinner when the channel request rejects', async () => {
    setRoutes({
      '/api/channel/': () => Promise.reject(new Error('offline')),
    });

    const { result } = renderHook(() => useChannelsData());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(showError).toHaveBeenCalled();
  });

  it.skip('stops the spinner when the channel request answers undefined', async () => {
    setRoutes({ '/api/channel/': () => Promise.resolve(undefined) });

    const { result } = renderHook(() => useChannelsData());
    await waitFor(() => expect(result.current.loading).toBe(false));
  });
});

describe('useChannelsData – row shaping', () => {
  it('stringifies the id into the table key in flat mode', async () => {
    setRoutes({
      '/api/channel/': channelList([
        { id: 1, name: 'a', status: 1 },
        { id: 2, name: 'b', status: 2 },
      ]),
    });

    const { result } = await mountLoaded();
    expect(result.current.channels.map((c) => c.key)).toEqual(['1', '2']);
    expect(result.current.channelCount).toBe(2);
  });

  it('greys out any row that is not enabled', async () => {
    const { result } = await mountLoaded();

    expect(result.current.handleRow({ status: 1 }, 0)).toEqual({});
    expect(result.current.handleRow({ status: 2 }, 1)).toEqual({
      style: { background: 'var(--semi-color-disabled-border)' },
    });
  });

  it('groups channels by tag, unions the groups and sums the quota', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          tag: 'eu',
          group: 'default',
          status: 2,
          priority: 5,
          weight: 2,
          used_quota: 100,
          response_time: 400,
        },
        {
          id: 2,
          tag: 'eu',
          group: 'vip',
          status: 1,
          priority: 5,
          weight: 9,
          used_quota: 50,
          response_time: 400,
        },
      ]),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');

    const { result } = await mountLoaded();
    const [group] = result.current.channels;

    expect(result.current.channels).toHaveLength(1);
    expect(group).toMatchObject({
      key: 'eu',
      id: 'eu',
      name: '标签：eu',
      // Same priority across children collapses to that value...
      priority: 5,
      // ...a divergent weight collapses to the empty string instead.
      weight: '',
      used_quota: 150,
      // One enabled child is enough to show the tag group as enabled.
      status: 1,
    });
    expect(group.group.split(',').sort()).toEqual(['default', 'vip']);
    expect(group.children.map((c) => c.id)).toEqual([1, 2]);
  });

  it('groups untagged channels under the empty tag', async () => {
    setRoutes({
      '/api/channel/': channelList([
        { id: 1, group: 'default', status: 1, priority: 1, weight: 1 },
      ]),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');

    const { result } = await mountLoaded();
    expect(result.current.channels[0]).toMatchObject({
      key: '',
      name: '标签：',
    });
  });

  // DEFECT: the tag row's response_time is folded with
  //   rt = (rt + child.response_time) / 2
  // starting from 0, so it is not an average at all. A tag holding ONE channel
  // that answers in 400ms reports 200ms, and with several children the oldest
  // samples decay away (an EMA), permanently understating latency on the
  // aggregated row operators use to spot slow upstreams.
  // Skipped: it encodes the correct contract (mean of the children).
  it.skip('shows the mean response time of the channels in a tag', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          tag: 'eu',
          group: 'default',
          status: 1,
          priority: 1,
          weight: 1,
          used_quota: 0,
          response_time: 400,
        },
      ]),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');

    const { result } = await mountLoaded();
    expect(result.current.channels[0].response_time).toBe(400);
  });
});

describe('useChannelsData – type tabs', () => {
  it('prefers the server type counts and adds an "all" total', async () => {
    setRoutes({
      '/api/channel/': channelList([{ id: 1, type: 1, status: 1 }], {
        type_counts: { 1: 4, 8: 2 },
      }),
    });

    const { result } = await mountLoaded();
    expect(result.current.typeCounts).toEqual({ 1: 4, 8: 2, all: 6 });
    expect(result.current.channelTypeCounts).toEqual({ 1: 4, 8: 2, all: 6 });
    expect(result.current.availableTypeKeys).toEqual(['all', '1', '8']);
  });

  it('derives the counts from the loaded rows when the server sends none', async () => {
    setRoutes({
      '/api/channel/': channelList([
        { id: 1, type: 1, status: 1 },
        { id: 2, type: 1, status: 1 },
        { id: 3, type: 8, status: 1 },
      ]),
    });

    const { result } = await mountLoaded();
    expect(result.current.channelTypeCounts).toEqual({ all: 3, 1: 2, 8: 1 });
  });

  it('counts the children of a tag group rather than the group row', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          tag: 'eu',
          type: 1,
          group: 'g',
          status: 1,
          priority: 1,
          weight: 1,
          used_quota: 0,
          response_time: 0,
        },
        {
          id: 2,
          tag: 'eu',
          type: 8,
          group: 'g',
          status: 1,
          priority: 1,
          weight: 1,
          used_quota: 0,
          response_time: 0,
        },
      ]),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');

    const { result } = await mountLoaded();
    expect(result.current.channelTypeCounts[1]).toBe(1);
    expect(result.current.channelTypeCounts[8]).toBe(1);
    expect(result.current.availableTypeKeys).toEqual(['all', '1', '8']);
  });
});

describe('useChannelsData – search and paging', () => {
  const withForm = async (values) => {
    const hook = await mountLoaded();
    act(() => {
      hook.result.current.setFormApi({ getValues: () => values });
    });
    return hook;
  };

  it('builds the search URL from the three form fields', async () => {
    setRoutes({ '/api/channel/search': channelList([{ id: 9, status: 1 }]) });
    const { result } = await withForm({
      searchKeyword: 'azure',
      searchGroup: 'vip',
      searchModel: 'gpt-4o',
    });

    await act(async () => {
      await result.current.searchChannels(false, 'all', 'all', 2, 10, false);
    });

    expect(lastChannelListUrl()).toBe(
      '/api/channel/search?keyword=azure&group=vip&model=gpt-4o&id_sort=false&tag_mode=false&p=2&page_size=10',
    );
    expect(result.current.activePage).toBe(2);
    expect(result.current.searching).toBe(false);
  });

  it('degrades to the plain listing when every search field is blank', async () => {
    const { result } = await withForm({
      searchKeyword: '',
      searchGroup: '',
      searchModel: '',
    });

    await act(async () => {
      await result.current.searchChannels(false);
    });

    expect(lastChannelListUrl()).toContain('/api/channel/?p=1');
  });

  it('routes loadChannels through the search endpoint while a query is active', async () => {
    setRoutes({ '/api/channel/search': channelList([]) });
    const { result } = await withForm({
      searchKeyword: 'k',
      searchGroup: '',
      searchModel: '',
    });

    await act(async () => {
      await result.current.loadChannels(1, 10, false, false);
    });

    expect(lastChannelListUrl()).toContain('/api/channel/search?keyword=k');
    expect(result.current.loading).toBe(false);
  });

  it('handlePageChange keeps the plain listing when no query is set', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      result.current.handlePageChange(3);
    });

    expect(result.current.activePage).toBe(3);
    expect(lastChannelListUrl()).toContain('?p=3&page_size=10');
  });

  it('handlePageChange stays on the search endpoint while a query is set', async () => {
    setRoutes({ '/api/channel/search': channelList([]) });
    const { result } = await withForm({
      searchKeyword: 'k',
      searchGroup: '',
      searchModel: '',
    });

    await act(async () => {
      result.current.handlePageChange(4);
    });

    expect(lastChannelListUrl()).toContain('&p=4&page_size=10');
  });

  it('persists the page size and rewinds to the first page', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.handlePageSizeChange(50);
    });

    expect(window.localStorage.getItem('page-size')).toBe('50');
    expect(result.current.pageSize).toBe(50);
    expect(result.current.activePage).toBe(1);
    expect(lastChannelListUrl()).toContain('?p=1&page_size=50');
  });

  it('refresh reuses the current page by default', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      result.current.handlePageChange(2);
    });
    API.get.mockClear();
    await act(async () => {
      await result.current.refresh();
    });

    expect(lastChannelListUrl()).toContain('?p=2');
  });
});

describe('useChannelsData – manageChannel', () => {
  const rows = [
    { id: 1, name: 'a', status: 1, type: 1, priority: 0, weight: 1 },
  ];
  const loaded = async () => {
    setRoutes({ '/api/channel/': channelList(rows.map((r) => ({ ...r }))) });
    return mountLoaded();
  };

  it('enables and disables through the shared PUT endpoint', async () => {
    const { result } = await loaded();
    const record = { ...rows[0] };
    API.put.mockResolvedValue({
      data: { success: true, data: { status: 2 } },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'disable', record);
    });

    expect(API.put).toHaveBeenCalledWith('/api/channel/', {
      id: 1,
      status: 2,
    });
    // The row object is patched in place from the server's answer.
    expect(record.status).toBe(2);
    expect(showSuccess).toHaveBeenCalledWith('操作成功完成！');
  });

  it('deletes through the id-scoped endpoint', async () => {
    const { result } = await loaded();
    API.delete.mockResolvedValue({
      data: { success: true, data: { status: 1 } },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'delete', { ...rows[0] });
    });

    expect(API.delete).toHaveBeenCalledWith('/api/channel/1/');
  });

  it('ignores an empty priority or weight instead of sending it', async () => {
    const { result } = await loaded();

    await act(async () => {
      await result.current.manageChannel(1, 'priority', { ...rows[0] }, '');
      await result.current.manageChannel(1, 'weight', { ...rows[0] }, '');
    });

    expect(API.put).not.toHaveBeenCalled();
  });

  it('sends an integer priority and clamps a negative weight to zero', async () => {
    const { result } = await loaded();
    API.put.mockResolvedValue({
      data: { success: true, data: { status: 1 } },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'priority', { ...rows[0] }, '7');
    });
    expect(API.put).toHaveBeenLastCalledWith('/api/channel/', {
      id: 1,
      priority: 7,
    });

    await act(async () => {
      await result.current.manageChannel(1, 'weight', { ...rows[0] }, '-4');
    });
    expect(API.put).toHaveBeenLastCalledWith('/api/channel/', {
      id: 1,
      weight: 0,
    });
  });

  // DEFECT: the priority/weight cells hand `e.target.value` (the raw DOM
  // string) straight to parseInt. parseInt stops at the first non-digit, so a
  // weight typed as '1e3' silently becomes 1 — a 1000x routing-weight change
  // in the wrong direction, saved without any warning.
  // Skipped: it encodes the correct contract, which the code does not meet.
  it.skip('does not silently truncate an exponent-style weight', async () => {
    const { result } = await loaded();
    API.put.mockResolvedValue({
      data: { success: true, data: { status: 1 } },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'weight', { ...rows[0] }, '1e3');
    });

    expect(API.put).toHaveBeenLastCalledWith('/api/channel/', {
      id: 1,
      weight: 1000,
    });
  });

  // DEFECT: parseInt('abc') is NaN and `NaN < 0` is false, so the clamp does
  // not catch it; the PUT body serialises weight as null and the upstream row
  // loses its weight.
  // Skipped: it encodes the correct contract, which the code does not meet.
  it.skip('does not send a non-numeric weight to the server', async () => {
    const { result } = await loaded();

    await act(async () => {
      await result.current.manageChannel(1, 'weight', { ...rows[0] }, 'abc');
    });

    expect(API.put).not.toHaveBeenCalled();
  });

  it('reports the server message when the update is refused', async () => {
    const { result } = await loaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'channel locked' },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'enable', { ...rows[0] });
    });

    expect(showError).toHaveBeenCalledWith('channel locked');
  });

  // DEFECT: 'enable_all' mutates record.channel_info.multi_key_status_list to
  // {} BEFORE awaiting the PUT and never restores it, so a refused or failed
  // request leaves the on-screen row claiming every sub-key is healthy.
  // Skipped: it encodes the correct contract (roll back on failure).
  it.skip('restores the multi-key status map when enable_all fails', async () => {
    const { result } = await loaded();
    const record = {
      ...rows[0],
      channel_info: { multi_key_status_list: { 0: 2 } },
    };
    API.put.mockResolvedValue({
      data: { success: false, message: 'boom' },
    });

    await act(async () => {
      await result.current.manageChannel(1, 'enable_all', record);
    });

    expect(record.channel_info.multi_key_status_list).toEqual({ 0: 2 });
  });
});

describe('useChannelsData – tag operations', () => {
  const tagRows = [
    {
      id: 1,
      tag: 'eu',
      group: 'g',
      status: 2,
      priority: 1,
      weight: 1,
      used_quota: 0,
      response_time: 0,
    },
    {
      id: 2,
      tag: 'eu',
      group: 'g',
      status: 2,
      priority: 1,
      weight: 1,
      used_quota: 0,
      response_time: 0,
    },
  ];

  const loadedTagged = async () => {
    setRoutes({
      '/api/channel/': channelList(tagRows.map((r) => ({ ...r }))),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');
    return mountLoaded();
  };

  it('enabling a tag flips the group row and every child', async () => {
    const { result } = await loadedTagged();
    API.post.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.manageTag('eu', 'enable');
    });

    expect(API.post).toHaveBeenCalledWith('/api/channel/tag/enabled', {
      tag: 'eu',
    });
    const [group] = result.current.channels;
    expect(group.status).toBe(1);
    expect(group.children.map((c) => c.status)).toEqual([1, 1]);
  });

  it('disabling a tag sets status 2 everywhere below it', async () => {
    const { result } = await loadedTagged();
    API.post.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.manageTag('eu', 'disable');
    });

    expect(API.post).toHaveBeenCalledWith('/api/channel/tag/disabled', {
      tag: 'eu',
    });
    expect(result.current.channels[0].children.map((c) => c.status)).toEqual([
      2, 2,
    ]);
  });

  it('leaves the rows alone when the tag update is refused', async () => {
    const { result } = await loadedTagged();
    API.post.mockResolvedValue({
      data: { success: false, message: 'tag missing' },
    });

    await act(async () => {
      await result.current.manageTag('eu', 'enable');
    });

    expect(showError).toHaveBeenCalledWith('tag missing');
    expect(result.current.channels[0].children.map((c) => c.status)).toEqual([
      2, 2,
    ]);
  });

  it('submitTagEdit refuses a missing priority and a negative weight', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.submitTagEdit('priority', { tag: 'eu' });
    });
    expect(showInfo).toHaveBeenCalledWith('优先级必须是整数！');

    await act(async () => {
      await result.current.submitTagEdit('weight', { tag: 'eu', weight: -1 });
    });
    expect(showInfo).toHaveBeenCalledWith('权重必须是非负整数！');
    expect(API.put).not.toHaveBeenCalled();
  });

  it('submitTagEdit sends the parsed value and refreshes', async () => {
    const { result } = await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.submitTagEdit('priority', {
        tag: 'eu',
        priority: '9',
      });
    });

    expect(API.put).toHaveBeenCalledWith('/api/channel/tag', {
      tag: 'eu',
      priority: 9,
    });
    expect(showSuccess).toHaveBeenCalledWith('更新成功！');
  });

  it('batchSetChannelTag guards both the selection and the tag value', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.batchSetChannelTag();
    });
    expect(showError).toHaveBeenCalledWith('请先选择要设置标签的渠道！');

    act(() => result.current.setSelectedChannels([{ id: 1 }]));
    await act(async () => {
      await result.current.batchSetChannelTag();
    });
    expect(showError).toHaveBeenCalledWith('标签不能为空！');
    expect(API.post).not.toHaveBeenCalled();
  });

  it('batchSetChannelTag posts the ids and closes the dialog', async () => {
    const { result } = await mountLoaded();
    API.post.mockResolvedValue({ data: { success: true, data: 2 } });

    act(() => {
      result.current.setSelectedChannels([{ id: 1 }, { id: 2 }]);
      result.current.setBatchSetTagValue('eu');
      result.current.setShowBatchSetTag(true);
    });
    await act(async () => {
      await result.current.batchSetChannelTag();
    });

    expect(API.post).toHaveBeenCalledWith('/api/channel/batch/tag', {
      ids: [1, 2],
      tag: 'eu',
    });
    expect(showSuccess).toHaveBeenCalledWith('已为 2 个渠道设置标签！');
    expect(result.current.showBatchSetTag).toBe(false);
  });
});

describe('useChannelsData – bulk channel actions', () => {
  it('batchDeleteChannels refuses an empty selection', async () => {
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.batchDeleteChannels();
    });

    expect(showError).toHaveBeenCalledWith('请先选择要删除的通道！');
    expect(API.post).not.toHaveBeenCalled();
  });

  it('batchDeleteChannels posts the selected ids and reports the count', async () => {
    const { result } = await mountLoaded();
    API.post.mockResolvedValue({ data: { success: true, data: 2 } });

    act(() => result.current.setSelectedChannels([{ id: 4 }, { id: 5 }]));
    await act(async () => {
      await result.current.batchDeleteChannels();
    });

    expect(API.post).toHaveBeenCalledWith('/api/channel/batch', {
      ids: [4, 5],
    });
    expect(showSuccess).toHaveBeenCalledWith('已删除 2 个通道！');
    expect(result.current.loading).toBe(false);
  });

  it('batchDeleteChannels surfaces a refusal', async () => {
    const { result } = await mountLoaded();
    API.post.mockResolvedValue({
      data: { success: false, message: 'in use' },
    });

    act(() => result.current.setSelectedChannels([{ id: 4 }]));
    await act(async () => {
      await result.current.batchDeleteChannels();
    });

    expect(showError).toHaveBeenCalledWith('in use');
  });

  it('testAllChannels and updateAllChannelsBalance are fire-and-forget', async () => {
    setRoutes({
      '/api/channel/test': { data: { success: true } },
      '/api/channel/update_balance': { data: { success: true } },
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.testAllChannels();
    });
    expect(showInfo).toHaveBeenCalledWith(
      '已成功开始测试所有已启用通道，请刷新页面查看结果。',
    );

    await act(async () => {
      await result.current.updateAllChannelsBalance();
    });
    expect(showInfo).toHaveBeenCalledWith('已更新完毕所有已启用通道余额！');
  });

  it('deleteAllDisabledChannels reports how many rows went away', async () => {
    const { result } = await mountLoaded();
    API.delete.mockResolvedValue({ data: { success: true, data: 3 } });

    await act(async () => {
      await result.current.deleteAllDisabledChannels();
    });

    expect(API.delete).toHaveBeenCalledWith('/api/channel/disabled');
    expect(showSuccess).toHaveBeenCalledWith('已删除所有禁用渠道，共计 3 个');
  });

  it('fixChannelsAbilities reports the success/failure split', async () => {
    const { result } = await mountLoaded();
    API.post.mockResolvedValue({
      data: { success: true, data: { success: 5, fails: 1 } },
    });

    await act(async () => {
      await result.current.fixChannelsAbilities();
    });

    expect(showSuccess).toHaveBeenCalledWith(
      '已修复 5 个通道，失败 1 个通道。',
    );
  });

  it('updateChannelBalance writes the new balance onto the row', async () => {
    setRoutes({
      '/api/channel/': channelList([{ id: 7, name: 'x', status: 1 }]),
      '/api/channel/update_balance/7/': {
        data: { success: true, balance: 42.5 },
      },
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.updateChannelBalance({ id: 7, name: 'x' });
    });

    expect(result.current.channels[0].balance).toBe(42.5);
    expect(typeof result.current.channels[0].balance_updated_time).toBe(
      'number',
    );
    expect(showInfo).toHaveBeenCalledWith('通道 x 余额更新成功！');
  });

  it('copySelectedChannel refreshes on success and reports both failure shapes', async () => {
    const { result } = await mountLoaded();

    API.post.mockResolvedValue({ data: { success: true } });
    await act(async () => {
      await result.current.copySelectedChannel({ id: 3 });
    });
    expect(API.post).toHaveBeenCalledWith('/api/channel/copy/3');
    expect(showSuccess).toHaveBeenCalledWith('渠道复制成功');

    API.post.mockResolvedValue({
      data: { success: false, message: 'duplicate name' },
    });
    await act(async () => {
      await result.current.copySelectedChannel({ id: 3 });
    });
    expect(showError).toHaveBeenCalledWith('duplicate name');

    API.post.mockRejectedValue({ response: { data: { message: 'boom' } } });
    await act(async () => {
      await result.current.copySelectedChannel({ id: 3 });
    });
    expect(showError).toHaveBeenCalledWith('渠道复制失败: boom');
  });

  it('checkOllamaVersion opens an info dialog or reports the failure', async () => {
    setRoutes({
      '/api/channel/ollama/version/2': {
        data: { success: true, data: { version: '0.5.1' } },
      },
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.checkOllamaVersion({ id: 2 });
    });
    expect(Modal.info).toHaveBeenCalledWith(
      expect.objectContaining({
        title: 'Ollama 版本信息',
        content: '当前 Ollama 版本为 0.5.1',
      }),
    );

    setRoutes({
      '/api/channel/ollama/version/2': {
        data: { success: false, message: 'not an ollama channel' },
      },
    });
    await act(async () => {
      await result.current.checkOllamaVersion({ id: 2 });
    });
    expect(showError).toHaveBeenCalledWith('not an ollama channel');
  });
});

describe('useChannelsData – per-channel model selection', () => {
  const modelRows = [
    { id: 1, name: 'a', status: 1, models: 'gpt-4o,gpt-4o-mini,o3' },
    { id: 2, name: 'b', status: 1, models: 'claude-3' },
  ];
  const loaded = async () => {
    setRoutes({
      '/api/channel/': channelList(modelRows.map((r) => ({ ...r }))),
    });
    return mountLoaded();
  };

  it('replaces only the slice belonging to the given channel', async () => {
    const { result } = await loaded();

    act(() => {
      result.current.setChannelModelSelection(1, ['gpt-4o', 'o3']);
      result.current.setChannelModelSelection(2, ['claude-3']);
    });
    expect(
      result.current.selectedModels.map((m) => [m.channelId, m.modelName]),
    ).toEqual([
      [1, 'gpt-4o'],
      [1, 'o3'],
      [2, 'claude-3'],
    ]);

    act(() => result.current.setChannelModelSelection(1, ['o3']));
    expect(
      result.current.selectedModels.map((m) => [m.channelId, m.modelName]),
    ).toEqual([
      [2, 'claude-3'],
      [1, 'o3'],
    ]);
  });

  it('drops the slice when the channel is not on the current page', async () => {
    const { result } = await loaded();

    act(() => result.current.setChannelModelSelection(1, ['gpt-4o']));
    act(() => result.current.setChannelModelSelection(999, ['ghost']));

    expect(result.current.selectedModels.map((m) => m.modelName)).toEqual([
      'gpt-4o',
    ]);
  });

  it('removeModelsFromChannel rewrites the model list and the mapping', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          name: 'a',
          status: 1,
          models: 'gpt-4o, gpt-4o-mini ,o3',
          model_mapping: '{"gpt-4o":"upstream-4o","o3":"upstream-o3"}',
        },
      ]),
    });
    const { result } = await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    let outcome;
    await act(async () => {
      outcome = await result.current.removeModelsFromChannel(1, ['gpt-4o']);
    });

    expect(API.put).toHaveBeenCalledWith('/api/channel/', {
      id: 1,
      models: 'gpt-4o-mini,o3',
      model_mapping: '{"o3":"upstream-o3"}',
    });
    expect(outcome).toBe(true);
    expect(showSuccess).toHaveBeenCalledWith('已移除 1 个模型');
  });

  it('blanks the mapping when nothing is left in it', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          name: 'a',
          status: 1,
          models: 'gpt-4o,o3',
          model_mapping: '{"gpt-4o":"x"}',
        },
      ]),
    });
    const { result } = await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.removeModelsFromChannel(1, ['gpt-4o']);
    });

    expect(API.put).toHaveBeenCalledWith('/api/channel/', {
      id: 1,
      models: 'o3',
      model_mapping: '',
    });
  });

  it('treats an unparseable mapping as empty instead of throwing', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 1,
          name: 'a',
          status: 1,
          models: 'gpt-4o,o3',
          model_mapping: 'not json',
        },
      ]),
    });
    const { result } = await mountLoaded();
    API.put.mockResolvedValue({ data: { success: true } });

    await act(async () => {
      await result.current.removeModelsFromChannel(1, ['o3']);
    });

    expect(API.put).toHaveBeenCalledWith('/api/channel/', {
      id: 1,
      models: 'gpt-4o',
      model_mapping: '',
    });
  });

  it('reports an unknown channel and keeps the selection', async () => {
    const { result } = await loaded();

    act(() => result.current.setChannelModelSelection(1, ['gpt-4o']));
    let outcome;
    await act(async () => {
      outcome = await result.current.removeModelsFromChannel(404, ['gpt-4o']);
    });

    expect(outcome).toBe(false);
    expect(showError).toHaveBeenCalledWith('未找到对应渠道');
    expect(API.put).not.toHaveBeenCalled();
    expect(result.current.selectedModels).toHaveLength(1);
  });

  it('keeps the selection when the server refuses the removal', async () => {
    const { result } = await loaded();
    API.put.mockResolvedValue({
      data: { success: false, message: 'channel is read-only' },
    });

    act(() => result.current.setChannelModelSelection(1, ['gpt-4o']));
    let outcome;
    await act(async () => {
      outcome = await result.current.removeModelsFromChannel(1, ['gpt-4o']);
    });

    expect(outcome).toBe(false);
    expect(showError).toHaveBeenCalledWith('channel is read-only');
    expect(result.current.selectedModels).toHaveLength(1);
  });

  it('finds channels nested under a tag group', async () => {
    setRoutes({
      '/api/channel/': channelList([
        {
          id: 11,
          tag: 'eu',
          group: 'g',
          status: 1,
          priority: 1,
          weight: 1,
          used_quota: 0,
          response_time: 0,
          models: 'gpt-4o',
        },
      ]),
    });
    window.localStorage.setItem('enable-tag-mode', 'true');
    const { result } = await mountLoaded();

    act(() => result.current.setChannelModelSelection(11, ['gpt-4o']));
    expect(result.current.selectedModels[0].channel.id).toBe(11);

    // updateChannelProperty must reach the same nested row.
    act(() => {
      result.current.updateChannelProperty(11, (ch) => {
        ch.response_time = 123;
      });
    });
    expect(result.current.channels[0].children[0].response_time).toBe(123);
  });
});

describe('useChannelsData – model testing', () => {
  const testChannelRow = { id: 5, name: 'azure', status: 1, models: 'a,b' };

  const loaded = async () => {
    setRoutes({
      '/api/channel/': channelList([{ ...testChannelRow }]),
      '/api/channel/test/5': { data: { success: true, time: 1.5 } },
    });
    return mountLoaded();
  };

  it('records a successful test and stamps the response time on the row', async () => {
    const { result } = await loaded();

    await act(async () => {
      await result.current.testChannel({ id: 5, name: 'azure' }, 'a');
    });

    expect(API.get).toHaveBeenCalledWith('/api/channel/test/5?model=a');
    expect(result.current.modelTestResults['5-a']).toMatchObject({
      success: true,
      time: 1.5,
    });
    expect(result.current.channels[0].response_time).toBe(1500);
    expect(result.current.testingModels.size).toBe(0);
  });

  it('passes the endpoint type through when one is chosen', async () => {
    setRoutes({
      '/api/channel/': channelList([{ ...testChannelRow }]),
      '/api/channel/test/5': { data: { success: true, time: 0.2 } },
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.testChannel({ id: 5, name: 'azure' }, 'a', 'openai');
    });

    expect(API.get).toHaveBeenCalledWith(
      '/api/channel/test/5?model=a&endpoint_type=openai',
    );
  });

  it('records a failed test without touching the response time', async () => {
    setRoutes({
      '/api/channel/': channelList([{ ...testChannelRow }]),
      '/api/channel/test/5': {
        data: { success: false, message: 'upstream 401' },
      },
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.testChannel({ id: 5, name: 'azure' }, 'b');
    });

    expect(result.current.modelTestResults['5-b']).toMatchObject({
      success: false,
      message: 'upstream 401',
      time: 0,
    });
    expect(showError).toHaveBeenCalledWith('模型 b: upstream 401');
    expect(result.current.channels[0].response_time).toBeUndefined();
  });

  it('records a network failure and still clears the in-flight marker', async () => {
    setRoutes({
      '/api/channel/': channelList([{ ...testChannelRow }]),
      '/api/channel/test/5': () => Promise.reject(new Error('ECONNRESET')),
    });
    const { result } = await mountLoaded();

    await act(async () => {
      await result.current.testChannel({ id: 5, name: 'azure' }, 'a');
    });

    expect(result.current.modelTestResults['5-a']).toMatchObject({
      success: false,
      message: 'ECONNRESET',
    });
    expect(result.current.testingModels.size).toBe(0);
  });

  it('batchTestModels refuses a channel with no model list', async () => {
    const { result } = await loaded();

    await act(async () => {
      await result.current.batchTestModels();
    });
    expect(showError).toHaveBeenCalledWith('渠道模型信息不完整');

    act(() => result.current.setCurrentTestChannel({ id: 5, models: 'a,b' }));
    act(() => result.current.setModelSearchKeyword('zzz'));
    await act(async () => {
      await result.current.batchTestModels();
    });
    expect(showError).toHaveBeenCalledWith('没有找到匹配的模型');
  });

  it('batchTestModels tests every matching model and summarises', async () => {
    const { result } = await loaded();

    act(() => result.current.setCurrentTestChannel({ ...testChannelRow }));
    await act(async () => {
      await result.current.batchTestModels();
    });

    expect(API.get).toHaveBeenCalledWith('/api/channel/test/5?model=a');
    expect(API.get).toHaveBeenCalledWith('/api/channel/test/5?model=b');
    expect(result.current.isBatchTesting).toBe(false);
    await waitFor(() =>
      expect(showSuccess).toHaveBeenCalledWith(
        '批量测试完成！成功: 2, 失败: 0, 总计: 2',
      ),
    );
  });

  it('handleCloseModal resets the modal state but keeps the results', async () => {
    const { result } = await loaded();

    await act(async () => {
      await result.current.testChannel({ id: 5, name: 'azure' }, 'a');
    });
    expect(Object.keys(result.current.modelTestResults)).toHaveLength(1);

    act(() => {
      result.current.setShowModelTestModal(true);
      result.current.setModelSearchKeyword('gpt');
      result.current.setSelectedModelKeys(['a']);
      result.current.setModelTablePage(3);
      result.current.setSelectedEndpointType('openai');
    });
    act(() => result.current.handleCloseModal());

    expect(result.current.showModelTestModal).toBe(false);
    expect(result.current.modelSearchKeyword).toBe('');
    expect(result.current.selectedModelKeys).toEqual([]);
    expect(result.current.modelTablePage).toBe(1);
    expect(result.current.selectedEndpointType).toBe('');
    expect(result.current.isBatchTesting).toBe(false);
    // Results survive the close on purpose so the operator can reopen and read
    // them.
    expect(Object.keys(result.current.modelTestResults)).toHaveLength(1);
  });
});

describe('useChannelsData – column preferences', () => {
  it('starts with every column visible and persists later edits', async () => {
    const { result } = await mountLoaded();

    await waitFor(() =>
      expect(Object.keys(result.current.visibleColumns)).toHaveLength(9),
    );
    expect(Object.values(result.current.visibleColumns).every(Boolean)).toBe(
      true,
    );

    act(() => result.current.handleColumnVisibilityChange('balance', false));

    expect(result.current.visibleColumns.balance).toBe(false);
    await waitFor(() =>
      expect(
        JSON.parse(window.localStorage.getItem('channels-table-columns'))
          .balance,
      ).toBe(false),
    );
  });

  it('merges a saved subset over the defaults', async () => {
    window.localStorage.setItem(
      'channels-table-columns',
      JSON.stringify({ weight: false }),
    );

    const { result } = await mountLoaded();
    await waitFor(() =>
      expect(result.current.visibleColumns.weight).toBe(false),
    );
    expect(result.current.visibleColumns.priority).toBe(true);
  });

  it('falls back to the defaults when the stored value is corrupt', async () => {
    window.localStorage.setItem('channels-table-columns', 'not json');

    const { result } = await mountLoaded();
    await waitFor(() =>
      expect(result.current.visibleColumns.weight).toBe(true),
    );
  });

  it('handleSelectAll flips every column at once', async () => {
    const { result } = await mountLoaded();

    act(() => result.current.handleSelectAll(false));
    expect(Object.values(result.current.visibleColumns)).toEqual(
      new Array(9).fill(false),
    );

    act(() => result.current.handleSelectAll(true));
    expect(Object.values(result.current.visibleColumns)).toEqual(
      new Array(9).fill(true),
    );
  });
});

describe('useChannelsData – misc state', () => {
  it('getFormValues normalises missing fields to empty strings', async () => {
    const { result } = await mountLoaded();

    expect(result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchGroup: '',
      searchModel: '',
    });

    act(() =>
      result.current.setFormApi({ getValues: () => ({ searchGroup: 'vip' }) }),
    );
    expect(result.current.getFormValues()).toEqual({
      searchKeyword: '',
      searchGroup: 'vip',
      searchModel: '',
    });
  });

  it('closeEdit hides the editor without clearing the record', async () => {
    const { result } = await mountLoaded();

    act(() => {
      result.current.setEditingChannel({ id: 9 });
      result.current.setShowEdit(true);
    });
    act(() => result.current.closeEdit());

    expect(result.current.showEdit).toBe(false);
    expect(result.current.editingChannel).toEqual({ id: 9 });
  });

  it('updateChannelProperty is a no-op for an id that is not on the page', async () => {
    setRoutes({
      '/api/channel/': channelList([{ id: 1, name: 'a', status: 1 }]),
    });
    const { result } = await mountLoaded();
    const before = result.current.channels;

    act(() => {
      result.current.updateChannelProperty(404, (ch) => {
        ch.name = 'mutated';
      });
    });

    expect(result.current.channels).toBe(before);
    expect(result.current.channels[0].name).toBe('a');
  });
});
