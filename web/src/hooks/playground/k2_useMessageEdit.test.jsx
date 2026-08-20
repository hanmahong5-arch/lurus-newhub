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
import { useState } from 'react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('@douyinfe/semi-ui', () => ({
  Toast: { success: vi.fn(), error: vi.fn() },
  Modal: { confirm: vi.fn(), error: vi.fn() },
}));

vi.mock('../../helpers', () => ({
  getTextContent: (msg) =>
    Array.isArray(msg.content)
      ? (msg.content.find((c) => c.type === 'text')?.text ?? '')
      : msg.content,
  buildApiPayload: vi.fn((messages, _extra, inputs) => ({
    model: inputs.model,
    messages,
  })),
  createLoadingAssistantMessage: () => ({
    id: 'pending',
    role: 'assistant',
    content: '',
    status: 'loading',
  }),
}));

vi.mock('react-i18next', () => {
  const translation = { t: (key) => key };
  return { useTranslation: () => translation };
});

import { useMessageEdit } from './useMessageEdit';
import { Toast, Modal } from '@douyinfe/semi-ui';
import { buildApiPayload } from '../../helpers';

const sendRequest = vi.fn();
const saveMessages = vi.fn();
const INPUTS = { model: 'gpt-4o', stream: true };
const PARAMS = { temperature: true };

const harness = (initial) => {
  const hook = renderHook(() => {
    const [message, setMessage] = useState(initial);
    const api = useMessageEdit(
      setMessage,
      INPUTS,
      PARAMS,
      sendRequest,
      saveMessages,
    );
    return { message, ...api };
  });
  return hook;
};

const flush = async () => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 120));
  });
};

const userMsg = (id, content) => ({ id, role: 'user', content });
const assistantMsg = (id, content) => ({ id, role: 'assistant', content });

beforeEach(async () => {
  await new Promise((resolve) => setTimeout(resolve, 150));
  vi.clearAllMocks();
});

describe('useMessageEdit — entering edit mode', () => {
  it('loads the plain text of the target message into the editor', () => {
    const target = userMsg('1', 'original prompt');
    const { result } = harness([target]);

    act(() => result.current.handleMessageEdit(target));

    expect(result.current.editingMessageId).toBe('1');
    expect(result.current.editValue).toBe('original prompt');
  });

  it('extracts the text part out of a multimodal message', () => {
    const target = {
      id: '1',
      role: 'user',
      content: [
        { type: 'text', text: 'describe this' },
        { type: 'image_url', image_url: { url: 'https://x/y.png' } },
      ],
    };
    const { result } = harness([target]);

    act(() => result.current.handleMessageEdit(target));

    expect(result.current.editValue).toBe('describe this');
  });

  it('cancel clears the editor without touching the conversation', () => {
    const target = userMsg('1', 'original');
    const { result } = harness([target]);

    act(() => result.current.handleMessageEdit(target));
    act(() => result.current.handleEditCancel());

    expect(result.current.editingMessageId).toBeNull();
    expect(result.current.editValue).toBe('');
    expect(result.current.message[0].content).toBe('original');
    expect(saveMessages).not.toHaveBeenCalled();
  });
});

