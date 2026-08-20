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
import { useRef, useState } from 'react';
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

const { sseInstances } = vi.hoisted(() => ({ sseInstances: [] }));

vi.mock('sse.js', () => {
  class FakeSSE {
    constructor(url, options) {
      this.url = url;
      this.options = options;
      this.listeners = {};
      this.readyState = 0;
      this.status = undefined;
      this.closeCount = 0;
      this.streamCount = 0;
      this.throwOnStream = null;
      sseInstances.push(this);
    }
    addEventListener(type, cb) {
      (this.listeners[type] = this.listeners[type] || []).push(cb);
    }
    stream() {
      this.streamCount += 1;
      if (this.throwOnStream) throw this.throwOnStream;
      this.readyState = 1;
    }
    close() {
      this.closeCount += 1;
      this.readyState = 2;
    }
    emit(type, event) {
      (this.listeners[type] || []).forEach((cb) => cb(event));
    }
  }
  return { SSE: FakeSSE };
});

vi.mock('../../helpers', () => ({
  getUserIdFromLocalStorage: () => '42',
  handleApiError: (error, response) => ({
    kind: 'api-error',
    message: error?.message,
    httpStatus: response?.status,
  }),
  // Contract mirror: pull <think>...</think> out of content into reasoning.
  processThinkTags: (content, reasoningContent) => ({
    content: content.replace(/<think>[\s\S]*?<\/think>/g, ''),
    reasoningContent,
  }),
  processIncompleteThinkTags: (content, reasoningContent) => ({
    content: content.replace(/<think>[\s\S]*$/, ''),
    reasoningContent,
  }),
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key) => key }),
}));

import { useApiRequest } from './useApiRequest';
import {
  MESSAGE_STATUS,
  DEBUG_TABS,
} from '../../constants/playground.constants';

const assistant = (over = {}) => ({
  id: 'a1',
  role: 'assistant',
  status: MESSAGE_STATUS.LOADING,
  content: '',
  reasoningContent: '',
  isReasoningExpanded: true,
  ...over,
});

const saveMessages = vi.fn();

const harness = (
  initial = [{ id: 'u1', role: 'user', content: 'hi' }, assistant()],
) => {
  const hook = renderHook(() => {
    const [message, setMessage] = useState(initial);
    const [debugData, setDebugData] = useState({});
    const [activeDebugTab, setActiveDebugTab] = useState(DEBUG_TABS.PREVIEW);
    const sseSourceRef = useRef(null);
    const api = useApiRequest(
      setMessage,
      setDebugData,
      setActiveDebugTab,
      sseSourceRef,
      saveMessages,
    );
    return { message, debugData, activeDebugTab, sseSourceRef, ...api };
  });
  return hook;
};

const last = (hook) =>
  hook.result.current.message[hook.result.current.message.length - 1];

const startStream = async (
  hook,
  payload = { model: 'gpt-4o', stream: true },
) => {
  await act(async () => {
    hook.result.current.sendRequest(payload, true);
  });
  return sseInstances[sseInstances.length - 1];
};

const chunk = (delta) => JSON.stringify({ choices: [{ delta, index: 0 }] });

const flushMicroTimers = async () => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

