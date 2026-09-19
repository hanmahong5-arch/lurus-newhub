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

// The single routable model every test gets unless it overrides the mock —
// a neutral fixture id, not a vendor model name (this cycle's fixtures use
// rt-* names, matching internal/adapter/handler/v2_models_routable_test.go
// on the Go side).
const ROUTABLE_DEFAULT = [
  { id: 'rt-alpha', owned_by: 'custom', supported_endpoint_types: ['openai'] },
];

const routableResponse = (items = ROUTABLE_DEFAULT) => ({
  data: { success: true, data: { items } },
});

// GET now serves two different concerns from the same mocked function
// (routable models AND chat sessions) — every test's own
// API.get.mockImplementation must route on URL, never rely on call order,
// since useRoutableModels' effect and loadSessions' effect both fire on
// mount and a plain FIFO queue could hand either one's answer to the wrong
// caller.
const wireGet = (sessionsHandler) => {
  API.get.mockImplementation((url) => {
    if (String(url).includes('/models/routable')) {
      return Promise.resolve(routableResponse());
    }
    return sessionsHandler(url);
  });
};

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
  // `undefined`. Routed by URL (see wireGet) so it also answers the new
  // /models/routable fetch with a single routable model.
  wireGet(() =>
    Promise.resolve({ data: { success: true, data: { sessions: [] } } }),
  );
  API.post.mockResolvedValue({
    data: {
      success: true,
      data: { id: 101, title: '', model: 'rt-alpha', messages: [] },
    },
  });
  API.patch.mockResolvedValue({
    data: {
      success: true,
      data: { id: 101, title: '', model: 'rt-alpha', messages: [] },
    },
  });
  API.delete.mockResolvedValue({ data: { success: true } });
});

