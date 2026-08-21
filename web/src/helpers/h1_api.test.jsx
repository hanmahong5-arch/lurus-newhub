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

// utils.jsx reads window.matchMedia at module scope (api.js imports it).
vi.hoisted(() => {
  if (typeof window !== 'undefined' && !window.matchMedia) {
    window.matchMedia = (query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    });
  }
});

vi.mock('@douyinfe/semi-ui', () => ({
  Toast: { error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn() },
  Pagination: () => null,
}));
vi.mock('react-toastify', () => ({ toast: vi.fn() }));
vi.mock('../components/hifi/HfToast', () => ({
  hfToast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    info: vi.fn(),
  },
}));

// api.js funnels every unhandled failure through showError; replace just that
// one export so the funnel is observable without touching the pure helpers.
vi.mock('./utils', async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, showError: vi.fn() };
});

// A minimal axios stand-in. `__rawGet` is the pre-patch getter, which is what
// the in-flight de-duplication in patchAPIInstance wraps.
vi.mock('axios', () => {
  const create = vi.fn(() => {
    const rawGet = vi.fn(() => Promise.resolve({ data: {} }));
    const inst = vi.fn(() => Promise.resolve({ data: { replayed: true } }));
    inst.get = rawGet;
    inst.post = vi.fn(() => Promise.resolve({ data: {} }));
    inst.interceptors = { response: { use: vi.fn() } };
    inst.__rawGet = rawGet;
    return inst;
  });
  return { default: { create } };
});

import axios from 'axios';
import { showError } from './utils';
import {
  API,
  updateAPI,
  buildApiPayload,
  handleApiError,
  processModelsData,
  processGroupsData,
  loadChannelModels,
  getChannelModels,
} from './api';
import { fetchTokenKeys, getServerAddress } from './token';

// The rejection half of the response interceptor registered on `API`.
const onRejected = () =>
  axios.create.mock.results[0].value.interceptors.response.use.mock.calls[0][1];

beforeEach(() => {
  localStorage.clear();
  API.__rawGet.mockReset();
  API.__rawGet.mockResolvedValue({ data: {} });
  API.post.mockReset();
  API.post.mockResolvedValue({ data: {} });
  API.mockReset();
  API.mockResolvedValue({ data: { replayed: true } });
  showError.mockClear();
});

describe('in-flight GET de-duplication', () => {
  it('shares one request between concurrent identical GETs', async () => {
    let resolveIt;
    API.__rawGet.mockImplementation(() => new Promise((r) => (resolveIt = r)));

    const a = API.get('/api/models');
    const b = API.get('/api/models');
    expect(a).toBe(b);
    expect(API.__rawGet).toHaveBeenCalledTimes(1);

    resolveIt({ data: { ok: true } });
    await a;
  });

  it('releases the slot once the request settles', async () => {
    API.__rawGet.mockResolvedValue({ data: { ok: true } });
    await API.get('/api/models');
    await API.get('/api/models');
    expect(API.__rawGet).toHaveBeenCalledTimes(2);
  });

  it('releases the slot after a FAILED request too', async () => {
    API.__rawGet.mockRejectedValue(new Error('boom'));
    await expect(API.get('/api/models')).rejects.toThrow('boom');
    await expect(API.get('/api/models')).rejects.toThrow('boom');
    expect(API.__rawGet).toHaveBeenCalledTimes(2);
  });

  it('keys on params, so different queries are not collapsed', async () => {
    let pending = [];
    API.__rawGet.mockImplementation(() => new Promise((r) => pending.push(r)));
    const a = API.get('/api/log', { params: { page: 1 } });
    const b = API.get('/api/log', { params: { page: 2 } });
    expect(a).not.toBe(b);
    expect(API.__rawGet).toHaveBeenCalledTimes(2);
    pending.forEach((r) => r({ data: {} }));
    await Promise.all([a, b]);
  });

  it('lets a caller opt out with disableDuplicate', async () => {
    API.__rawGet.mockImplementation(() => new Promise(() => {}));
    API.get('/api/models', { disableDuplicate: true });
    API.get('/api/models', { disableDuplicate: true });
    expect(API.__rawGet).toHaveBeenCalledTimes(2);
  });
});