beforeEach(async () => {
  // The hook defers saveMessages by a 0ms timer; drain anything a previous
  // test left queued before resetting the spy, so leakage cannot be mistaken
  // for a call made by the test under way.
  await new Promise((resolve) => setTimeout(resolve, 0));
  sseInstances.length = 0;
  saveMessages.mockClear();
  vi.spyOn(console, 'error').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('useApiRequest — stream setup', () => {
  it('opens the relay endpoint with the caller identity and the payload', async () => {
    const hook = harness();
    const source = await startStream(hook, { model: 'gpt-4o', stream: true });

    expect(source.url).toBe('/pg/chat/completions');
    expect(source.options.method).toBe('POST');
    expect(source.options.headers['lurus-api-User']).toBe('42');
    expect(source.options.headers['Content-Type']).toBe('application/json');
    expect(JSON.parse(source.options.payload)).toEqual({
      model: 'gpt-4o',
      stream: true,
    });
    expect(source.streamCount).toBe(1);
    expect(hook.result.current.sseSourceRef.current).toBe(source);
  });

  it('primes the debug pane on the request tab before any token arrives', async () => {
    const hook = harness();
    await startStream(hook);

    expect(hook.result.current.activeDebugTab).toBe(DEBUG_TABS.REQUEST);
    expect(hook.result.current.debugData.isStreaming).toBe(true);
    expect(hook.result.current.debugData.sseMessages).toEqual([]);
    expect(hook.result.current.debugData.response).toBeNull();
  });

  it('routes a non-stream request away from SSE', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ choices: [] }),
    });

    await act(async () => {
      hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(sseInstances).toHaveLength(0);
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  it('reports a stream that cannot even be opened', async () => {
    const hook = harness();
    const failing = new Error('CORS blocked');
    // Fail the very first stream() call.
    const original = sseInstances.length;
    await act(async () => {
      hook.result.current.sendRequest({ model: 'x' }, true);
    });
    expect(sseInstances.length).toBe(original + 1);

    // Second attempt with a throwing stream().
    const hook2 = harness();
    const spy = vi
      .spyOn(Object.getPrototypeOf(sseInstances[0]), 'stream')
      .mockImplementation(function () {
        throw failing;
      });
    await act(async () => {
      hook2.result.current.sendRequest({ model: 'x' }, true);
    });
    spy.mockRestore();

    expect(last(hook2).content).toContain('建立连接时发生错误');
    expect(last(hook2).status).toBe(MESSAGE_STATUS.ERROR);
    expect(hook2.result.current.debugData.response).toContain('Stream启动失败');
  });
});

describe('useApiRequest — streaming deltas', () => {
  it('appends content chunks and flips the debug pane to the response tab', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'Hel' }) });
    });
    expect(hook.result.current.activeDebugTab).toBe(DEBUG_TABS.RESPONSE);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'lo!' }) });
    });

    expect(last(hook).content).toBe('Hello!');
    expect(last(hook).status).toBe(MESSAGE_STATUS.INCOMPLETE);
    expect(hook.result.current.debugData.sseMessages).toHaveLength(2);
  });

  it('keeps reasoning tokens in their own field and the panel open', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ reasoning_content: 'step 1 ' }) });
      source.emit('message', { data: chunk({ reasoning: 'step 2' }) });
    });

    expect(last(hook).reasoningContent).toBe('step 1 step 2');
    expect(last(hook).content).toBe('');
    expect(last(hook).isThinkingComplete).toBe(false);
    expect(last(hook).isReasoningExpanded).toBe(true);
  });

  it('collapses the reasoning panel exactly once when content starts', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', {
        data: chunk({ reasoning_content: 'thinking' }),
      });
    });
    await act(async () => {
      source.emit('message', { data: chunk({ content: 'answer' }) });
    });

    expect(last(hook).isThinkingComplete).toBe(true);
    expect(last(hook).hasAutoCollapsed).toBe(true);
    expect(last(hook).isReasoningExpanded).toBe(false);

    // A later chunk must not re-collapse a panel the user re-opened.
    await act(async () => {
      source.emit('message', { data: chunk({ content: ' more' }) });
    });
    expect(last(hook).content).toBe('answer more');
    expect(last(hook).hasAutoCollapsed).toBe(true);
  });

  it('treats a closed think tag in the content stream as end of thinking', async () => {
    const hook = harness([assistant({ content: '<think>weighing' })]);
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: '</think>final' }) });
    });

    expect(last(hook).content).toBe('<think>weighing</think>final');
    expect(last(hook).isThinkingComplete).toBe(true);
    expect(last(hook).isReasoningExpanded).toBe(false);
  });

  it('ignores a delta with no recognised fields', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ role: 'assistant' }) });
    });

    expect(last(hook).content).toBe('');
    expect(last(hook).status).toBe(MESSAGE_STATUS.LOADING);
    // The raw frame is still recorded for the debug pane.
    expect(hook.result.current.debugData.sseMessages).toHaveLength(1);
  });

  it('refuses to append to a message that already failed', async () => {
    const hook = harness([
      assistant({ status: MESSAGE_STATUS.ERROR, content: 'boom' }),
    ]);
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'late token' }) });
    });

    expect(last(hook).content).toBe('boom');
  });

  it('refuses to append when the tail is a user message', async () => {
    const hook = harness([{ id: 'u1', role: 'user', content: 'hi' }]);
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'stray' }) });
    });

    expect(last(hook).content).toBe('hi');
  });

  it('surfaces an unparsable frame as an error on the message', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: '{not json' });
    });
    await flushMicroTimers();

    expect(last(hook).content).toContain('解析响应数据时发生错误');
    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(hook.result.current.debugData.response).toContain('解析错误');
    expect(hook.result.current.debugData.isStreaming).toBe(false);
    // The raw frame is preserved so the operator can see what arrived.
    expect(hook.result.current.debugData.sseMessages).toEqual(['{not json']);
  });
});

