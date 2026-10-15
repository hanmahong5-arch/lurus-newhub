/*
Copyright (C) 2023-2026 QuantumNous

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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { AxiosRequestConfig } from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ApiError, http } from '@/lib/api';

import { ChatPage } from './index';

// The page talks to the server through the real tenantApi / unwrap, so every
// URL asserted below is the one that would go on the wire. Only the transport
// is replaced.
const h = vi.hoisted(() => ({
  toastError: vi.fn(),
  toastSuccess: vi.fn(),
  copy: vi.fn(),
}));

vi.mock('sonner', () => ({
  toast: { error: h.toastError, success: h.toastSuccess },
}));

vi.mock('@/lib/copy-to-clipboard', () => ({
  copyToClipboard: h.copy,
}));

interface Req {
  method: string;
  url: string;
  data?: Record<string, unknown> & { messages?: unknown[] };
}
type Handler = (req: Req) => unknown;

const BASE = '/api/v2/~';
const SESSIONS = `${BASE}/chat/sessions`;
const SEND = `${BASE}/chat/send`;
const ROUTABLE = `${BASE}/models/routable`;

const chatModel = (id: string, types: string[] = ['openai']) => ({
  id,
  owned_by: 'custom',
  supported_endpoint_types: types,
});

const summary = (
  id: number,
  title: string,
  extra: Record<string, unknown> = {},
) => ({
  id,
  title,
  model: 'rt-alpha',
  message_count: 2,
  updated_at: new Date().toISOString(),
  ...extra,
});

const detail = (
  id: number,
  title: string,
  messages: Array<{ role: string; content: string }>,
  model = 'rt-alpha',
) => ({ id, title, model, messages });

const reply = (content: string) => ({
  message: { role: 'assistant', content },
  usage: { prompt_tokens: 8, completion_tokens: 15 },
  latency_ms: 120,
});

function deferred<T = unknown>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

let nextSessionId = 101;
let handlers: Record<string, Handler> = {};

function defaults(): Record<string, Handler> {
  return {
    [`GET ${ROUTABLE}`]: () => ({ items: [chatModel('rt-alpha')] }),
    [`GET ${SESSIONS}`]: () => ({ sessions: [] }),
    [`POST ${SESSIONS}`]: (req) =>
      detail(
        nextSessionId++,
        String(req.data?.title ?? ''),
        (req.data?.messages as Array<{ role: string; content: string }>) ?? [],
      ),
    [`POST ${SEND}`]: () => reply('ok'),
    [`DELETE ${SESSIONS}/9`]: () => null,
  };
}

function setup(overrides: Record<string, Handler> = {}) {
  handlers = { ...defaults(), ...overrides };
  vi.spyOn(http, 'request').mockImplementation(
    async (config: AxiosRequestConfig) => {
      const method = (config.method ?? 'GET').toUpperCase();
      const url = config.url ?? '';
      let handler = handlers[`${method} ${url}`];
      // PATCH on any session defaults to an echo of the written fields.
      if (!handler && method === 'PATCH' && url.startsWith(`${SESSIONS}/`)) {
        handler = (req) =>
          detail(
            Number(url.split('/').pop()),
            String(req.data?.title ?? 'kept'),
            (req.data?.messages as Array<{ role: string; content: string }>) ??
              [],
          );
      }
      if (!handler) throw new Error(`unhandled request ${method} ${url}`);
      const data = await handler({
        method,
        url,
        data: config.data as Req['data'],
      });
      return { data: { success: true, data }, status: 200 } as never;
    },
  );
}

/** Every request made so far, optionally narrowed to one method + URL. */
function calls(method?: string, url?: string): Req[] {
  const spy = vi.mocked(http.request);
  return spy.mock.calls
    .map(([config]) => ({
      method: (config.method ?? 'GET').toUpperCase(),
      url: config.url ?? '',
      data: config.data as Req['data'],
    }))
    .filter((c) => (!method || c.method === method) && (!url || c.url === url));
}

function renderPage(props: Parameters<typeof ChatPage>[0] = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ChatPage {...props} />
    </QueryClientProvider>,
  );
}

const messageBox = () => screen.getByRole('textbox', { name: 'Message' });
const sendButton = () => screen.getByRole('button', { name: /^Send/ });
const modelSelect = () => screen.getByTestId('chat-model-select');

/** The default pick is async (the routable fetch); wait for it before typing. */
async function ready(model = 'rt-alpha') {
  await waitFor(() => expect(modelSelect()).toHaveValue(model));
}

async function type(user: ReturnType<typeof userEvent.setup>, text: string) {
  await user.clear(messageBox());
  await user.type(messageBox(), text);
}

async function sendText(
  user: ReturnType<typeof userEvent.setup>,
  text: string,
) {
  await type(user, text);
  await user.click(sendButton());
}

async function openRowMenu(
  user: ReturnType<typeof userEvent.setup>,
  title: string,
  item: RegExp,
) {
  await user.click(
    await screen.findByRole('button', { name: `Actions for ${title}` }),
  );
  await user.click(await screen.findByRole('menuitem', { name: item }));
}

function rowOf(id: number) {
  return screen.getByTestId(`session-row-${id}`);
}