describe('401 session self-heal interceptor', () => {
  const unauthorized = (url = '/api/user/self') => ({
    response: { status: 401 },
    config: { url },
  });

  it('passes straight through when the caller opted out of handling', async () => {
    const err = {
      response: { status: 500 },
      config: { skipErrorHandler: true },
    };
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(showError).not.toHaveBeenCalled();
  });

  it('re-bootstraps the session and replays the request exactly once', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    API.post.mockResolvedValue({
      data: { success: true, data: { id: 9, tenant_slug: 'acme' } },
    });
    const err = unauthorized();

    const res = await onRejected()(err);

    expect(API.post).toHaveBeenCalledWith(
      '/api/v2/auth/zita-bootstrap',
      {},
      { skipErrorHandler: true },
    );
    // The replay must be marked so a second 401 cannot loop.
    expect(err.config._retriedAfterBootstrap).toBe(true);
    expect(err.config.skipErrorHandler).toBe(true);
    expect(API).toHaveBeenCalledWith(err.config);
    expect(res).toEqual({ data: { replayed: true } });
    expect(showError).not.toHaveBeenCalled();
    // The refreshed identity and tenant are persisted for the shim.
    expect(JSON.parse(localStorage.getItem('user')).id).toBe(9);
  });

  it('does not heal when the SPA has no durable user', async () => {
    const err = unauthorized();
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(API.post).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(err);
  });

  it('does not heal a 401 on the bridge endpoint itself', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    const err = unauthorized('/api/v2/auth/zita-bootstrap');
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(API.post).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(err);
  });

  it('does not heal a request that was already replayed', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    const err = {
      response: { status: 401 },
      config: { url: '/api/user/self', _retriedAfterBootstrap: true },
    };
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(API.post).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalledWith(err);
  });

  it('falls back to the toast when the bridge declines the session', async () => {
    localStorage.setItem('user', JSON.stringify({ id: 1 }));
    API.post.mockResolvedValue({ data: { success: false } });
    const err = unauthorized();
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(showError).toHaveBeenCalledWith(err);
    // A failed bootstrap must not overwrite the existing shim.
    expect(JSON.parse(localStorage.getItem('user')).id).toBe(1);
  });

  it('reports any non-401 failure through the toast funnel', async () => {
    const err = { response: { status: 500 }, config: { url: '/api/x' } };
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(showError).toHaveBeenCalledWith(err);
  });

  it('reports a failure with no config at all', async () => {
    const err = { message: 'network down' };
    await expect(onRejected()(err)).rejects.toBe(err);
    expect(showError).toHaveBeenCalledWith(err);
  });
});

describe('updateAPI', () => {
  it('builds a fresh instance and re-applies both patches', () => {
    const before = axios.create.mock.calls.length;
    updateAPI();
    expect(axios.create.mock.calls.length).toBe(before + 1);
    const fresh = axios.create.mock.results.at(-1).value;
    // de-dup patch applied (get replaced) and interceptor registered
    expect(fresh.get).not.toBe(fresh.__rawGet);
    expect(fresh.interceptors.response.use).toHaveBeenCalledTimes(1);
  });

  it('stamps the current user id onto the request headers', () => {
    localStorage.setItem('user', JSON.stringify({ id: 77 }));
    updateAPI();
    const cfg = axios.create.mock.calls.at(-1)[0];
    expect(cfg.headers['lurus-api-User']).toBe(77);
    expect(cfg.headers['Cache-Control']).toBe('no-store');
  });
});

describe('buildApiPayload', () => {
  const inputs = { model: 'gpt-4o', group: 'default', stream: true };

  it('keeps only valid messages and strips client-only fields', () => {
    const payload = buildApiPayload(
      [
        { role: 'user', content: 'hi', id: '1', createAt: 1 },
        { role: 'assistant' }, // invalid: no content
        { content: 'orphan' }, // invalid: no role
      ],
      '',
      inputs,
      {},
    );
    expect(payload.messages).toEqual([{ role: 'user', content: 'hi' }]);
    expect(payload.model).toBe('gpt-4o');
    expect(payload.group).toBe('default');
    expect(payload.stream).toBe(true);
  });

  it('prepends a trimmed system prompt', () => {
    const payload = buildApiPayload(
      [{ role: 'user', content: 'hi' }],
      '   be terse   ',
      inputs,
      {},
    );
    expect(payload.messages[0]).toEqual({
      role: 'system',
      content: 'be terse',
    });
    expect(payload.messages).toHaveLength(2);
  });

  it('ignores a whitespace-only system prompt', () => {
    const payload = buildApiPayload(
      [{ role: 'user', content: 'hi' }],
      '   ',
      inputs,
      {},
    );
    expect(payload.messages).toHaveLength(1);
  });

  it('includes a tuning parameter only when it is BOTH enabled and set', () => {
    const payload = buildApiPayload(
      [{ role: 'user', content: 'hi' }],
      '',
      { ...inputs, temperature: 0.7, top_p: 0.9, seed: 5 },
      { temperature: true, top_p: false },
    );
    expect(payload.temperature).toBe(0.7);
    expect(payload).not.toHaveProperty('top_p'); // enabled=false
    expect(payload).not.toHaveProperty('seed'); // not enabled at all
  });

  it('passes a zero-valued parameter through (0 is a real setting)', () => {
    const payload = buildApiPayload(
      [{ role: 'user', content: 'hi' }],
      '',
      { ...inputs, temperature: 0 },
      { temperature: true },
    );
    expect(payload.temperature).toBe(0);
  });

  it('drops an enabled parameter whose value is null/undefined', () => {
    const payload = buildApiPayload(
      [{ role: 'user', content: 'hi' }],
      '',
      { ...inputs, max_tokens: null },
      { max_tokens: true },
    );
    expect(payload).not.toHaveProperty('max_tokens');
  });
});

