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
import { renderHook, act } from '@testing-library/react';

vi.mock('../../components/playground/configStorage', () => ({
  loadConfig: vi.fn(),
  saveConfig: vi.fn(),
  loadMessages: vi.fn(),
  saveMessages: vi.fn(),
}));

vi.mock('../../helpers', () => ({
  processIncompleteThinkTags: (content, reasoningContent) => ({
    content: content.replace(/<think>[\s\S]*$/, ''),
    reasoningContent,
  }),
}));

// The translator identity must be STABLE across renders: usePlaygroundState
// keeps `t` in an effect dependency list that calls setMessage, so a fresh
// function per render would spin forever. Real react-i18next hands back a
// stable t per language, and this mock mirrors that.
vi.mock('react-i18next', () => {
  const translation = { t: (key) => key };
  return { useTranslation: () => translation };
});

import { usePlaygroundState } from './usePlaygroundState';
import {
  loadConfig,
  loadMessages,
  saveConfig,
  saveMessages,
} from '../../components/playground/configStorage';
import {
  DEFAULT_CONFIG,
  MESSAGE_STATUS,
} from '../../constants/playground.constants';

const mount = () => renderHook(() => usePlaygroundState());

const flush = async () => {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
};

beforeEach(async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  vi.clearAllMocks();
  window.localStorage.clear();
  loadConfig.mockReturnValue({});
  loadMessages.mockReturnValue(null);
});

afterEach(() => {
  vi.useRealTimers();
});

describe('usePlaygroundState — initial config', () => {
  it('falls back to the shipped defaults when nothing was saved', () => {
    const { result } = mount();

    expect(result.current.inputs).toEqual(DEFAULT_CONFIG.inputs);
    expect(result.current.parameterEnabled).toEqual(
      DEFAULT_CONFIG.parameterEnabled,
    );
    expect(result.current.showDebugPanel).toBe(false);
    expect(result.current.customRequestMode).toBe(false);
    expect(result.current.customRequestBody).toBe('');
  });

  it('adopts the persisted config', () => {
    loadConfig.mockReturnValue({
      inputs: { model: 'claude-3', temperature: 0.1 },
      parameterEnabled: { temperature: false },
      showDebugPanel: true,
      customRequestMode: true,
      customRequestBody: '{"model":"claude-3"}',
    });

    const { result } = mount();

    expect(result.current.inputs).toEqual({
      model: 'claude-3',
      temperature: 0.1,
    });
    expect(result.current.showDebugPanel).toBe(true);
    expect(result.current.customRequestMode).toBe(true);
    expect(result.current.customRequestBody).toBe('{"model":"claude-3"}');
  });

  it('reads the persisted config exactly once', () => {
    const { rerender } = mount();
    rerender();
    rerender();

    expect(loadConfig).toHaveBeenCalledTimes(1);
    expect(loadMessages).toHaveBeenCalledTimes(1);
  });
});

describe('usePlaygroundState — initial messages', () => {
  it('starts from the shipped sample conversation when nothing was saved', () => {
    const { result } = mount();

    expect(result.current.message).toHaveLength(2);
    expect(result.current.message.map((m) => m.role)).toEqual([
      'user',
      'assistant',
    ]);
  });

  it('restores a saved conversation verbatim', () => {
    loadMessages.mockReturnValue([
      { id: '9', role: 'user', content: 'resume me' },
    ]);

    const { result } = mount();

    expect(result.current.message).toEqual([
      { id: '9', role: 'user', content: 'resume me' },
    ]);
  });

  it('evicts the legacy hard-coded sample conversation', () => {
    window.localStorage.setItem('playground_messages', 'legacy');
    loadMessages.mockReturnValue([
      { id: '2', role: 'user', content: '你好' },
      {
        id: '3',
        role: 'assistant',
        content: '你好，请问有什么可以帮助您的吗？',
      },
    ]);

    const { result } = mount();

    expect(window.localStorage.getItem('playground_messages')).toBeNull();
    // Falls back to the current localised sample rather than the stale one.
    expect(result.current.message[0].content).not.toBe('你好');
  });

  it('keeps a two-message conversation that merely reuses the sample ids', () => {
    window.localStorage.setItem('playground_messages', 'kept');
    loadMessages.mockReturnValue([
      { id: '2', role: 'user', content: 'my own prompt' },
      { id: '3', role: 'assistant', content: 'my own answer' },
    ]);

    const { result } = mount();

    expect(window.localStorage.getItem('playground_messages')).toBe('kept');
    expect(result.current.message[0].content).toBe('my own prompt');
  });
});

