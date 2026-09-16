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
import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), delete: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

// ConfirmDialog stub — renders visible modal with confirm + cancel.
vi.mock('../../../components/common/ConfirmDialog', () => ({
  default: ({ visible, title, onConfirm, onCancel }) =>
    visible
      ? React.createElement(
          'div',
          { 'data-testid': 'confirm-dialog' },
          React.createElement('span', null, title),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-dialog-confirm', onClick: onConfirm },
            'confirm',
          ),
          React.createElement(
            'button',
            { 'data-testid': 'confirm-dialog-cancel', onClick: onCancel },
            'cancel',
          ),
        )
      : null,
}));

vi.mock('../../../components/hifi/HFShell', () => ({
  default: ({ children, actions }) =>
    React.createElement(
      'div',
      { 'data-testid': 'hf-shell' },
      React.createElement('div', null, actions),
      children,
    ),
}));

// Mirror i18next's en behaviour: return the English defaultValue (2nd arg)
// with {{var}} interpolation, falling back to the key when no default given.
vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key, fallback, opts) => {
      const vars =
        typeof fallback === 'object' && fallback !== null ? fallback : opts;
      let out = typeof fallback === 'string' ? fallback : key;
      if (vars) {
        for (const [k, v] of Object.entries(vars)) {
          out = out.split('{{' + k + '}}').join(String(v));
        }
      }
      return out;
    },
  }),
  initReactI18next: { type: '3rdParty', init: () => {} },
}));

import HFChat from './index';
import { API, showError, showSuccess } from '../../../helpers';

beforeEach(() => {
  API.get.mockReset();
  API.post.mockReset();
  API.patch.mockReset();
  API.delete.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  // The slug the browser was actually given — the page must read it from
  // storage, not from the (segment-less) /console/v2/chat route.
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');

  // Deterministic defaults for calls a given test doesn't otherwise care
  // about — every send() now ALSO calls the sessions API (persistSession),
  // so a test that only cares about the /chat/send turn still needs the
  // trailing persistence call to resolve to something, not vi.fn()'s bare
  // `undefined`.
  API.get.mockResolvedValue({
    data: { success: true, data: { sessions: [] } },
  });
  API.post.mockResolvedValue({
    data: {
      success: true,
      data: { id: 101, title: '', model: 'gpt-4o', messages: [] },
    },
  });
  API.patch.mockResolvedValue({
    data: {
      success: true,
      data: { id: 101, title: '', model: 'gpt-4o', messages: [] },
    },
  });
  API.delete.mockResolvedValue({ data: { success: true } });
});

const chatResponse = (content, latencyMs = 120) => ({
  data: {
    success: true,
    data: {
      message: { role: 'assistant', content },
      usage: { prompt_tokens: 8, completion_tokens: 15 },
      latency_ms: latencyMs,
    },
  },
});

// Routes API.post calls by endpoint suffix: /chat/send and the sessions
// persistence create (POST /chat/sessions) share the same axios method, so
// a plain FIFO mockResolvedValueOnce queue would hand a canned chat reply
// to whichever call happens to land next — including the persistence call
// interleaved between two turns. This queues chat replies against ONLY
// the /chat/send calls; any other POST (the sessions create) gets an
// auto-incrementing session id instead.
const mockChatSendSequence = (...replies) => {
  const queue = [...replies];
  let nextSessionId = 101;
  API.post.mockImplementation((url) => {
    if (url.endsWith('/chat/send')) {
      return Promise.resolve(chatResponse(queue.shift()));
    }
    return Promise.resolve({
      data: {
        success: true,
        data: { id: nextSessionId++, title: '', model: 'gpt-4o', messages: [] },
      },
    });
  });
};