describe('handleApiError', () => {
  it('captures the message, a timestamp and the stack', () => {
    const err = new Error('boom');
    const info = handleApiError(err);
    expect(info.error).toBe('boom');
    expect(info.stack).toBe(err.stack);
    expect(() => new Date(info.timestamp)).not.toThrow();
  });

  it('folds in the response status when one is supplied', () => {
    const info = handleApiError(new Error('boom'), {
      status: 503,
      statusText: 'Service Unavailable',
    });
    expect(info.status).toBe(503);
    expect(info.statusText).toBe('Service Unavailable');
  });

  it('classifies HTTP-status and network failures', () => {
    expect(handleApiError(new Error('HTTP error 500')).details).toBe(
      '服务器返回了错误状态码',
    );
    expect(handleApiError(new Error('Failed to fetch')).details).toBe(
      '网络连接失败或服务器无响应',
    );
    expect(handleApiError(new Error('something else')).details).toBeUndefined();
  });

  it('should survive an error object with no message', () => {
    // DEFECT: line 1 of handleApiError already defends against a missing
    // message (`error.message || '未知错误'`), but two lines later it calls
    // error.message.includes(...) unguarded. A rejection that is not an
    // Error (e.g. a bare string thrown, or `{ status: 500 }`) therefore
    // crashes the very handler written to describe it.
    expect(handleApiError({}).error).toBe('未知错误');
  });
});

describe('processModelsData', () => {
  it('maps names to label/value pairs and keeps a still-valid selection', () => {
    const out = processModelsData(['gpt-4o', 'claude-3'], 'claude-3');
    expect(out.modelOptions).toEqual([
      { label: 'gpt-4o', value: 'gpt-4o' },
      { label: 'claude-3', value: 'claude-3' },
    ]);
    expect(out.selectedModel).toBe('claude-3');
  });

  it('falls back to the first model when the selection disappeared', () => {
    expect(
      processModelsData(['gpt-4o', 'claude-3'], 'retired').selectedModel,
    ).toBe('gpt-4o');
  });

  it('leaves the selection undefined when there are no models', () => {
    const out = processModelsData([], 'gpt-4o');
    expect(out.modelOptions).toEqual([]);
    expect(out.selectedModel).toBeUndefined();
  });
});

describe('processGroupsData', () => {
  const groups = {
    default: { desc: 'Default group', ratio: 1 },
    vip: { desc: 'VIP group', ratio: 0.8 },
  };

  it('maps each group to a labelled option carrying its ratio', () => {
    const out = processGroupsData(groups, null);
    expect(out).toEqual([
      {
        label: 'Default group',
        value: 'default',
        ratio: 1,
        fullLabel: 'Default group',
      },
      { label: 'VIP group', value: 'vip', ratio: 0.8, fullLabel: 'VIP group' },
    ]);
  });

  it("hoists the user's own group to the front", () => {
    const out = processGroupsData(groups, 'vip');
    expect(out.map((g) => g.value)).toEqual(['vip', 'default']);
  });

  it('leaves the order alone when the user group is not offered', () => {
    expect(processGroupsData(groups, 'ghost').map((g) => g.value)).toEqual([
      'default',
      'vip',
    ]);
  });

  it('truncates a long description in the label but keeps it in fullLabel', () => {
    const desc = 'x'.repeat(30);
    const [opt] = processGroupsData({ a: { desc, ratio: 1 } }, null);
    expect(opt.label).toBe('x'.repeat(20) + '...');
    expect(opt.fullLabel).toBe(desc);
  });

  it('does not truncate a description of exactly 20 characters', () => {
    const desc = 'y'.repeat(20);
    expect(processGroupsData({ a: { desc, ratio: 1 } }, null)[0].label).toBe(
      desc,
    );
  });

  it('substitutes a single placeholder option when there are no groups', () => {
    expect(processGroupsData({}, 'vip')).toEqual([
      { label: '用户分组', value: '', ratio: 1 },
    ]);
  });
});