async function openSessionRow(
  user: ReturnType<typeof userEvent.setup>,
  id: number,
) {
  await user.click(within(rowOf(id)).getAllByRole('button')[0]);
}

beforeEach(() => {
  nextSessionId = 101;
  window.localStorage.clear();
  // A slug left in storage must never reach a URL.
  window.localStorage.setItem('tenant_slug', 'acme');
  h.copy.mockResolvedValue(true);
  setup();
});

describe('ChatPage', () => {
  // -- thread basics --------------------------------------------------------

  it('renders the empty state and keeps send disabled on blank input', async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(sendButton()).toBeDisabled();
    await user.type(messageBox(), '   ');
    expect(sendButton()).toBeDisabled();
    await user.type(messageBox(), 'hi');
    expect(sendButton()).toBeEnabled();
  });

  it('sends a turn, renders both messages and saves the conversation', async () => {
    const user = userEvent.setup();
    setup({ [`POST ${SEND}`]: () => reply('hi from rt-alpha') });
    renderPage();
    await ready();

    await sendText(user, 'hello');

    expect(await screen.findByText('hi from rt-alpha')).toBeInTheDocument();
    // The user's message is in the thread (and, once saved, in the title).
    expect(screen.getAllByText('hello').length).toBeGreaterThan(0);
    // Reply footer carries the real latency and token total.
    expect(screen.getByText('120 ms')).toBeInTheDocument();
    expect(screen.getByText('23 tokens')).toBeInTheDocument();

    await waitFor(() => expect(calls('POST')).toHaveLength(2));
    const [send, save] = calls('POST');
    expect(send.url).toBe(SEND);
    expect(send.data).toEqual({
      model: 'rt-alpha',
      messages: [{ role: 'user', content: 'hello' }],
    });
    expect(save.url).toBe(SESSIONS);
    expect(save.data).toMatchObject({
      title: 'hello',
      model: 'rt-alpha',
      messages: [
        { role: 'user', content: 'hello' },
        { role: 'assistant', content: 'hi from rt-alpha' },
      ],
    });
  });

  it('talks to the self-tenant alias, never a stored or literal slug', async () => {
    const user = userEvent.setup();
    window.localStorage.setItem('tenant_slug', 'lurus');
    renderPage();
    await ready();
    await sendText(user, 'ping');
    await waitFor(() => expect(calls('POST')).toHaveLength(2));
    const urls = calls().map((c) => c.url);
    expect(urls.length).toBeGreaterThan(0);
    expect(urls.every((u) => u.startsWith('/api/v2/~/'))).toBe(true);
    expect(urls.some((u) => u.includes('/default/'))).toBe(false);
    expect(urls.some((u) => u.includes('/lurus/'))).toBe(false);
  });

  it('forwards the full history and switches the save from POST to PATCH', async () => {
    const user = userEvent.setup();
    const replies = ['first reply', 'second reply'];
    setup({ [`POST ${SEND}`]: () => reply(replies.shift() ?? '') });
    renderPage();
    await ready();

    await sendText(user, 'hi');
    expect(await screen.findByText('first reply')).toBeInTheDocument();
    await waitFor(() => expect(calls('POST', SESSIONS)).toHaveLength(1));

    await sendText(user, 'follow up');
    expect(await screen.findByText('second reply')).toBeInTheDocument();

    const sends = calls('POST', SEND);
    expect(sends[1].data?.messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'first reply' },
      { role: 'user', content: 'follow up' },
    ]);
    await waitFor(() => expect(calls('PATCH')).toHaveLength(1));
    const [patch] = calls('PATCH');
    expect(patch.url).toBe(`${SESSIONS}/101`);
    expect(patch.data?.messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'first reply' },
      { role: 'user', content: 'follow up' },
      { role: 'assistant', content: 'second reply' },
    ]);
    // The picked model is saved on every PATCH, not just on create...
    expect(patch.data?.model).toBe('rt-alpha');
    // ...and the title is left alone so a rename survives later turns.
    expect(patch.data).not.toHaveProperty('title');
  });

  it('rolls back the optimistic message on failure, reports it and never saves', async () => {
    const user = userEvent.setup();
    setup({
      [`POST ${SEND}`]: () => {
        throw new ApiError('upstream down', { status: 502 });
      },
    });
    renderPage();
    await ready();

    await sendText(user, 'will fail');

    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('upstream down'),
    );
    // The thread no longer shows it; only the textarea (restored for a
    // retry) still holds the text.
    expect(document.querySelector('[data-role="user"]')).toBeNull();
    expect(messageBox()).toHaveValue('will fail');
    expect(calls('POST')).toHaveLength(1);
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
  });

  it('does not overwrite text typed meanwhile when restoring after a failure', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({ [`POST ${SEND}`]: () => gate.promise });
    renderPage();
    await ready();

    await sendText(user, 'first');
    await user.type(messageBox(), 'second draft');
    gate.reject(new ApiError('boom', { status: 502 }));

    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith('boom'));
    expect(messageBox()).toHaveValue('second draft');
  });

  it('treats a reply without a message as an error', async () => {
    const user = userEvent.setup();
    setup({ [`POST ${SEND}`]: () => ({}) });
    renderPage();
    await ready();
    await sendText(user, 'hello');
    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('Invalid chat response'),
    );
    expect(calls('POST')).toHaveLength(1);
  });

  it('shows a thinking indicator while a turn is in flight and blocks a second send', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({ [`POST ${SEND}`]: () => gate.promise });
    renderPage();
    await ready();

    await sendText(user, 'slow one');
    expect(await screen.findByTestId('sending-indicator')).toHaveTextContent(
      'rt-alpha is thinking...',
    );
    await user.type(messageBox(), 'another');
    expect(sendButton()).toBeDisabled();
    await user.keyboard('{Enter}');
    expect(calls('POST', SEND)).toHaveLength(1);

    gate.resolve(reply('done'));
    expect(await screen.findByText('done')).toBeInTheDocument();
    expect(screen.queryByTestId('sending-indicator')).not.toBeInTheDocument();
  });

  it('sends on Enter, not on Shift+Enter, and not while composing', async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();

    await user.type(messageBox(), 'line one{Shift>}{Enter}{/Shift}line two');
    expect(messageBox()).toHaveValue('line one\nline two');
    expect(calls('POST', SEND)).toHaveLength(0);

    // An IME candidate confirmation is an Enter keydown with isComposing.
    const box = messageBox();
    const composing = new KeyboardEvent('keydown', {
      key: 'Enter',
      bubbles: true,
      cancelable: true,
      isComposing: true,
    });
    box.dispatchEvent(composing);
    expect(calls('POST', SEND)).toHaveLength(0);

    await user.keyboard('{Enter}');
    await waitFor(() => expect(calls('POST', SEND)).toHaveLength(1));
    expect(calls('POST', SEND)[0].data?.messages).toEqual([
      { role: 'user', content: 'line one\nline two' },
    ]);
  });

  it('shows the real character budget and enforces the server limit', async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();
    expect(messageBox()).toHaveAttribute('maxlength', '200000');
    await user.type(messageBox(), 'abcd');
    expect(screen.getByText('4 / 200,000')).toBeInTheDocument();
  });

  it('renders replies as markdown and system turns as system notes', async () => {
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(4, 'rich')] }),
      [`GET ${SESSIONS}/4`]: () =>
        detail(4, 'rich', [
          { role: 'system', content: 'be brief' },
          { role: 'user', content: 'q' },
          { role: 'assistant', content: '**bold** answer' },
        ]),
    });
    const user = userEvent.setup();
    renderPage();
    await screen.findByText('rich');
    await openSessionRow(user, 4);
    const bold = await screen.findByText('bold');
    expect(bold.tagName).toBe('STRONG');
    expect(screen.getByText('be brief')).toBeInTheDocument();
    expect(screen.getByText('System message')).toBeInTheDocument();
    // Loaded history has no latency / token figures to invent.
    expect(screen.queryByText(/ ms$/)).not.toBeInTheDocument();
  });

  it('copies one reply and the whole conversation', async () => {
    const user = userEvent.setup();
    setup({ [`POST ${SEND}`]: () => reply('copy me') });
    renderPage();
    await ready();
    await sendText(user, 'q1');
    await screen.findByText('copy me');

    await user.click(screen.getByRole('button', { name: 'Copy' }));
    await waitFor(() => expect(h.copy).toHaveBeenCalledWith('copy me'));

    await user.click(
      screen.getByRole('button', { name: 'Conversation actions' }),
    );
    await user.click(
      await screen.findByRole('menuitem', { name: /Copy conversation/ }),
    );
    await waitFor(() =>
      expect(h.copy).toHaveBeenLastCalledWith(
        'You:\nq1\n\nAssistant:\ncopy me',
      ),
    );
  });

  // -- starting over --------------------------------------------------------

  it('clears the thread when + New chat is clicked (the saved copy stays)', async () => {
    const user = userEvent.setup();
    setup({ [`POST ${SEND}`]: () => reply('hi back') });
    renderPage();
    await ready();
    await sendText(user, 'hello');
    await screen.findByText('hi back');

    await user.click(screen.getByRole('button', { name: 'New chat' }));
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(screen.queryByText('hi back')).not.toBeInTheDocument();
    // Nothing was deleted server-side.
    expect(calls('DELETE')).toHaveLength(0);
  });

  it('asks before clearing and only clears on confirm', async () => {
    const user = userEvent.setup();
    setup({ [`POST ${SEND}`]: () => reply('hello response') });
    renderPage();
    await ready();
    await sendText(user, 'test msg');
    await screen.findByText('hello response');

    await user.click(
      screen.getByRole('button', { name: 'Conversation actions' }),
    );
    await user.click(
      await screen.findByRole('menuitem', { name: /Clear conversation/ }),
    );
    const dialog = await screen.findByRole('alertdialog');
    expect(
      within(dialog).getByText('Clear current conversation'),
    ).toBeVisible();
    // Cancel keeps the thread.
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
    );
    expect(screen.getByText('hello response')).toBeInTheDocument();

    await user.click(
      screen.getByRole('button', { name: 'Conversation actions' }),
    );
    await user.click(
      await screen.findByRole('menuitem', { name: /Clear conversation/ }),
    );
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Clear conversation',
      }),
    );
    expect(
      await screen.findByText('Ask anything to begin'),
    ).toBeInTheDocument();
  });

  // -- saved conversations --------------------------------------------------

  it('loads the caller’s saved conversations on mount', async () => {
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(5, 'earlier chat', { message_count: 4 })],
      }),
    });
    renderPage();
    expect(await screen.findByText('earlier chat')).toBeInTheDocument();
    expect(screen.getByText('4 messages')).toBeInTheDocument();
    expect(calls('GET', SESSIONS)).toHaveLength(1);
    expect(
      screen.queryByText('No saved conversations yet'),
    ).not.toBeInTheDocument();
  });

  it('says so when there are no saved conversations', async () => {
    renderPage();
    expect(
      await screen.findByText('No saved conversations yet'),
    ).toBeInTheDocument();
  });

  it('renders an error, never an empty list, when the list read fails, and retries', async () => {
    const user = userEvent.setup();
    let fail = true;
    setup({
      [`GET ${SESSIONS}`]: () => {
        if (fail) throw new ApiError('db is down', { status: 500 });
        return { sessions: [summary(5, 'back again')] };
      },
    });
    renderPage();
    const error = await screen.findByTestId('chat-sessions-error');
    expect(
      within(error).getByText('Failed to load conversations'),
    ).toBeVisible();
    expect(within(error).getByText('db is down')).toBeVisible();
    expect(
      screen.queryByText('No saved conversations yet'),
    ).not.toBeInTheDocument();
    // The chat itself still works without its history.
    await ready();

    fail = false;
    await user.click(within(error).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('back again')).toBeInTheDocument();
    expect(screen.queryByTestId('chat-sessions-error')).not.toBeInTheDocument();
  });

  it('opens a saved conversation and loads its messages', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(7, 'old convo')] }),
      [`GET ${SESSIONS}/7`]: () =>
        detail(7, 'old convo', [
          { role: 'user', content: 'what is Kyoto' },
          { role: 'assistant', content: 'a city in Japan' },
        ]),
    });
    renderPage();
    await screen.findByText('old convo');
    await openSessionRow(user, 7);

    expect(await screen.findByText('a city in Japan')).toBeInTheDocument();
    expect(screen.getAllByText('what is Kyoto').length).toBeGreaterThan(0);
    // The open conversation is flagged in the list.
    expect(within(rowOf(7)).getAllByRole('button')[0]).toHaveAttribute(
      'aria-current',
      'true',
    );
  });

  it('opens the conversation a link names, without clearing the link first', async () => {
    const onSessionChange = vi.fn();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(7, 'old convo')] }),
      [`GET ${SESSIONS}/7`]: () =>
        detail(7, 'old convo', [
          { role: 'user', content: 'what is Kyoto' },
          { role: 'assistant', content: 'a city in Japan' },
        ]),
    });
    renderPage({ sessionParam: 7, onSessionChange });
    expect(await screen.findByText('a city in Japan')).toBeInTheDocument();
    // The link already names 7: nothing to rewrite, and never a null that
    // would drop the link while the conversation was still loading.
    expect(onSessionChange).not.toHaveBeenCalled();
  });

  it('keeps the URL naming the open conversation and copies a link to it', async () => {
    const user = userEvent.setup();
    const onSessionChange = vi.fn();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(7, 'old convo')] }),
      [`GET ${SESSIONS}/7`]: () =>
        detail(7, 'old convo', [{ role: 'user', content: 'hello there' }]),
    });
    renderPage({ onSessionChange });
    await screen.findByText('old convo');

    // Nothing saved is open yet: there is no conversation to link to.
    await user.click(
      screen.getByRole('button', { name: 'Conversation actions' }),
    );
    expect(
      await screen.findByRole('menuitem', { name: /Copy link to this conversation/ }),
    ).toHaveAttribute('aria-disabled', 'true');
    await user.keyboard('{Escape}');

    await openSessionRow(user, 7);
    await screen.findAllByText('hello there');
    await waitFor(() => expect(onSessionChange).toHaveBeenLastCalledWith(7));

    await user.click(
      screen.getByRole('button', { name: 'Conversation actions' }),
    );
    await user.click(
      await screen.findByRole('menuitem', { name: /Copy link to this conversation/ }),
    );
    await waitFor(() =>
      expect(h.copy).toHaveBeenLastCalledWith(
        `${window.location.origin}/chat?session=7`,
      ),
    );
  });

  it('keeps the current conversation and reports it when opening one fails', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(8, 'broken one')] }),
      [`GET ${SESSIONS}/8`]: () => {
        throw new ApiError('Chat session not found', {
          status: 404,
          code: 'CHAT_SESSION_NOT_FOUND',
        });
      },
      [`POST ${SEND}`]: () => reply('still here'),
    });
    renderPage();
    await ready();
    await sendText(user, 'mine');
    await screen.findByText('still here');
    await screen.findByText('broken one');

    await openSessionRow(user, 8);
    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('Chat session not found'),
    );
    expect(screen.getByText('still here')).toBeInTheDocument();
    // The stale row is a reason to re-read the list.
    await waitFor(() =>
      expect(calls('GET', SESSIONS).length).toBeGreaterThan(1),
    );
  });

  it('only keeps the latest of two quick opens', async () => {
    const user = userEvent.setup();
    const first = deferred();
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(1, 'one'), summary(2, 'two')],
      }),
      [`GET ${SESSIONS}/1`]: () => first.promise,
      [`GET ${SESSIONS}/2`]: () =>
        detail(2, 'two', [{ role: 'user', content: 'B-user' }]),
    });
    renderPage();
    await screen.findByText('one');
    await openSessionRow(user, 1);
    await openSessionRow(user, 2);
    await screen.findAllByText('B-user');

    first.resolve(detail(1, 'one', [{ role: 'user', content: 'A-user' }]));
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText('A-user')).not.toBeInTheDocument();
    expect(screen.getAllByText('B-user').length).toBeGreaterThan(0);
  });

  // -- deleting -------------------------------------------------------------

  it('confirms before deleting, never deletes on cancel, then removes the row', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(9, 'to delete')] }),
    });
    renderPage();
    await screen.findByText('to delete');

    await openRowMenu(user, 'to delete', /Delete conversation/);
    let dialog = await screen.findByRole('alertdialog');
    expect(within(dialog).getByText('Delete this conversation?')).toBeVisible();
    expect(calls('DELETE')).toHaveLength(0);

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
    );
    expect(calls('DELETE')).toHaveLength(0);
    expect(screen.getByText('to delete')).toBeInTheDocument();

    await openRowMenu(user, 'to delete', /Delete conversation/);
    dialog = await screen.findByRole('alertdialog');
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(calls('DELETE')[0]?.url).toBe(`${SESSIONS}/9`));
    await waitFor(() =>
      expect(screen.queryByText('to delete')).not.toBeInTheDocument(),
    );
  });

  it('starts a new chat when the open conversation is deleted', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(9, 'doomed')] }),
      [`GET ${SESSIONS}/9`]: () =>
        detail(9, 'doomed', [
          { role: 'user', content: 'u' },
          { role: 'assistant', content: 'gone soon' },
        ]),
    });
    renderPage();
    await screen.findByText('doomed');
    await openSessionRow(user, 9);
    await screen.findByText('gone soon');

    await openRowMenu(user, 'doomed', /Delete conversation/);
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      }),
    );
    await waitFor(() =>
      expect(screen.queryByText('gone soon')).not.toBeInTheDocument(),
    );
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
  });

  it('using the row delete action does not also open the conversation', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(9, 'to delete')] }),
      [`GET ${SESSIONS}/9`]: () =>
        detail(9, 'to delete', [{ role: 'user', content: 'must not load' }]),
    });
    renderPage();
    await screen.findByText('to delete');

    await openRowMenu(user, 'to delete', /Delete conversation/);
    const dialog = await screen.findByRole('alertdialog');
    expect(calls('GET', `${SESSIONS}/9`)).toHaveLength(0);
    expect(screen.queryByText('must not load')).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() =>
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument(),
    );
    expect(calls('GET', `${SESSIONS}/9`)).toHaveLength(0);
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(rowOf(9)).not.toHaveAttribute('aria-current');
    expect(within(rowOf(9)).getAllByRole('button')[0]).not.toHaveAttribute(
      'aria-current',
    );
  });

  it('keeps the row and reports a failed delete', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(9, 'sticky')] }),
      [`DELETE ${SESSIONS}/9`]: () => {
        throw new ApiError('cannot delete', { status: 500 });
      },
    });
    renderPage();
    await screen.findByText('sticky');
    await openRowMenu(user, 'sticky', /Delete conversation/);
    await user.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', {
        name: 'Delete',
      }),
    );
    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('cannot delete'),
    );
    expect(screen.getByText('sticky')).toBeInTheDocument();
  });

  // -- renaming -------------------------------------------------------------

  it('renames a saved conversation with a title-only PATCH', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(3, 'untitled')] }),
      [`PATCH ${SESSIONS}/3`]: (req) => detail(3, String(req.data?.title), []),
    });
    renderPage();
    await screen.findByText('untitled');

    await openRowMenu(user, 'untitled', /Rename/);
    const field = await screen.findByRole('textbox', {
      name: 'Conversation name',
    });
    expect(field).toHaveValue('untitled');
    await user.clear(field);
    await user.click(screen.getByRole('button', { name: 'Save' }));
    // A blank name is refused client-side.
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    await user.type(field, '  Trip plan  ');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(calls('PATCH')).toHaveLength(1));
    // Messages are omitted, so the stored turns are left alone.
    expect(calls('PATCH')[0]).toMatchObject({
      url: `${SESSIONS}/3`,
      data: { title: 'Trip plan' },
    });
    expect(calls('PATCH')[0].data).not.toHaveProperty('messages');
    expect(await screen.findByText('Trip plan')).toBeInTheDocument();
    expect(screen.queryByText('untitled')).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    );
    expect(h.toastSuccess).toHaveBeenCalledWith('Conversation renamed');
  });

  it('keeps the dialog open and reports a failed rename', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(3, 'untitled')] }),
      [`PATCH ${SESSIONS}/3`]: () => {
        throw new ApiError('title exceeds 255 characters', { status: 400 });
      },
    });
    renderPage();
    await screen.findByText('untitled');
    await openRowMenu(user, 'untitled', /Rename/);
    const field = await screen.findByRole('textbox', {
      name: 'Conversation name',
    });
    await user.type(field, ' v2');
    await user.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('title exceeds 255 characters'),
    );
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getAllByText('untitled').length).toBeGreaterThan(0);
  });

  // -- the switching race ---------------------------------------------------

  it('drops an in-flight turn, and never saves it, when the user switches conversations', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(1, 'session one'), summary(2, 'session two')],
      }),
      [`GET ${SESSIONS}/1`]: () =>
        detail(1, 'session one', [{ role: 'user', content: 'A-user' }]),
      [`GET ${SESSIONS}/2`]: () =>
        detail(2, 'session two', [
          { role: 'user', content: 'B-user' },
          { role: 'assistant', content: 'B-asst' },
        ]),
      [`POST ${SEND}`]: () => gate.promise,
      [`POST ${SESSIONS}`]: () => {
        throw new Error('the dropped turn must not be created');
      },
    });
    renderPage();
    await screen.findByText('session one');
    await openSessionRow(user, 1);
    await screen.findAllByText('A-user');

    await sendText(user, 'A-follow');
    await screen.findAllByText('A-follow');

    // The sidebar stays usable while the turn is pending.
    await openSessionRow(user, 2);
    await screen.findAllByText('B-asst');
    expect(screen.queryByText('A-follow')).not.toBeInTheDocument();
    // ...and so does the composer: the pending turn belongs to session one.
    expect(screen.queryByTestId('sending-indicator')).not.toBeInTheDocument();
    expect(messageBox()).toBeEnabled();

    gate.resolve(reply('A-reply'));
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.queryByText('A-reply')).not.toBeInTheDocument();
    expect(screen.getAllByText('B-user').length).toBeGreaterThan(0);
    expect(calls('PATCH')).toHaveLength(0);
    expect(calls('POST', SESSIONS)).toHaveLength(0);
    expect(h.toastError).not.toHaveBeenCalled();
  });

  it('drops an in-flight turn from an unsaved draft when + New chat is clicked first', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({
      [`POST ${SEND}`]: () => gate.promise,
      [`POST ${SESSIONS}`]: () => {
        throw new Error('the dropped turn must not be created');
      },
    });
    renderPage();
    await ready();

    await sendText(user, 'first draft msg');
    await screen.findAllByText('first draft msg');

    // null -> null: a session-id comparison alone could not see this switch.
    await user.click(screen.getByRole('button', { name: 'New chat' }));
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(screen.queryByText('first draft msg')).not.toBeInTheDocument();

    gate.resolve(reply('stale draft reply'));
    await new Promise((r) => setTimeout(r, 0));

    expect(screen.queryByText('stale draft reply')).not.toBeInTheDocument();
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(calls('PATCH')).toHaveLength(0);
    expect(calls('POST', SESSIONS)).toHaveLength(0);
  });

  it('does not report a failure that belongs to a conversation left behind', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({ [`POST ${SEND}`]: () => gate.promise });
    renderPage();
    await ready();
    await sendText(user, 'doomed turn');
    await screen.findAllByText('doomed turn');
    await user.click(screen.getByRole('button', { name: 'New chat' }));

    gate.reject(new ApiError('late failure', { status: 502 }));
    await new Promise((r) => setTimeout(r, 0));

    expect(h.toastError).not.toHaveBeenCalled();
    expect(screen.getByText('Ask anything to begin')).toBeInTheDocument();
    expect(messageBox()).toHaveValue('');
  });

  // -- save failures --------------------------------------------------------

  it('falls back to creating after a 404 on PATCH and flags the turn as not saved', async () => {
    const user = userEvent.setup();
    let patches = 0;
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(55, 'gone elsewhere')],
      }),
      [`GET ${SESSIONS}/55`]: () =>
        detail(55, 'gone elsewhere', [{ role: 'user', content: 'old turn' }]),
      [`POST ${SEND}`]: () => reply('reply after 404'),
      [`PATCH ${SESSIONS}/55`]: () => {
        patches += 1;
        throw new ApiError('Chat session not found', {
          status: 404,
          code: 'CHAT_SESSION_NOT_FOUND',
        });
      },
      [`POST ${SESSIONS}`]: () => detail(999, 'x', []),
    });
    renderPage();
    await screen.findByText('gone elsewhere');
    await openSessionRow(user, 55);
    await screen.findAllByText('old turn');

    await sendText(user, 'first after reopen');
    await screen.findByText('reply after 404');
    // The failed save is visible, not swallowed.
    expect(await screen.findByTestId('chat-save-failed')).toBeInTheDocument();
    expect(patches).toBe(1);

    // The next turn creates a fresh session instead of re-PATCHing the id.
    await sendText(user, 'second after 404');
    await waitFor(() => expect(calls('POST', SESSIONS)).toHaveLength(1));
    expect(patches).toBe(1);
    await waitFor(() =>
      expect(screen.queryByTestId('chat-save-failed')).not.toBeInTheDocument(),
    );
  });

  it('retries the same session after a transient save failure instead of forking one', async () => {
    const user = userEvent.setup();
    let fail = true;
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(6, 'flaky')] }),
      [`GET ${SESSIONS}/6`]: () =>
        detail(6, 'flaky', [{ role: 'user', content: 'start' }]),
      [`PATCH ${SESSIONS}/6`]: (req) => {
        if (fail) {
          fail = false;
          throw new ApiError('db blip', { status: 500 });
        }
        return detail(6, 'flaky', (req.data?.messages as never) ?? []);
      },
    });
    renderPage();
    await screen.findByText('flaky');
    await openSessionRow(user, 6);
    await screen.findAllByText('start');

    await sendText(user, 'one');
    expect(await screen.findByTestId('chat-save-failed')).toBeInTheDocument();
    await sendText(user, 'two');
    await waitFor(() => expect(calls('PATCH')).toHaveLength(2));
    expect(calls('PATCH').every((c) => c.url === `${SESSIONS}/6`)).toBe(true);
    expect(calls('POST', SESSIONS)).toHaveLength(0);
    await waitFor(() =>
      expect(screen.queryByTestId('chat-save-failed')).not.toBeInTheDocument(),
    );
  });

  it('explains the saved-conversation cap when a create is refused', async () => {
    const user = userEvent.setup();
    setup({
      [`POST ${SESSIONS}`]: () => {
        throw new ApiError('Chat session limit reached', {
          status: 400,
          code: 'CHAT_SESSION_LIMIT_REACHED',
        });
      },
    });
    renderPage();
    await ready();
    await sendText(user, 'hello');
    const marker = await screen.findByTestId('chat-save-failed');
    expect(marker).toHaveTextContent(/saved conversation limit/);
    // The turn itself still rendered.
    expect(screen.getByText('ok')).toBeInTheDocument();
  });

  it('shows an unsaved draft row until its first save lands in the list', async () => {
    const user = userEvent.setup();
    renderPage();
    await ready();
    await sendText(user, 'draft me');
    await screen.findByText('ok');
    const draft = await screen.findByTestId('session-draft');
    expect(draft).toHaveTextContent('draft me');
    expect(draft).toHaveTextContent('Not saved yet');
  });

  // -- the model picker -----------------------------------------------------

  it('defaults to the first routable chat model from the API, never a literal', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${ROUTABLE}`]: () => ({
        items: [chatModel('rt-beta'), chatModel('rt-gamma')],
      }),
    });
    renderPage();
    await ready('rt-beta');
    await sendText(user, 'hello');
    await waitFor(() => expect(calls('POST', SEND)).toHaveLength(1));
    expect(calls('POST', SEND)[0].data?.model).toBe('rt-beta');
  });

  it('keeps non-chat models out of the picker and the default pick', async () => {
    setup({
      [`GET ${ROUTABLE}`]: () => ({
        items: [
          chatModel('sys-one', ['systemone']),
          chatModel('rt-beta'),
          chatModel('rt-undeclared', []),
        ],
      }),
    });
    renderPage();
    await ready('rt-beta');
    const options = within(modelSelect())
      .getAllByRole('option')
      .map((o) => (o as HTMLOptionElement).value);
    // A model that does not declare its endpoints is kept; one that
    // declares only a non-chat endpoint is not.
    expect(options).toEqual(['rt-beta', 'rt-undeclared']);
  });

  it('carries no model and blocks send until the routable list resolves', async () => {
    const user = userEvent.setup();
    const gate = deferred();
    setup({ [`GET ${ROUTABLE}`]: () => gate.promise });
    renderPage();

    expect(modelSelect()).toHaveValue('');
    await user.type(messageBox(), 'hello');
    expect(sendButton()).toBeDisabled();
    await user.keyboard('{Enter}');
    expect(calls('POST')).toHaveLength(0);

    gate.resolve({ items: [chatModel('rt-alpha')] });
    await ready();
    expect(sendButton()).toBeEnabled();
  });

  it('shows an honest empty state and blocks send when nothing is routable', async () => {
    const user = userEvent.setup();
    setup({ [`GET ${ROUTABLE}`]: () => ({ items: [] }) });
    renderPage();

    expect(await screen.findByTestId('chat-no-models')).toBeInTheDocument();
    expect(screen.queryByTestId('chat-model-select')).not.toBeInTheDocument();
    expect(
      screen.getByText(/No chat model is routable for this tenant yet/),
    ).toBeInTheDocument();
    await user.type(messageBox(), 'hello');
    expect(sendButton()).toBeDisabled();
    await user.keyboard('{Enter}');
    expect(calls('POST')).toHaveLength(0);
  });

  it('tells a failed model read apart from "no models" and can retry it', async () => {
    const user = userEvent.setup();
    let fail = true;
    setup({
      [`GET ${ROUTABLE}`]: () => {
        if (fail) throw new ApiError('registry down', { status: 500 });
        return { items: [chatModel('rt-alpha')] };
      },
    });
    renderPage();

    const error = await screen.findByTestId('chat-models-error');
    expect(within(error).getByText('Failed to load models')).toBeVisible();
    expect(screen.queryByTestId('chat-no-models')).not.toBeInTheDocument();
    expect(screen.queryByTestId('chat-model-select')).not.toBeInTheDocument();
    await user.type(messageBox(), 'hello');
    expect(sendButton()).toBeDisabled();

    fail = false;
    await user.click(within(error).getByRole('button', { name: 'Retry' }));
    await ready();
    expect(screen.queryByTestId('chat-models-error')).not.toBeInTheDocument();
    expect(sendButton()).toBeEnabled();
  });

  it('sends with whichever model the picker shows', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${ROUTABLE}`]: () => ({
        items: [chatModel('rt-alpha'), chatModel('rt-beta')],
      }),
    });
    renderPage();
    await ready();
    await user.selectOptions(modelSelect(), 'rt-beta');
    await sendText(user, 'hello');
    await waitFor(() => expect(calls('POST', SEND)).toHaveLength(1));
    expect(calls('POST', SEND)[0].data?.model).toBe('rt-beta');
    await waitFor(() => expect(calls('POST', SESSIONS)).toHaveLength(1));
    expect(calls('POST', SESSIONS)[0].data?.model).toBe('rt-beta');
  });

  it('adopts the opened conversation’s own model', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${ROUTABLE}`]: () => ({
        items: [chatModel('rt-alpha'), chatModel('rt-beta')],
      }),
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(3, 'beta convo', { model: 'rt-beta' })],
      }),
      [`GET ${SESSIONS}/3`]: () =>
        detail(3, 'beta convo', [{ role: 'user', content: 'hi' }], 'rt-beta'),
    });
    renderPage();
    await ready();
    await screen.findByText('beta convo');
    await openSessionRow(user, 3);
    await waitFor(() => expect(modelSelect()).toHaveValue('rt-beta'));
  });

  it('keeps a saved conversation’s model selectable even when it is no longer routable', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(3, 'old model', { model: 'rt-retired' })],
      }),
      [`GET ${SESSIONS}/3`]: () =>
        detail(3, 'old model', [{ role: 'user', content: 'hi' }], 'rt-retired'),
    });
    renderPage();
    await ready();
    await screen.findByText('old model');
    await openSessionRow(user, 3);
    await waitFor(() => expect(modelSelect()).toHaveValue('rt-retired'));
    const options = within(modelSelect())
      .getAllByRole('option')
      .map((o) => (o as HTMLOptionElement).value);
    expect(options).toEqual(['rt-retired', 'rt-alpha']);
  });

  it('on model_not_found warns, names the model and refetches the routable list', async () => {
    const user = userEvent.setup();
    let routableReads = 0;
    setup({
      [`GET ${ROUTABLE}`]: () => {
        routableReads += 1;
        return { items: [chatModel('rt-alpha'), chatModel('rt-beta')] };
      },
      [`GET ${SESSIONS}`]: () => ({
        sessions: [summary(3, 'beta convo', { model: 'rt-beta' })],
      }),
      [`GET ${SESSIONS}/3`]: () =>
        detail(3, 'beta convo', [{ role: 'user', content: 'hi' }], 'rt-beta'),
      [`POST ${SEND}`]: () => {
        throw new ApiError('model not found', {
          status: 502,
          code: 'model_not_found',
        });
      },
    });
    renderPage();
    await ready();
    await screen.findByText('beta convo');
    await openSessionRow(user, 3);
    await waitFor(() => expect(modelSelect()).toHaveValue('rt-beta'));
    const before = routableReads;

    await sendText(user, 'hello');

    const hint = await screen.findByTestId('chat-model-not-found-hint');
    expect(hint).toHaveTextContent('rt-beta');
    expect(h.toastError).toHaveBeenCalledWith(
      expect.stringContaining('rt-beta'),
    );
    await waitFor(() => expect(routableReads).toBeGreaterThan(before));
    // The refetch must not clobber the session's own pick with the first
    // routable model.
    expect(modelSelect()).toHaveValue('rt-beta');

    // Picking another model clears the hint.
    await user.selectOptions(modelSelect(), 'rt-alpha');
    expect(
      screen.queryByTestId('chat-model-not-found-hint'),
    ).not.toBeInTheDocument();
  });

  // -- layout ---------------------------------------------------------------

  it('opens the conversation list in a drawer on small screens and closes it after a pick', async () => {
    const user = userEvent.setup();
    setup({
      [`GET ${SESSIONS}`]: () => ({ sessions: [summary(7, 'drawer convo')] }),
      [`GET ${SESSIONS}/7`]: () =>
        detail(7, 'drawer convo', [{ role: 'user', content: 'from drawer' }]),
    });
    renderPage();
    await ready();
    await screen.findByText('drawer convo');

    await user.click(screen.getByRole('button', { name: 'Conversations' }));
    const drawer = await screen.findByRole('dialog');
    await user.click(
      within(drawer).getByRole('button', { name: /^drawer convo/ }),
    );

    expect((await screen.findAllByText('from drawer')).length).toBeGreaterThan(
      0,
    );
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    );
  });

  it('groups saved conversations by how recently they changed', async () => {
    const day = 86_400_000;
    const at = (ms: number) => new Date(Date.now() - ms).toISOString();
    setup({
      [`GET ${SESSIONS}`]: () => ({
        sessions: [
          summary(1, 'fresh', { updated_at: at(0) }),
          summary(2, 'last week', { updated_at: at(4 * day) }),
          summary(3, 'ancient', { updated_at: at(60 * day) }),
        ],
      }),
    });
    renderPage();
    await screen.findByText('fresh');
    expect(screen.getByRole('region', { name: 'Today' })).toContainElement(
      screen.getByText('fresh'),
    );
    expect(
      screen.getByRole('region', { name: 'Previous 7 days' }),
    ).toContainElement(screen.getByText('last week'));
    expect(screen.getByRole('region', { name: 'Older' })).toContainElement(
      screen.getByText('ancient'),
    );
  });
});