describe('useApiRequest — stream termination', () => {
  it('completes the message and releases the connection on [DONE]', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'done text' }) });
      source.emit('message', { data: '[DONE]' });
    });
    await flushMicroTimers();

    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(last(hook).isThinkingComplete).toBe(true);
    expect(source.closeCount).toBe(1);
    expect(hook.result.current.sseSourceRef.current).toBeNull();
    expect(hook.result.current.debugData.isStreaming).toBe(false);
    expect(hook.result.current.debugData.sseMessages).toContain('[DONE]');
    expect(hook.result.current.debugData.response).toContain('done text');
  });

  it('persists the completed conversation exactly once', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'saved' }) });
      source.emit('message', { data: '[DONE]' });
    });
    await flushMicroTimers();

    expect(saveMessages).toHaveBeenCalledTimes(1);
    const saved = saveMessages.mock.calls[0][0];
    expect(saved[saved.length - 1].content).toBe('saved');
    expect(saved[saved.length - 1].status).toBe(MESSAGE_STATUS.COMPLETE);
  });

  it('marks a mid-stream transport error on the message and closes the source', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'partial' }) });
    });
    await act(async () => {
      source.emit('error', { data: 'upstream reset' });
    });
    await flushMicroTimers();

    expect(last(hook).content).toBe('partialupstream reset');
    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(source.closeCount).toBe(1);
    expect(hook.result.current.sseSourceRef.current).toBeNull();
    expect(hook.result.current.debugData.response).toContain('SSE Error');
  });

  it('falls back to a generic message when the error frame carries no text', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('error', {});
    });

    expect(last(hook).content).toBe('请求发生错误');
    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
  });

  it('ignores an error frame that arrives after a clean [DONE]', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'all good' }) });
      source.emit('message', { data: '[DONE]' });
    });
    await flushMicroTimers();
    const responseBefore = hook.result.current.debugData.response;

    await act(async () => {
      source.emit('error', { data: 'late failure' });
    });

    expect(last(hook).content).toBe('all good');
    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(hook.result.current.debugData.response).toBe(responseBefore);
  });

  it('ignores an error frame once the source is already closed', async () => {
    const hook = harness();
    const source = await startStream(hook);
    source.readyState = 2;

    await act(async () => {
      source.emit('error', { data: 'post-close noise' });
    });

    expect(last(hook).status).toBe(MESSAGE_STATUS.LOADING);
    expect(last(hook).content).toBe('');
  });

  it('reports a non-200 handshake through readystatechange', async () => {
    const hook = harness();
    const source = await startStream(hook);
    source.status = 401;

    await act(async () => {
      source.emit('readystatechange', { readyState: 2 });
    });
    await flushMicroTimers();

    expect(last(hook).content).toContain('连接已断开');
    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(source.closeCount).toBe(1);
    expect(hook.result.current.debugData.response).toContain('HTTP Error');
  });

  it('leaves a healthy 200 readystatechange alone', async () => {
    const hook = harness();
    const source = await startStream(hook);
    source.status = 200;

    await act(async () => {
      source.emit('readystatechange', { readyState: 2 });
    });

    expect(last(hook).status).toBe(MESSAGE_STATUS.LOADING);
    expect(source.closeCount).toBe(0);
  });

  // DEFECT (see report): completeMessage dereferences the tail of the message
  // list without the emptiness guard streamMessageUpdate/onStopGenerator both
  // have. Clearing the conversation while a stream is in flight makes the
  // [DONE] frame throw inside the state updater. Un-skip once guarded.
  it.skip('survives a [DONE] that arrives after the conversation was cleared', async () => {
    const hook = harness([]);
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: '[DONE]' });
    });

    expect(hook.result.current.message).toEqual([]);
  });
});