describe('channel model cache', () => {
  it('returns an empty list before anything was cached', () => {
    expect(getChannelModels(1)).toEqual([]);
  });

  it('serves types from the localStorage cache after a cold start', () => {
    localStorage.setItem(
      'channel_models',
      JSON.stringify({ 1: ['gpt-4o'], 2: [] }),
    );
    expect(getChannelModels(1)).toEqual(['gpt-4o']);
    expect(getChannelModels(2)).toEqual([]);
    expect(getChannelModels(99)).toEqual([]);
  });

  it('loadChannelModels persists the payload and warms the in-memory cache', async () => {
    API.__rawGet.mockResolvedValue({
      data: { success: true, data: { 5: ['claude-3'] } },
    });
    await loadChannelModels();
    expect(JSON.parse(localStorage.getItem('channel_models'))).toEqual({
      5: ['claude-3'],
    });
    localStorage.removeItem('channel_models');
    // Served from memory now, with no localStorage left to fall back on.
    expect(getChannelModels(5)).toEqual(['claude-3']);
  });

  it('loadChannelModels writes nothing when the API reports failure', async () => {
    API.__rawGet.mockResolvedValue({ data: { success: false } });
    await loadChannelModels();
    expect(localStorage.getItem('channel_models')).toBeNull();
  });
});

describe('fetchTokenKeys', () => {
  it('requests the v1 token page and keeps only active keys', async () => {
    API.__rawGet.mockResolvedValue({
      data: {
        success: true,
        data: [
          { key: 'sk-live', status: 1 },
          { key: 'sk-disabled', status: 2 },
          { key: 'sk-expired', status: 3 },
        ],
      },
    });
    await expect(fetchTokenKeys()).resolves.toEqual(['sk-live']);
    expect(API.__rawGet).toHaveBeenCalledWith('/api/token/?p=1&size=10', {});
  });

  it('accepts the paginated {items:[...]} envelope too', async () => {
    API.__rawGet.mockResolvedValue({
      data: { success: true, data: { items: [{ key: 'sk-a', status: 1 }] } },
    });
    await expect(fetchTokenKeys()).resolves.toEqual(['sk-a']);
  });

  it('returns an empty list (never throws) when the API reports failure', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    API.__rawGet.mockResolvedValue({ data: { success: false } });
    await expect(fetchTokenKeys()).resolves.toEqual([]);
    err.mockRestore();
  });

  it('returns an empty list when the request itself rejects', async () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    API.__rawGet.mockRejectedValue(new Error('offline'));
    await expect(fetchTokenKeys()).resolves.toEqual([]);
    err.mockRestore();
  });

  it('switches to the tenant-scoped v2 path once a slug is set', async () => {
    localStorage.setItem('tenant_slug', 'acme');
    API.__rawGet.mockResolvedValue({ data: { success: true, data: [] } });
    await fetchTokenKeys();
    expect(API.__rawGet.mock.calls[0][0]).toContain('acme');
  });
});

describe('getServerAddress', () => {
  it('prefers the configured server_address from status', () => {
    localStorage.setItem(
      'status',
      JSON.stringify({ server_address: 'https://hub.example.test' }),
    );
    expect(getServerAddress()).toBe('https://hub.example.test');
  });

  it('falls back to the current origin when status has no address', () => {
    localStorage.setItem('status', JSON.stringify({}));
    expect(getServerAddress()).toBe(window.location.origin);
  });

  it('falls back to the current origin when status is absent', () => {
    expect(getServerAddress()).toBe(window.location.origin);
  });

  it('survives a corrupted status blob', () => {
    const err = vi.spyOn(console, 'error').mockImplementation(() => {});
    localStorage.setItem('status', 'not json{');
    expect(getServerAddress()).toBe(window.location.origin);
    expect(err).toHaveBeenCalled();
    err.mockRestore();
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});