describe('Chat page', () => {
  // 1. Empty state — no messages, prompt to start, send disabled when
  //    input empty.
  it('renders empty state and disables send on blank input', () => {
    render(<HFChat />);
    expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    const sendBtn = screen.getByText(/▶ send/i).closest('button');
    expect(sendBtn?.disabled).toBe(true);
  });

  // 1b. The page used to ship two WIPBanner renders claiming the whole
  //     page was "design-mock only" — false, /chat/send always ran a real
  //     completion. Neither banner exists anymore.
  it('renders no WIPBanner anywhere on the page', async () => {
    render(<HFChat />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByText(/deferred to v3/i)).toBeNull();
    expect(screen.queryByText(/design-mock/i)).toBeNull();
    expect(
      screen.queryByRole('status', { name: /work in progress/i }),
    ).toBeNull();
  });

  // 2. Send one turn → user message appears optimistically, then assistant
  //    response renders, and the full conversation is preserved. The turn
  //    is also SAVED (POST .../chat/sessions) once it completes.
  it('sends a turn, renders both messages, and persists the session', async () => {
    API.post.mockResolvedValueOnce(chatResponse('hi from gpt-4o'));
    render(<HFChat />);

    const input = screen.getByLabelText('message-input');
    fireEvent.change(input, { target: { value: 'hello' } });
    const sendBtn = screen.getByText(/▶ send/i).closest('button');
    fireEvent.click(sendBtn);

    // Optimistic user message — same text also shows in sidebar title +
    // main title (both derive from messages[0]), so assert >= 1 occurrence.
    await waitFor(() => {
      expect(screen.getAllByText('hello').length).toBeGreaterThan(0);
    });
    // Assistant response after the promise resolves.
    await waitFor(() => {
      expect(screen.getByText(/hi from gpt-4o/i)).toBeTruthy();
    });

    // API.post called twice: the completion, then the session-create save.
    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(2));
    const [sendUrl, sendPayload] = API.post.mock.calls[0];
    expect(sendUrl).toBe('/api/v2/acme/chat/send');
    expect(sendPayload.model).toBe('gpt-4o');
    expect(sendPayload.messages).toEqual([{ role: 'user', content: 'hello' }]);

    const [persistUrl, persistPayload] = API.post.mock.calls[1];
    expect(persistUrl).toBe('/api/v2/acme/chat/sessions');
    expect(persistPayload.messages).toEqual([
      { role: 'user', content: 'hello' },
      { role: 'assistant', content: 'hi from gpt-4o' },
    ]);
  });

  // 2b. Regression: /console/v2/chat is a static route with no :tenant_slug
  //     segment, so deriving the slug from useParams() posted every message to
  //     /api/v2/default/chat/send — a slug no tenant owns, so TenantSlugGuard
  //     404'd the whole page. The slug must come from the stored tenant.
  it('posts to the stored tenant slug, never the literal default', async () => {
    window.localStorage.setItem('tenant_slug', 'lurus');
    API.post.mockResolvedValueOnce(chatResponse('pong'));
    render(<HFChat />);

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'ping' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(2));
    const urls = API.post.mock.calls.map(([u]) => u);
    expect(urls).toEqual([
      '/api/v2/lurus/chat/send',
      '/api/v2/lurus/chat/sessions',
    ]);
    expect(urls.some((u) => u.includes('/api/v2/default/'))).toBe(false);
  });

  // 3. Multi-turn — second send forwards the *full* history (user + asst +
  //    user) to the backend. The FIRST turn's save creates a session
  //    (POST); the SECOND turn's save updates that same session (PATCH) —
  //    proving persistSession switches from create to update once an id
  //    exists, not "always POST".
  it('forwards full message history on subsequent turns and switches save from POST to PATCH', async () => {
    mockChatSendSequence('first reply', 'second reply');
    render(<HFChat />);

    const input = screen.getByLabelText('message-input');
    fireEvent.change(input, { target: { value: 'hi' } });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() => expect(screen.getByText(/first reply/i)).toBeTruthy());
    await waitFor(() =>
      expect(
        API.post.mock.calls.filter(([u]) => u.endsWith('/chat/sessions')),
      ).toHaveLength(1),
    );

    fireEvent.change(input, { target: { value: 'follow up' } });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() => expect(screen.getByText(/second reply/i)).toBeTruthy());

    const sendCalls = API.post.mock.calls.filter(([u]) =>
      u.endsWith('/chat/send'),
    );
    expect(sendCalls).toHaveLength(2);
    expect(sendCalls[1][1].messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'first reply' },
      { role: 'user', content: 'follow up' },
    ]);

    await waitFor(() => expect(API.patch).toHaveBeenCalledTimes(1));
    const [patchUrl, patchPayload] = API.patch.mock.calls[0];
    expect(patchUrl).toBe('/api/v2/acme/chat/sessions/101');
    expect(patchPayload.messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'first reply' },
      { role: 'user', content: 'follow up' },
      { role: 'assistant', content: 'second reply' },
    ]);
  });

  // 4. Failure path — backend 5xx rolls back the optimistic user message
  //    and surfaces a showError call. Without rollback a retry would
  //    double-send. A failed turn is never handed to persistSession.
  it('rolls back optimistic user message on send failure', async () => {
    API.post.mockRejectedValueOnce({
      response: { data: { message: 'upstream down' } },
    });
    render(<HFChat />);

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'will fail' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() => {
      expect(showError).toHaveBeenCalledWith('upstream down');
    });
    expect(screen.queryByText('will fail')).toBeNull();
    expect(API.post).toHaveBeenCalledTimes(1);
  });

  // 5. + new chat clears the in-memory conversation (the saved copy, if
  //    any, is left alone — it is a separate entry in `sessions`).
  it('clears messages when + new chat clicked', async () => {
    API.post.mockResolvedValueOnce(chatResponse('hi back'));
    render(<HFChat />);

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() => expect(screen.getByText(/hi back/i)).toBeTruthy());

    fireEvent.click(screen.getByText(/\+ new chat/i).closest('button'));
    expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    expect(screen.queryByText(/hi back/i)).toBeNull();
  });

  // 6. "⋯" button opens ConfirmDialog; confirming clears the conversation.
  it('opens ConfirmDialog on ⋯ click and clears messages on confirm', async () => {
    API.post.mockResolvedValueOnce(chatResponse('hello response'));
    render(<HFChat />);

    // Send a message so the conversation is non-empty.
    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'test msg' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() =>
      expect(screen.getByText(/hello response/i)).toBeTruthy(),
    );

    // ConfirmDialog not yet visible.
    expect(screen.queryByTestId('confirm-dialog')).toBeNull();

    // Click ⋯ — dialog should open.
    fireEvent.click(screen.getByTestId('chat-more-btn'));
    expect(screen.getByTestId('confirm-dialog')).toBeTruthy();

    // Confirm — conversation cleared.
    fireEvent.click(screen.getByTestId('confirm-dialog-confirm'));
    await waitFor(() => {
      expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    });
  });

  // 7. Sidebar loads real, persisted sessions from the server on mount —
  //    this is the "wire the sidebar to the new endpoints" requirement:
  //    before this cycle there was no GET call here at all.
  it('loads saved sessions from the server on mount', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessions: [
            { id: 5, title: 'earlier chat', model: 'gpt-4o', message_count: 4 },
          ],
        },
      },
    });
    render(<HFChat />);

    await waitFor(() => {
      expect(screen.getByText('earlier chat')).toBeTruthy();
    });
    expect(API.get).toHaveBeenCalledWith('/api/v2/acme/chat/sessions');
    expect(screen.queryByText(/no saved conversations yet/i)).toBeNull();
  });

  // 7b. Empty sidebar state is a plain message, not a WIPBanner.
  it('shows an empty-state message when there are no saved sessions', async () => {
    render(<HFChat />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.getByText(/no saved conversations yet/i)).toBeTruthy();
  });

  // 8. Clicking a saved session in the sidebar fetches and loads its full
  //    message history — the round trip's "fetch" half, driven from the UI.
  it('opens a saved session from the sidebar and loads its messages', async () => {
    API.get.mockImplementation((url) => {
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 7,
                  title: 'old convo',
                  model: 'gpt-4o',
                  message_count: 2,
                },
              ],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/7') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 7,
              title: 'old convo',
              model: 'gpt-4o',
              messages: [
                { role: 'user', content: 'what is Kyoto' },
                { role: 'assistant', content: 'a city in Japan' },
              ],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    render(<HFChat />);

    await waitFor(() => expect(screen.getByText('old convo')).toBeTruthy());
    fireEvent.click(screen.getByTestId('session-list-row-7'));

    await waitFor(() => {
      expect(screen.getByText('a city in Japan')).toBeTruthy();
    });
    // "what is Kyoto" renders twice (main header title + message body,
    // same as sessionTitle's other derivations elsewhere in this file).
    expect(screen.getAllByText('what is Kyoto').length).toBeGreaterThan(0);
  });

  // 9. Deleting a saved session calls DELETE and removes it from the
  //    sidebar — the round trip's "delete" half, driven from the UI.
  it('deletes a saved session from the sidebar', async () => {
    API.get.mockResolvedValueOnce({
      data: {
        success: true,
        data: {
          sessions: [
            { id: 9, title: 'to delete', model: 'gpt-4o', message_count: 1 },
          ],
        },
      },
    });
    render(<HFChat />);

    await waitFor(() => expect(screen.getByText('to delete')).toBeTruthy());
    fireEvent.click(screen.getByTestId('session-delete-9'));

    await waitFor(() => {
      expect(API.delete).toHaveBeenCalledWith('/api/v2/acme/chat/sessions/9');
    });
    await waitFor(() => {
      expect(screen.queryByText('to delete')).toBeNull();
    });
  });
});
