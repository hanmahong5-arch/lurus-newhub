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
  API: { get: vi.fn(), post: vi.fn() },
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
  API.post.mockReset();
  showError.mockReset();
  showSuccess.mockReset();
  // The slug the browser was actually given — the page must read it from
  // storage, not from the (segment-less) /console/v2/chat route.
  window.localStorage.clear();
  window.localStorage.setItem('tenant_slug', 'acme');
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

describe('Chat page', () => {
  // 1. Empty state — no messages, prompt to start, send disabled when
  //    input empty.
  it('renders empty state and disables send on blank input', () => {
    render(<HFChat />);
    expect(screen.getByText(/ask anything to begin/i)).toBeTruthy();
    const sendBtn = screen.getByText(/▶ send/i).closest('button');
    expect(sendBtn?.disabled).toBe(true);
  });

  // 2. Send one turn → user message appears optimistically, then assistant
  //    response renders, and the full conversation is preserved.
  it('sends a turn and renders both user + assistant messages', async () => {
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

    // API.post called with /api/v2/acme/chat/send and full message history.
    expect(API.post).toHaveBeenCalledTimes(1);
    const [url, payload] = API.post.mock.calls[0];
    expect(url).toBe('/api/v2/acme/chat/send');
    expect(payload.model).toBe('gpt-4o');
    expect(payload.messages).toEqual([{ role: 'user', content: 'hello' }]);
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

    await waitFor(() => expect(API.post).toHaveBeenCalledTimes(1));
    const urls = API.post.mock.calls.map(([u]) => u);
    expect(urls).toEqual(['/api/v2/lurus/chat/send']);
    expect(urls.some((u) => u.includes('/api/v2/default/'))).toBe(false);
  });

  // 3. Multi-turn — second send forwards the *full* history (user + asst +
  //    user) to the backend, matching the in-memory append model.
  it('forwards full message history on subsequent turns', async () => {
    API.post
      .mockResolvedValueOnce(chatResponse('first reply'))
      .mockResolvedValueOnce(chatResponse('second reply'));
    render(<HFChat />);

    const input = screen.getByLabelText('message-input');
    fireEvent.change(input, { target: { value: 'hi' } });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() => expect(screen.getByText(/first reply/i)).toBeTruthy());

    fireEvent.change(input, { target: { value: 'follow up' } });
    fireEvent.click(screen.getByText(/▶ send/i).closest('button'));
    await waitFor(() => expect(screen.getByText(/second reply/i)).toBeTruthy());

    expect(API.post).toHaveBeenCalledTimes(2);
    const [, secondPayload] = API.post.mock.calls[1];
    expect(secondPayload.messages).toEqual([
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: 'first reply' },
      { role: 'user', content: 'follow up' },
    ]);
  });

  // 4. Failure path — backend 5xx rolls back the optimistic user message
  //    and surfaces a showError call. Without rollback a retry would
  //    double-send.
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
  });

  // 5. + new chat clears the in-memory conversation.
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
});