describe('usePlaygroundState — interrupted answer repair', () => {
  it('completes an answer left mid-stream by a page reload', async () => {
    loadMessages.mockReturnValue([
      { id: '1', role: 'user', content: 'hi' },
      {
        id: '2',
        role: 'assistant',
        content: 'half an answer<think>unterminated',
        status: MESSAGE_STATUS.INCOMPLETE,
      },
    ]);

    const { result } = mount();
    await flush();

    const tail = result.current.message[1];
    expect(tail.status).toBe(MESSAGE_STATUS.COMPLETE);
    expect(tail.content).toBe('half an answer');
    expect(tail.isThinkingComplete).toBe(true);
    expect(tail.reasoningContent).toBeNull();
    expect(saveMessages).toHaveBeenCalledTimes(1);
  });

  it('repairs a bubble still stuck on LOADING', async () => {
    loadMessages.mockReturnValue([
      {
        id: '2',
        role: 'assistant',
        content: 'partial',
        status: MESSAGE_STATUS.LOADING,
      },
    ]);

    const { result } = mount();
    await flush();

    expect(result.current.message[0].status).toBe(MESSAGE_STATUS.COMPLETE);
  });

  it('leaves a cleanly finished conversation alone', async () => {
    loadMessages.mockReturnValue([
      {
        id: '2',
        role: 'assistant',
        content: 'done',
        status: MESSAGE_STATUS.COMPLETE,
      },
    ]);

    const { result } = mount();
    await flush();

    expect(result.current.message[0].content).toBe('done');
    expect(saveMessages).not.toHaveBeenCalled();
  });

  it('does nothing for an empty saved conversation', async () => {
    loadMessages.mockReturnValue([]);

    const { result } = mount();
    await flush();

    // An empty array is falsy-equivalent for the loader, so the sample loads.
    expect(Array.isArray(result.current.message)).toBe(true);
    expect(saveMessages).not.toHaveBeenCalled();
  });
});

describe('usePlaygroundState — config edits', () => {
  it('patches a single input field', () => {
    const { result } = mount();

    act(() => result.current.handleInputChange('model', 'claude-3'));

    expect(result.current.inputs.model).toBe('claude-3');
    expect(result.current.inputs.temperature).toBe(
      DEFAULT_CONFIG.inputs.temperature,
    );
  });

  it('flips a single parameter toggle', () => {
    const { result } = mount();
    const before = result.current.parameterEnabled.max_tokens;

    act(() => result.current.handleParameterToggle('max_tokens'));

    expect(result.current.parameterEnabled.max_tokens).toBe(!before);
    expect(result.current.parameterEnabled.temperature).toBe(true);
  });

  it('collapses a burst of edits into one debounced save', async () => {
    vi.useFakeTimers();
    const { result } = mount();

    act(() => {
      result.current.debouncedSaveConfig();
      result.current.debouncedSaveConfig();
      result.current.debouncedSaveConfig();
    });
    expect(saveConfig).not.toHaveBeenCalled();

    act(() => vi.advanceTimersByTime(1000));

    expect(saveConfig).toHaveBeenCalledTimes(1);
    expect(saveConfig.mock.calls[0][0]).toEqual({
      inputs: DEFAULT_CONFIG.inputs,
      parameterEnabled: DEFAULT_CONFIG.parameterEnabled,
      showDebugPanel: false,
      customRequestMode: false,
      customRequestBody: '',
    });
  });

  it('does not save a config the user abandoned by leaving the page', async () => {
    vi.useFakeTimers();
    const { result, unmount } = mount();

    act(() => result.current.debouncedSaveConfig());
    unmount();
    act(() => vi.advanceTimersByTime(2000));

    expect(saveConfig).not.toHaveBeenCalled();
  });

  it('saves the messages it is handed, or the current ones', () => {
    const { result } = mount();

    act(() => result.current.saveMessagesImmediately());
    expect(saveMessages).toHaveBeenLastCalledWith(result.current.message);

    const explicit = [{ id: 'x', role: 'user', content: 'explicit' }];
    act(() => result.current.saveMessagesImmediately(explicit));
    expect(saveMessages).toHaveBeenLastCalledWith(explicit);
  });
});