describe('useMessageEdit — saving', () => {
  it('does nothing when no message is being edited', async () => {
    const { result } = harness([userMsg('1', 'original')]);

    act(() => result.current.handleEditSave());
    await flush();

    expect(result.current.message[0].content).toBe('original');
    expect(saveMessages).not.toHaveBeenCalled();
    expect(Toast.success).not.toHaveBeenCalled();
  });

  it('refuses to save a blank edit', async () => {
    const target = userMsg('1', 'original');
    const { result } = harness([target]);

    act(() => result.current.handleMessageEdit(target));
    act(() => result.current.setEditValue('    '));
    act(() => result.current.handleEditSave());
    await flush();

    expect(result.current.message[0].content).toBe('original');
    expect(saveMessages).not.toHaveBeenCalled();
  });

  it('replaces plain string content and trims it', async () => {
    const target = assistantMsg('2', 'old answer');
    const { result } = harness([userMsg('1', 'q'), target]);

    act(() => result.current.handleMessageEdit(target));
    act(() => result.current.setEditValue('  new answer  '));
    act(() => result.current.handleEditSave());
    await flush();

    expect(result.current.message[1].content).toBe('new answer');
    expect(result.current.editingMessageId).toBeNull();
    expect(result.current.editValue).toBe('');
    expect(Toast.success).toHaveBeenCalledTimes(1);
    expect(saveMessages).toHaveBeenCalledTimes(1);
    expect(saveMessages.mock.calls[0][0][1].content).toBe('new answer');
  });

  it('rewrites only the text part of a multimodal message', async () => {
    const target = {
      id: '2',
      role: 'assistant',
      content: [
        { type: 'text', text: 'old caption' },
        { type: 'image_url', image_url: { url: 'https://x/y.png' } },
      ],
    };
    const { result } = harness([userMsg('1', 'q'), target]);

    act(() => result.current.handleMessageEdit(target));
    act(() => result.current.setEditValue('new caption'));
    act(() => result.current.handleEditSave());
    await flush();

    expect(result.current.message[1].content).toEqual([
      { type: 'text', text: 'new caption' },
      { type: 'image_url', image_url: { url: 'https://x/y.png' } },
    ]);
  });

  it('edits the last user turn in place when nothing follows it', async () => {
    const target = userMsg('2', 'second question');
    const { result } = harness([
      userMsg('1', 'first'),
      assistantMsg('1a', 'answer'),
      target,
    ]);

    act(() => result.current.handleMessageEdit(target));
    act(() => result.current.setEditValue('second question, revised'));
    act(() => result.current.handleEditSave());
    await flush();

    expect(Modal.confirm).not.toHaveBeenCalled();
    expect(result.current.message).toHaveLength(3);
    expect(result.current.message[2].content).toBe('second question, revised');
    expect(sendRequest).not.toHaveBeenCalled();
  });
});

describe('useMessageEdit — editing a question that already has an answer', () => {
  const setup = async () => {
    const target = userMsg('1', 'original question');
    const hook = harness([target, assistantMsg('2', 'stale answer')]);

    act(() => hook.result.current.handleMessageEdit(target));
    act(() => hook.result.current.setEditValue('revised question'));
    act(() => hook.result.current.handleEditSave());
    await flush();

    return hook;
  };

  it('asks before discarding the stale answer and changes nothing yet', async () => {
    const hook = await setup();

    expect(Modal.confirm).toHaveBeenCalledTimes(1);
    expect(hook.result.current.message[0].content).toBe('original question');
    expect(hook.result.current.message).toHaveLength(2);
    expect(saveMessages).not.toHaveBeenCalled();
  });

  it('regenerating truncates after the question and fires a fresh request', async () => {
    const hook = await setup();
    const { onOk } = Modal.confirm.mock.calls[0][0];

    await act(async () => {
      onOk();
      await new Promise((resolve) => setTimeout(resolve, 150));
    });

    // Stale answer dropped, revised question kept, new loading bubble added.
    expect(hook.result.current.message.map((m) => m.id)).toEqual([
      '1',
      'pending',
    ]);
    expect(hook.result.current.message[0].content).toBe('revised question');

    expect(buildApiPayload).toHaveBeenCalledTimes(1);
    const [messagesSent, , inputsSent, paramsSent] =
      buildApiPayload.mock.calls[0];
    expect(messagesSent).toHaveLength(1);
    expect(messagesSent[0].content).toBe('revised question');
    expect(inputsSent).toBe(INPUTS);
    expect(paramsSent).toBe(PARAMS);

    expect(sendRequest).toHaveBeenCalledTimes(1);
    expect(sendRequest.mock.calls[0][1]).toBe(true); // stream flag
    // The truncated thread is what gets persisted.
    expect(saveMessages.mock.calls[0][0]).toHaveLength(1);
  });

  it('save-only keeps the stale answer and sends nothing', async () => {
    const hook = await setup();
    const { onCancel } = Modal.confirm.mock.calls[0][0];

    await act(async () => {
      onCancel();
      await new Promise((resolve) => setTimeout(resolve, 150));
    });

    expect(hook.result.current.message).toHaveLength(2);
    expect(hook.result.current.message[0].content).toBe('revised question');
    expect(hook.result.current.message[1].content).toBe('stale answer');
    expect(sendRequest).not.toHaveBeenCalled();
    expect(saveMessages).toHaveBeenCalledTimes(1);
    expect(saveMessages.mock.calls[0][0]).toHaveLength(2);
  });
});