// The model picker's default pick is now async (useRoutableModels' fetch),
// where it used to be a synchronous literal — tests that fire input/send
// events immediately after render() must first wait for the picker to have
// resolved, or send() silently no-ops on its `!model` guard.
const waitForModelReady = () =>
  waitFor(() =>
    expect(screen.getByTestId('chat-model-select').value).toBe('rt-alpha'),
  );

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
        data: {
          id: nextSessionId++,
          title: '',
          model: 'rt-alpha',
          messages: [],
        },
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
    API.post.mockResolvedValueOnce(chatResponse('hi from rt-alpha'));
    render(<HFChat />);
    await waitForModelReady();

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
      expect(screen.getByText(/hi from rt-alpha/i)).toBeTruthy();
    });

    // API.post called twice: the completion, then the session-create save.
    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(2));
    const [sendUrl, sendPayload] = API.post.mock.calls[0];
    expect(sendUrl).toBe('/api/v2/acme/chat/send');
    expect(sendPayload.model).toBe('rt-alpha');
    expect(sendPayload.messages).toEqual([{ role: 'user', content: 'hello' }]);

    const [persistUrl, persistPayload] = API.post.mock.calls[1];
    expect(persistUrl).toBe('/api/v2/acme/chat/sessions');
    expect(persistPayload.messages).toEqual([
      { role: 'user', content: 'hello' },
      { role: 'assistant', content: 'hi from rt-alpha' },
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
    await waitForModelReady();

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
    await waitForModelReady();

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
    // updateChatSessionRequest (backend, v2_chat_session.go) now accepts an
    // optional model — the page sends the currently-selected model on every
    // PATCH (L1, cycle-11), not just on create.
    expect(patchPayload.model).toBe('rt-alpha');
  });

  // 4. Failure path — backend 5xx rolls back the optimistic user message
  //    and surfaces a showError call. Without rollback a retry would
  //    double-send. A failed turn is never handed to persistSession.
  it('rolls back optimistic user message on send failure', async () => {
    API.post.mockRejectedValueOnce({
      response: { data: { message: 'upstream down' } },
    });
    render(<HFChat />);
    await waitForModelReady();

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
    await waitForModelReady();

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
    await waitForModelReady();

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
    // wireGet, not mockResolvedValueOnce: useRoutableModels' effect and
    // loadSessions' effect both fire on mount, so a blind "next call"
    // override could land on either one — must route by URL.
    wireGet(() =>
      Promise.resolve({
        data: {
          success: true,
          data: {
            sessions: [
              {
                id: 5,
                title: 'earlier chat',
                model: 'rt-alpha',
                message_count: 4,
              },
            ],
          },
        },
      }),
    );
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
    wireGet((url) => {
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 7,
                  title: 'old convo',
                  model: 'rt-alpha',
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
              model: 'rt-alpha',
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
    wireGet(() =>
      Promise.resolve({
        data: {
          success: true,
          data: {
            sessions: [
              {
                id: 9,
                title: 'to delete',
                model: 'rt-alpha',
                message_count: 1,
              },
            ],
          },
        },
      }),
    );
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

  // 10. GENERATION TOKEN regression: switching to a different saved session
  //     while a turn is in flight must drop that turn's response entirely
  //     — no render into the new conversation, no persistSession call at
  //     all. Without the generation-token guard, conversation A's history
  //     would land in a PATCH against session B, replacing whatever B had
  //     stored (repo.UpdateChatSessionOwned is a delete-then-reinsert, so
  //     that loss has no undo).
  it('drops an in-flight turn and never persists it when the user switches sessions mid-flight', async () => {
    wireGet((url) => {
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 1,
                  title: 'session one',
                  model: 'rt-alpha',
                  message_count: 1,
                },
                {
                  id: 2,
                  title: 'session two',
                  model: 'rt-alpha',
                  message_count: 2,
                },
              ],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/1') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 1,
              title: 'session one',
              model: 'rt-alpha',
              messages: [{ role: 'user', content: 'A-user' }],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/2') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 2,
              title: 'session two',
              model: 'rt-alpha',
              messages: [
                { role: 'user', content: 'B-user' },
                { role: 'assistant', content: 'B-asst' },
              ],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });

    let resolveChatSend;
    API.post.mockImplementation((url) => {
      if (url.endsWith('/chat/send')) {
        return new Promise((resolve) => {
          resolveChatSend = resolve;
        });
      }
      // Both sessions in this test already exist — a create call here
      // would itself be the bug under test (persisting the dropped turn).
      return Promise.reject(new Error('unexpected session create'));
    });

    render(<HFChat />);
    await waitFor(() => expect(screen.getByText('session one')).toBeTruthy());

    fireEvent.click(screen.getByTestId('session-list-row-1'));
    await waitFor(() =>
      expect(screen.getAllByText('A-user').length).toBeGreaterThan(0),
    );

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'A-follow' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() =>
      expect(screen.getAllByText('A-follow').length).toBeGreaterThan(0),
    );

    // Switch away WHILE the turn above is still in flight — the sidebar
    // row must remain clickable during a pending send (a slow turn must
    // not lock navigation).
    fireEvent.click(screen.getByTestId('session-list-row-2'));
    await waitFor(() =>
      expect(screen.getAllByText('B-asst').length).toBeGreaterThan(0),
    );
    expect(screen.queryByText(/A-follow/i)).toBeNull();

    // Now let session 1's turn resolve.
    resolveChatSend(chatResponse('A-reply'));
    await new Promise((resolve) => setTimeout(resolve, 0));

    // Session 2 is untouched: A's reply never rendered into it, and no
    // save call (POST create or PATCH) ever fired for the dropped turn.
    expect(screen.queryByText(/A-reply/i)).toBeNull();
    expect(screen.getAllByText('B-user').length).toBeGreaterThan(0);
    expect(screen.getAllByText('B-asst').length).toBeGreaterThan(0);
    expect(API.patch).not.toHaveBeenCalled();
  });

  // 11. persistSession must distinguish a 404 (session gone server-side)
  //     from a transient failure: on 404, the NEXT turn falls back to
  //     POST (create) instead of re-PATCHing the now-nonexistent id
  //     forever, and the swallowed failure surfaces as a visible
  //     "not saved" marker rather than vanishing silently.
  it('falls back to creating a new session after a 404 on PATCH, and shows a not-saved marker', async () => {
    wireGet((url) => {
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 55,
                  title: 'gone elsewhere',
                  model: 'rt-alpha',
                  message_count: 1,
                },
              ],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/55') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 55,
              title: 'gone elsewhere',
              model: 'rt-alpha',
              messages: [{ role: 'user', content: 'old turn' }],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    API.patch.mockRejectedValueOnce({ response: { status: 404 } });
    API.post.mockImplementation((url) => {
      if (url.endsWith('/chat/send')) {
        return Promise.resolve(chatResponse('reply after 404'));
      }
      // The create call the post-404 fallback should trigger.
      return Promise.resolve({
        data: {
          success: true,
          data: { id: 999, title: '', model: 'rt-alpha', messages: [] },
        },
      });
    });

    render(<HFChat />);
    await waitFor(() =>
      expect(screen.getByText('gone elsewhere')).toBeTruthy(),
    );
    fireEvent.click(screen.getByTestId('session-list-row-55'));
    await waitFor(() =>
      expect(screen.getAllByText('old turn').length).toBeGreaterThan(0),
    );

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'first after reopen' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() =>
      expect(screen.getByText(/reply after 404/i)).toBeTruthy(),
    );

    // The PATCH was attempted against the (now-gone) session and 404'd.
    await waitFor(() => expect(API.patch).toHaveBeenCalledTimes(1));
    expect(API.patch.mock.calls[0][0]).toBe('/api/v2/acme/chat/sessions/55');

    // The swallowed failure is NOT invisible.
    await waitFor(() =>
      expect(screen.getByTestId('chat-save-failed')).toBeTruthy(),
    );

    // The next turn must NOT retry PATCHing the dead id — it creates a
    // fresh session instead.
    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'second after 404' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() =>
      expect(
        API.post.mock.calls.filter(([u]) => u.endsWith('/chat/sessions')),
      ).toHaveLength(1),
    );
    // Still exactly one PATCH ever — the fallback used POST, not a second
    // PATCH against the dead id.
    expect(API.patch).toHaveBeenCalledTimes(1);
  });

  // 12. GENERATION TOKEN regression, the null/null edge case: a session id
  //     comparison alone cannot tell two DIFFERENT unsaved drafts apart —
  //     both have activeSessionId === null. Starting "+ new chat" while a
  //     fresh, never-saved conversation's first turn is still in flight
  //     must not let that turn's response (or its save) land in the new,
  //     blank draft.
  it('drops an in-flight turn started from an unsaved draft when + new chat is clicked before it resolves', async () => {
    let resolveChatSend;
    API.post.mockImplementation((url) => {
      if (url.endsWith('/chat/send')) {
        return new Promise((resolve) => {
          resolveChatSend = resolve;
        });
      }
      return Promise.reject(
        new Error('unexpected session create — the turn must be dropped'),
      );
    });

    render(<HFChat />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'first draft msg' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() =>
      expect(screen.getAllByText('first draft msg').length).toBeGreaterThan(0),
    );

    // Start a new chat WHILE the turn above is still in flight — never
    // disabled during `sending`.
    fireEvent.click(screen.getByText(/\+ new chat/i).closest('button'));
    expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    expect(screen.queryByText('first draft msg')).toBeNull();

    // Now let the stale turn resolve.
    resolveChatSend(chatResponse('stale draft reply'));
    await new Promise((resolve) => setTimeout(resolve, 0));

    // The new, blank chat must still be blank — the stale response was
    // dropped, not rendered into it, and never saved.
    expect(screen.queryByText(/stale draft reply/i)).toBeNull();
    expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    expect(API.patch).not.toHaveBeenCalled();
  });

  // 13. L1 (cycle-11): the model is no longer a literal — it defaults to
  // the FIRST routable model returned by GET .../models/routable.
  it('defaults the send model to the first routable model, not a literal', async () => {
    // NOT wireGet: this test overrides the routable response itself, so it
    // needs full control over that branch too (wireGet's own routable
    // branch always answers with the fixed ROUTABLE_DEFAULT).
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.resolve(
          routableResponse([
            {
              id: 'rt-beta',
              owned_by: 'custom',
              supported_endpoint_types: ['openai'],
            },
            {
              id: 'rt-gamma',
              owned_by: 'custom',
              supported_endpoint_types: ['openai'],
            },
          ]),
        );
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    API.post.mockResolvedValueOnce(chatResponse('hi from rt-beta'));
    render(<HFChat />);
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-select').value).toBe('rt-beta'),
    );

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() => expect(API.post).toHaveBeenCalled());
    const [, sendPayload] = API.post.mock.calls[0];
    expect(sendPayload.model).toBe('rt-beta');
  });

  // 13b. Before the routable fetch resolves, the page must not have picked
  // ANY model (literal or otherwise) — the pre-resolve window is exactly
  // where a hardcoded vendor model would be observable and sendable, and
  // is the window the previous test could not see because it always
  // waited for the fetch to resolve first.
  it('carries no model and blocks send before the routable list resolves', async () => {
    // Never resolves — pins the component in the pre-resolve state for
    // the life of the test.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return new Promise(() => {});
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    render(<HFChat />);

    // Neither the empty-state pill nor the error pill has anything to show
    // yet — `resolved` is still false — so the select branch renders with
    // an empty value, not a literal.
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-select')).toBeTruthy(),
    );
    expect(screen.getByTestId('chat-model-select').value).toBe('');

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    const sendBtn = screen.getByText(/▶ send/i).closest('button');
    expect(sendBtn?.disabled).toBe(true);
    fireEvent.click(sendBtn);
    expect(API.post).not.toHaveBeenCalled();
  });

  // 14. Honest empty state: a tenant with zero routable models must never
  // see a command (send) that is guaranteed to fail.
  it('shows an honest empty state and disables send when nothing is routable', async () => {
    // NOT wireGet — see the previous test's comment.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.resolve(routableResponse([]));
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    render(<HFChat />);

    await waitFor(() =>
      expect(screen.getByTestId('chat-no-models')).toBeTruthy(),
    );
    expect(screen.queryByTestId('chat-model-select')).toBeNull();

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    const sendBtn = screen.getByText(/▶ send/i).closest('button');
    expect(sendBtn?.disabled).toBe(true);
    fireEvent.click(sendBtn);
    expect(API.post).not.toHaveBeenCalled();
  });

  // 15. A load failure on the routable endpoint must be distinguishable
  // from "the tenant genuinely has zero models".
  it('shows a models_load_failed state when the routable fetch errors', async () => {
    // NOT wireGet — see the earlier comment.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.reject(new Error('network down'));
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    render(<HFChat />);

    await waitFor(() =>
      expect(screen.getByTestId('chat-models-error')).toBeTruthy(),
    );
    expect(screen.queryByTestId('chat-no-models')).toBeNull();
    expect(screen.queryByTestId('chat-model-select')).toBeNull();
  });

  // 16. The picker is a real control: changing it changes the model the
  // NEXT send actually carries.
  it('the picker changes the model sent', async () => {
    // NOT wireGet — see the earlier comment.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.resolve(
          routableResponse([
            {
              id: 'rt-alpha',
              owned_by: 'custom',
              supported_endpoint_types: ['openai'],
            },
            {
              id: 'rt-beta',
              owned_by: 'custom',
              supported_endpoint_types: ['openai'],
            },
          ]),
        );
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    API.post.mockResolvedValueOnce(chatResponse('hi from rt-beta'));
    render(<HFChat />);
    await waitForModelReady();

    fireEvent.change(screen.getByTestId('chat-model-select'), {
      target: { value: 'rt-beta' },
    });
    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() => expect(API.post).toHaveBeenCalled());
    const [, sendPayload] = API.post.mock.calls[0];
    expect(sendPayload.model).toBe('rt-beta');
  });

  // 17. openSession adopts the session's OWN model — a saved session made
  // with a since-superseded model must keep answering with that model, not
  // silently switch to whatever the picker currently shows.
  it('opening a saved session adopts its model', async () => {
    wireGet((url) => {
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 3,
                  title: 'beta convo',
                  model: 'rt-beta',
                  message_count: 1,
                },
              ],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/3') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 3,
              title: 'beta convo',
              model: 'rt-beta',
              messages: [{ role: 'user', content: 'hi' }],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    render(<HFChat />);
    await waitFor(() => expect(screen.getByText('beta convo')).toBeTruthy());

    fireEvent.click(screen.getByTestId('session-list-row-3'));

    await waitFor(() =>
      expect(screen.getByTestId('chat-model-select').value).toBe('rt-beta'),
    );
  });

  // 17b. A routable-list refetch (triggered by model_not_found below) must
  // not clobber a model the user/session already picked — the
  // modelInitializedRef guard is what stops the once-only default-pick
  // effect from re-firing just because `routableModels` got a new array
  // reference from the refetch.
  it('keeps a saved session model after a routable refetch, not the first routable model', async () => {
    // A fresh array on every call (not a shared reference) — the refetch
    // this test drives must look, from React's point of view, like a real
    // new answer (same ids, new array identity), the way two separate HTTP
    // responses actually would.
    const makeRoutableTwo = () => [
      {
        id: 'rt-alpha',
        owned_by: 'custom',
        supported_endpoint_types: ['openai'],
      },
      {
        id: 'rt-beta',
        owned_by: 'custom',
        supported_endpoint_types: ['openai'],
      },
    ];
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        return Promise.resolve(routableResponse(makeRoutableTwo()));
      }
      if (url === '/api/v2/acme/chat/sessions') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              sessions: [
                {
                  id: 3,
                  title: 'beta convo',
                  model: 'rt-beta',
                  message_count: 1,
                },
              ],
            },
          },
        });
      }
      if (url === '/api/v2/acme/chat/sessions/3') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 3,
              title: 'beta convo',
              model: 'rt-beta',
              messages: [{ role: 'user', content: 'hi' }],
            },
          },
        });
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    API.post.mockRejectedValueOnce({
      response: {
        status: 502,
        data: { error_code: 'model_not_found', message: 'model not found' },
      },
    });
    render(<HFChat />);
    await waitFor(() => expect(screen.getByText('beta convo')).toBeTruthy());

    fireEvent.click(screen.getByTestId('session-list-row-3'));
    await waitFor(() =>
      expect(screen.getByTestId('chat-model-select').value).toBe('rt-beta'),
    );

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() =>
      expect(screen.getByTestId('chat-model-not-found-hint')).toBeTruthy(),
    );
    // The refetch triggered by model_not_found answered with a NEW array
    // (same two ids, rt-alpha still first) — the session's own model must
    // still be selected, not rt-alpha.
    expect(screen.getByTestId('chat-model-select').value).toBe('rt-beta');
  });

  // 18. model_not_found: /chat/send answers 502 with error_code
  // "model_not_found" when the routable list was stale (the routing truth
  // changed between the picker resolving and this send landing). Must
  // toast, show an inline hint naming the model, and refetch the list.
  it('shows model_not_found guidance and refetches the routable list on a stale model', async () => {
    let routableCalls = 0;
    // NOT wireGet — needs to count routable calls itself.
    API.get.mockImplementation((url) => {
      if (String(url).includes('/models/routable')) {
        routableCalls += 1;
        return Promise.resolve(routableResponse());
      }
      return Promise.resolve({
        data: { success: true, data: { sessions: [] } },
      });
    });
    API.post.mockRejectedValueOnce({
      response: {
        status: 502,
        data: { error_code: 'model_not_found', message: 'model not found' },
      },
    });
    render(<HFChat />);
    await waitForModelReady();
    const callsBeforeSend = routableCalls;

    fireEvent.change(screen.getByLabelText('message-input'), {
      target: { value: 'hello' },
    });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));

    await waitFor(() =>
      expect(screen.getByTestId('chat-model-not-found-hint')).toBeTruthy(),
    );
    expect(showError).toHaveBeenCalledWith(expect.stringContaining('rt-alpha'));
    await waitFor(() => expect(routableCalls).toBeGreaterThan(callsBeforeSend));
  });
});