describe('usePlaygroundState — import and reset', () => {
  it('merges an imported partial config over the current one', () => {
    const { result } = mount();

    act(() =>
      result.current.handleConfigImport({
        inputs: { model: 'gemini-2' },
        parameterEnabled: { seed: true },
      }),
    );

    expect(result.current.inputs.model).toBe('gemini-2');
    expect(result.current.inputs.temperature).toBe(
      DEFAULT_CONFIG.inputs.temperature,
    );
    expect(result.current.parameterEnabled.seed).toBe(true);
    expect(result.current.parameterEnabled.temperature).toBe(true);
  });

  it('imports the debug panel flag in both directions', () => {
    loadConfig.mockReturnValue({ showDebugPanel: true });
    const { result } = mount();

    act(() => result.current.handleConfigImport({ showDebugPanel: false }));
    expect(result.current.showDebugPanel).toBe(false);

    act(() => result.current.handleConfigImport({ showDebugPanel: true }));
    expect(result.current.showDebugPanel).toBe(true);
  });

  it('restores a conversation carried inside an imported config', () => {
    const { result } = mount();

    act(() =>
      result.current.handleConfigImport({
        messages: [{ id: '1', role: 'user', content: 'imported' }],
      }),
    );

    expect(result.current.message).toEqual([
      { id: '1', role: 'user', content: 'imported' },
    ]);
  });

  it('ignores a non-array messages field in an imported config', () => {
    const { result } = mount();
    const before = result.current.message;

    act(() => result.current.handleConfigImport({ messages: 'oops' }));

    expect(result.current.message).toBe(before);
  });

  // DEFECT (see report): only showDebugPanel gets the `typeof === 'boolean'`
  // treatment. customRequestMode is imported through a truthiness check, so a
  // config that switches custom-request mode OFF silently leaves it ON.
  it.skip('imports customRequestMode in both directions', () => {
    loadConfig.mockReturnValue({ customRequestMode: true });
    const { result } = mount();
    expect(result.current.customRequestMode).toBe(true);

    act(() => result.current.handleConfigImport({ customRequestMode: false }));

    expect(result.current.customRequestMode).toBe(false);
  });

  it('reset restores every default but keeps the conversation', () => {
    loadConfig.mockReturnValue({
      inputs: { model: 'claude-3' },
      showDebugPanel: true,
      customRequestMode: true,
      customRequestBody: '{"a":1}',
    });
    const { result } = mount();
    const conversation = result.current.message;

    act(() => result.current.handleConfigReset());

    expect(result.current.inputs).toEqual(DEFAULT_CONFIG.inputs);
    expect(result.current.showDebugPanel).toBe(false);
    expect(result.current.customRequestMode).toBe(false);
    expect(result.current.customRequestBody).toBe('');
    expect(result.current.message).toBe(conversation);
  });

  it('reset with resetMessages swaps in a fresh sample conversation', async () => {
    loadMessages.mockReturnValue([
      { id: '9', role: 'user', content: 'old thread' },
    ]);
    const { result } = mount();

    await act(async () => {
      result.current.handleConfigReset({ resetMessages: true });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    expect(result.current.message).toHaveLength(2);
    expect(result.current.message[0].content).not.toBe('old thread');
  });
});
