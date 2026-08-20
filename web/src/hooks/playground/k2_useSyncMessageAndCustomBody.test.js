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
import { renderHook, act } from '@testing-library/react';

import { useSyncMessageAndCustomBody } from './useSyncMessageAndCustomBody';

const setCustomRequestBody = vi.fn();
const setMessage = vi.fn();
const debouncedSaveConfig = vi.fn();

const INPUTS = { model: 'gpt-4o', temperature: 0.7, stream: true };

const mount = (props) =>
  renderHook(
    ({ customRequestMode, customRequestBody, message, inputs }) =>
      useSyncMessageAndCustomBody(
        customRequestMode,
        customRequestBody,
        message,
        inputs,
        setCustomRequestBody,
        setMessage,
        debouncedSaveConfig,
      ),
    {
      initialProps: {
        customRequestMode: true,
        customRequestBody: '{}',
        message: [],
        inputs: INPUTS,
        ...props,
      },
    },
  );

const written = () => JSON.parse(setCustomRequestBody.mock.calls.at(-1)[0]);

const flush = async () => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

beforeEach(async () => {
  // The hook defers debouncedSaveConfig by a 0ms timer; drain anything a
  // previous test left queued before resetting the spies.
  await new Promise((resolve) => setTimeout(resolve, 0));
  vi.clearAllMocks();
});

describe('syncMessageToCustomBody', () => {
  it('writes the conversation into the custom payload', async () => {
    const message = [
      { id: '1', role: 'user', content: 'hi' },
      { id: '2', role: 'assistant', content: 'hello' },
    ];
    const { result } = mount({
      message,
      customRequestBody: JSON.stringify({ model: 'claude-3', messages: [] }),
    });

    act(() => result.current.syncMessageToCustomBody());

    expect(written().messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'hello' },
    ]);
    // Everything else in the hand-written body survives.
    expect(written().model).toBe('claude-3');
  });

  it('drops per-message ui state that has no place in a request body', async () => {
    const message = [
      {
        id: '1',
        role: 'assistant',
        content: 'answer',
        status: 'complete',
        isReasoningExpanded: false,
        reasoningContent: 'private',
      },
    ];
    const { result } = mount({ message });

    act(() => result.current.syncMessageToCustomBody());

    expect(written().messages).toEqual([
      { role: 'assistant', content: 'answer' },
    ]);
  });

  it('rebuilds a default payload when the custom body is unparsable', async () => {
    const { result } = mount({
      message: [{ id: '1', role: 'user', content: 'hi' }],
      customRequestBody: 'not json at all',
    });

    act(() => result.current.syncMessageToCustomBody());

    const body = written();
    expect(body.model).toBe('gpt-4o');
    expect(body.temperature).toBe(0.7);
    expect(body.stream).toBe(true);
    expect(body.messages).toHaveLength(1);
  });

  it('treats stream as on unless it is explicitly false', async () => {
    const off = mount({
      message: [{ id: '1', role: 'user', content: 'hi' }],
      customRequestBody: 'broken',
      inputs: { model: 'm', temperature: 1, stream: false },
    });
    act(() => off.result.current.syncMessageToCustomBody());
    expect(written().stream).toBe(false);

    setCustomRequestBody.mockClear();
    const unset = mount({
      message: [{ id: '1', role: 'user', content: 'hi' }],
      customRequestBody: 'broken',
      inputs: { model: 'm', temperature: 1 },
    });
    act(() => unset.result.current.syncMessageToCustomBody());
    expect(written().stream).toBe(true);
  });

  it('does nothing at all outside custom request mode', async () => {
    const { result } = mount({
      customRequestMode: false,
      message: [{ id: '1', role: 'user', content: 'hi' }],
    });

    act(() => result.current.syncMessageToCustomBody());

    expect(setCustomRequestBody).not.toHaveBeenCalled();
    expect(debouncedSaveConfig).not.toHaveBeenCalled();
  });

  it('skips the write when the conversation has not changed', async () => {
    const message = [{ id: '1', role: 'user', content: 'hi' }];
    const { result } = mount({ message });

    act(() => result.current.syncMessageToCustomBody());
    expect(setCustomRequestBody).toHaveBeenCalledTimes(1);

    act(() => result.current.syncMessageToCustomBody());
    expect(setCustomRequestBody).toHaveBeenCalledTimes(1);
  });

  it('writes again once the conversation actually changes', async () => {
    const { result, rerender } = mount({
      message: [{ id: '1', role: 'user', content: 'hi' }],
    });
    act(() => result.current.syncMessageToCustomBody());

    rerender({
      customRequestMode: true,
      customRequestBody: '{}',
      inputs: INPUTS,
      message: [
        { id: '1', role: 'user', content: 'hi' },
        { id: '2', role: 'user', content: 'again' },
      ],
    });
    act(() => result.current.syncMessageToCustomBody());

    expect(setCustomRequestBody).toHaveBeenCalledTimes(2);
    expect(written().messages).toHaveLength(2);
  });

  it('persists the config after the write settles', async () => {
    const { result } = mount({
      message: [{ id: '1', role: 'user', content: 'hi' }],
    });

    act(() => result.current.syncMessageToCustomBody());
    expect(debouncedSaveConfig).not.toHaveBeenCalled();

    await flush();
    expect(debouncedSaveConfig).toHaveBeenCalledTimes(1);
  });
});