describe('useApiRequest — stop generating', () => {
  it('closes the live connection and freezes the partial answer', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', { data: chunk({ content: 'half a sen' }) });
    });
    await act(async () => {
      hook.result.current.onStopGenerator();
    });
    await flushMicroTimers();

    expect(source.closeCount).toBe(1);
    expect(hook.result.current.sseSourceRef.current).toBeNull();
    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(last(hook).content).toBe('half a sen');
    expect(saveMessages).toHaveBeenCalledTimes(1);
  });

  it('strips an unterminated think block from the frozen content', async () => {
    const hook = harness();
    const source = await startStream(hook);

    await act(async () => {
      source.emit('message', {
        data: chunk({ content: 'answer<think>unfinis' }),
      });
    });
    await act(async () => {
      hook.result.current.onStopGenerator();
    });

    expect(last(hook).content).toBe('answer');
  });

  it('normalises empty reasoning to null when stopping', async () => {
    const hook = harness();
    await startStream(hook);

    await act(async () => {
      hook.result.current.onStopGenerator();
    });
    await flushMicroTimers();

    expect(last(hook).reasoningContent).toBeNull();
    expect(saveMessages).toHaveBeenCalledTimes(1);
  });

  it('is a no-op on an already completed message', async () => {
    const hook = harness([
      assistant({ status: MESSAGE_STATUS.COMPLETE, content: 'final' }),
    ]);

    await act(async () => {
      hook.result.current.onStopGenerator();
    });
    await flushMicroTimers();

    expect(last(hook).content).toBe('final');
    expect(saveMessages).not.toHaveBeenCalled();
  });

  it('is a no-op on an empty conversation', async () => {
    const hook = harness([]);

    await act(async () => {
      hook.result.current.onStopGenerator();
    });

    expect(hook.result.current.message).toEqual([]);
    expect(saveMessages).not.toHaveBeenCalled();
  });

  it('works with no connection open at all', async () => {
    const hook = harness();

    await act(async () => {
      hook.result.current.onStopGenerator();
    });
    await flushMicroTimers();

    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(saveMessages).toHaveBeenCalledTimes(1);
  });
});

describe('useApiRequest — non-streaming requests', () => {
  it('applies the assistant answer and both reasoning field spellings', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        choices: [{ message: { content: 'the answer', reasoning: 'why' } }],
      }),
    });

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).content).toBe('the answer');
    expect(last(hook).reasoningContent).toBe('why');
    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(hook.result.current.activeDebugTab).toBe(DEBUG_TABS.RESPONSE);
    expect(hook.result.current.debugData.isStreaming).toBe(false);
  });

  it('prefers reasoning_content over the short spelling', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        choices: [
          {
            message: {
              content: 'a',
              reasoning_content: 'long form',
              reasoning: 'short form',
            },
          },
        ],
      }),
    });

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).reasoningContent).toBe('long form');
  });

  it('turns a non-2xx response into an error bubble carrying the body', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 429,
      text: async () => 'rate limited',
      json: async () => ({}),
    });

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(last(hook).content).toContain('429');
    expect(last(hook).content).toContain('rate limited');
  });

  it('still reports the failure when the error body cannot be read', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      text: async () => {
        throw new Error('stream already consumed');
      },
    });

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(last(hook).content).toContain('无法读取错误响应体');
  });

  it('turns a transport failure into an error bubble', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockRejectedValue(new Error('network unreachable'));

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).status).toBe(MESSAGE_STATUS.ERROR);
    expect(last(hook).content).toBe('请求发生错误: network unreachable');
    expect(hook.result.current.debugData.response).toContain(
      'network unreachable',
    );
  });

  it('leaves an already completed tail untouched on error', async () => {
    const hook = harness([
      assistant({ status: MESSAGE_STATUS.COMPLETE, content: 'old' }),
    ]);
    global.fetch = vi.fn().mockRejectedValue(new Error('boom'));

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).content).toBe('old');
    expect(last(hook).status).toBe(MESSAGE_STATUS.COMPLETE);
  });

  // DEFECT (see report): a 200 response that carries no `choices` (an upstream
  // that reports errors in the body) leaves the assistant bubble pinned in
  // LOADING forever — the spinner never resolves and nothing is saved.
  it.skip('resolves the bubble when a 200 response carries no choices', async () => {
    const hook = harness();
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ error: { message: 'model overloaded' } }),
    });

    await act(async () => {
      await hook.result.current.sendRequest({ model: 'gpt-4o' }, false);
    });

    expect(last(hook).status).not.toBe(MESSAGE_STATUS.LOADING);
  });
});