describe('syncCustomBodyToMessage', () => {
  it('rebuilds the conversation from the hand-written body', async () => {
    const { result } = mount({
      customRequestBody: JSON.stringify({
        messages: [
          { role: 'system', content: 'be terse' },
          { role: 'user', content: 'hi' },
        ],
      }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    const rebuilt = setMessage.mock.calls.at(-1)[0];
    expect(rebuilt.map((m) => m.role)).toEqual(['system', 'user']);
    expect(rebuilt.map((m) => m.content)).toEqual(['be terse', 'hi']);
  });

  it('assigns positional ids and defaults a missing role to user', async () => {
    const { result } = mount({
      customRequestBody: JSON.stringify({
        messages: [{ content: 'first' }, { id: 'keep-me', content: 'second' }],
      }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    const rebuilt = setMessage.mock.calls.at(-1)[0];
    expect(rebuilt[0].id).toBe('1');
    expect(rebuilt[0].role).toBe('user');
    expect(rebuilt[1].id).toBe('keep-me');
  });

  it('gives assistant turns the reasoning fields the renderer expects', async () => {
    const { result } = mount({
      customRequestBody: JSON.stringify({
        messages: [
          { role: 'assistant', content: 'a' },
          { role: 'user', content: 'b' },
        ],
      }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    const rebuilt = setMessage.mock.calls.at(-1)[0];
    expect(rebuilt[0].reasoningContent).toBe('');
    expect(rebuilt[0].isReasoningExpanded).toBe(false);
    expect(rebuilt[1].isReasoningExpanded).toBeUndefined();
  });

  it('substitutes an empty string for missing content', async () => {
    const { result } = mount({
      customRequestBody: JSON.stringify({ messages: [{ role: 'user' }] }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    expect(setMessage.mock.calls.at(-1)[0][0].content).toBe('');
  });

  it('ignores a body whose messages field is not an array', async () => {
    const { result } = mount({
      customRequestBody: JSON.stringify({ messages: 'oops' }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    expect(setMessage).not.toHaveBeenCalled();
  });

  it('swallows an unparsable body instead of crashing the editor', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { result } = mount({ customRequestBody: '{ "messages": [' });

    act(() => result.current.syncCustomBodyToMessage());

    expect(setMessage).not.toHaveBeenCalled();
    warnSpy.mockRestore();
  });

  it('does nothing at all outside custom request mode', async () => {
    const { result } = mount({
      customRequestMode: false,
      customRequestBody: JSON.stringify({
        messages: [{ role: 'user', content: 'hi' }],
      }),
    });

    act(() => result.current.syncCustomBodyToMessage());

    expect(setMessage).not.toHaveBeenCalled();
  });

  it('skips the rebuild when the messages block is unchanged', async () => {
    const body = JSON.stringify({
      messages: [{ role: 'user', content: 'hi' }],
    });
    const { result } = mount({ customRequestBody: body });

    act(() => result.current.syncCustomBodyToMessage());
    expect(setMessage).toHaveBeenCalledTimes(1);

    act(() => result.current.syncCustomBodyToMessage());
    expect(setMessage).toHaveBeenCalledTimes(1);
  });

  it('ignores edits that only touch fields outside the messages block', async () => {
    const { result, rerender } = mount({
      customRequestBody: JSON.stringify({
        model: 'a',
        messages: [{ role: 'user', content: 'hi' }],
      }),
    });
    act(() => result.current.syncCustomBodyToMessage());
    expect(setMessage).toHaveBeenCalledTimes(1);

    rerender({
      customRequestMode: true,
      message: [],
      inputs: INPUTS,
      customRequestBody: JSON.stringify({
        model: 'b',
        temperature: 0.1,
        messages: [{ role: 'user', content: 'hi' }],
      }),
    });
    act(() => result.current.syncCustomBodyToMessage());

    expect(setMessage).toHaveBeenCalledTimes(1);
  });
});

describe('loop protection between the two directions', () => {
  it('a round trip through the custom body does not re-enter the message sync', async () => {
    const message = [{ id: '1', role: 'user', content: 'hi' }];
    const { result } = mount({ message });

    act(() => result.current.syncMessageToCustomBody());
    const producedBody = setCustomRequestBody.mock.calls.at(-1)[0];
    setMessage.mockClear();

    // The editor now holds exactly what the message list produced, so pulling
    // it back must be recognised as a no-op rather than a fresh edit.
    const back = mount({ customRequestBody: producedBody, message });
    act(() => back.result.current.syncMessageToCustomBody());
    act(() => back.result.current.syncCustomBodyToMessage());

    expect(setMessage.mock.calls).toHaveLength(0);
  });
});
