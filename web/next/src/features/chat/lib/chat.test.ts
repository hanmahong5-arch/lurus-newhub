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
import { describe, expect, it } from 'vitest';

import type { ChatSessionSummary } from '../types';
import {
  countReplies,
  firstUserText,
  formatPreview,
  groupSessions,
  isUnsavedDraft,
  isValidTitle,
  makeMessage,
  mapReply,
  mapRoutableModels,
  mapSessionDetail,
  mapSessionList,
  speaksChat,
  toTranscript,
  toWire,
} from './chat';

const session = (id: number, updatedAt: string): ChatSessionSummary => ({
  id,
  title: `s${id}`,
  model: 'model-a',
  messageCount: 1,
  updatedAt,
});

describe('formatPreview', () => {
  it('leaves a short first message alone and clips a long one', () => {
    expect(formatPreview('hello')).toBe('hello');
    expect(formatPreview('')).toBe('');
    const long = 'x'.repeat(50);
    expect(formatPreview(long)).toBe(`${'x'.repeat(36)}…`);
  });

  it('never splits a character made of two code units', () => {
    const emoji = '😀'.repeat(40);
    const clipped = formatPreview(emoji);
    expect([...clipped]).toHaveLength(37);
    expect(clipped.endsWith('…')).toBe(true);
    // A lone surrogate would break JSON / the server's rune count.
    expect(() => encodeURIComponent(clipped)).not.toThrow();
  });
});

describe('speaksChat', () => {
  const model = (endpointTypes: string[]) => ({
    id: 'model-a',
    ownedBy: 'vendor-a',
    endpointTypes,
  });
  it('keeps chat models and models that do not say which endpoints they have', () => {
    expect(speaksChat(model(['openai']))).toBe(true);
    expect(speaksChat(model(['openai', 'anthropic']))).toBe(true);
    expect(speaksChat(model([]))).toBe(true);
  });
  it('drops models that only speak another endpoint', () => {
    expect(speaksChat(model(['systemone']))).toBe(false);
    expect(speaksChat(model(['embeddings']))).toBe(false);
  });
});

describe('wire mappers', () => {
  it('maps the session list and drops rows without a usable id', () => {
    expect(
      mapSessionList({
        sessions: [
          {
            id: 5,
            title: 'a',
            model: 'model-a',
            message_count: 4,
            updated_at: '2026-01-01T00:00:00Z',
          },
          { id: 'x', title: 'bad' },
          { id: 0, title: 'zero' },
        ],
      }),
    ).toEqual([
      {
        id: 5,
        title: 'a',
        model: 'model-a',
        messageCount: 4,
        updatedAt: '2026-01-01T00:00:00Z',
      },
    ]);
    expect(mapSessionList(null)).toEqual([]);
    expect(mapSessionList({ sessions: null })).toEqual([]);
  });

  it('maps a session detail and defaults an unknown role to assistant', () => {
    const d = mapSessionDetail({
      id: 7,
      title: 't',
      model: 'model-a',
      messages: [
        { role: 'system', content: 's' },
        { role: 'user', content: 'u' },
        { role: 'weird', content: 'w' },
      ],
    });
    expect(d.id).toBe(7);
    expect(d.messages.map((m) => [m.role, m.content])).toEqual([
      ['system', 's'],
      ['user', 'u'],
      ['assistant', 'w'],
    ]);
    expect(new Set(d.messages.map((m) => m.key)).size).toBe(3);
    expect(mapSessionDetail({ id: 1 }).messages).toEqual([]);
  });

  it('maps a reply and rejects a body with no assistant message', () => {
    expect(
      mapReply({
        message: { role: 'assistant', content: 'hi' },
        usage: { prompt_tokens: 3, completion_tokens: 4 },
        latency_ms: 12,
      }),
    ).toEqual({
      content: 'hi',
      latencyMs: 12,
      promptTokens: 3,
      completionTokens: 4,
    });
    expect(mapReply({})).toBeNull();
    expect(mapReply(null)).toBeNull();
    // No usage block: the figures stay unknown rather than becoming zero.
    expect(mapReply({ message: { content: 'x' } })).toEqual({
      content: 'x',
      latencyMs: undefined,
      promptTokens: undefined,
      completionTokens: undefined,
    });
  });

  it('maps routable models and drops nameless ones', () => {
    expect(
      mapRoutableModels({
        items: [
          {
            id: 'model-a',
            owned_by: 'vendor-a',
            supported_endpoint_types: ['openai'],
          },
          { id: '', owned_by: 'vendor-a' },
          { id: 'model-b' },
        ],
      }),
    ).toEqual([
      { id: 'model-a', ownedBy: 'vendor-a', endpointTypes: ['openai'] },
      { id: 'model-b', ownedBy: '', endpointTypes: [] },
    ]);
    expect(mapRoutableModels({ items: null })).toEqual([]);
  });
});

describe('messages', () => {
  const messages = [
    makeMessage('system', 'be brief'),
    makeMessage('user', 'q1'),
    makeMessage('assistant', 'a1', { latencyMs: 5 }),
    makeMessage('user', 'q2'),
  ];
  it('sends only role and content, never the client key or metrics', () => {
    expect(toWire(messages)).toEqual([
      { role: 'system', content: 'be brief' },
      { role: 'user', content: 'q1' },
      { role: 'assistant', content: 'a1' },
      { role: 'user', content: 'q2' },
    ]);
  });
  it('finds the first user turn and counts replies', () => {
    expect(firstUserText(messages)).toBe('q1');
    expect(firstUserText([])).toBe('');
    expect(countReplies(messages)).toBe(1);
  });
  it('exports a transcript with the given role labels', () => {
    expect(
      toTranscript(messages.slice(1, 3), {
        user: 'You',
        assistant: 'Assistant',
        system: 'System',
      }),
    ).toBe('You:\nq1\n\nAssistant:\na1');
  });
});

describe('isValidTitle', () => {
  it('refuses blank and over-long titles', () => {
    expect(isValidTitle('  ')).toBe(false);
    expect(isValidTitle('ok')).toBe(true);
    expect(isValidTitle('x'.repeat(255))).toBe(true);
    expect(isValidTitle('x'.repeat(256))).toBe(false);
    // The server counts characters, so 255 four-byte characters are fine.
    expect(isValidTitle('😀'.repeat(255))).toBe(true);
  });
});

describe('groupSessions', () => {
  // Local noon, so day arithmetic never straddles midnight.
  const now = new Date(2026, 5, 15, 12, 0, 0);
  const ago = (days: number, hours = 0) =>
    new Date(
      now.getTime() - days * 86_400_000 - hours * 3_600_000,
    ).toISOString();

  it('buckets by calendar day and keeps the incoming order', () => {
    const groups = groupSessions(
      [
        session(1, ago(0, 1)),
        session(2, ago(0, 3)),
        session(3, ago(1)),
        session(4, ago(3)),
        session(5, ago(6)),
        session(6, ago(7)),
        session(7, 'not a date'),
      ],
      now,
    );
    expect(groups.map((g) => [g.key, g.items.map((s) => s.id)])).toEqual([
      ['today', [1, 2]],
      ['yesterday', [3]],
      ['week', [4, 5]],
      ['older', [6, 7]],
    ]);
  });

  it('omits empty buckets and treats a future stamp as today', () => {
    expect(groupSessions([], now)).toEqual([]);
    const groups = groupSessions([session(1, ago(-2))], now);
    expect(groups.map((g) => g.key)).toEqual(['today']);
  });
});

describe('isUnsavedDraft', () => {
  const saved = [session(7, '2026-01-01T00:00:00Z')];

  it('is never a draft without messages', () => {
    expect(isUnsavedDraft(false, null, saved)).toBe(false);
    expect(isUnsavedDraft(false, 7, saved)).toBe(false);
  });

  it('is a draft when there is no id yet, whatever the list state', () => {
    expect(isUnsavedDraft(true, null, undefined)).toBe(true);
    expect(isUnsavedDraft(true, null, saved)).toBe(true);
  });

  it('does not mislabel a saved conversation while the list is loading or failed', () => {
    expect(isUnsavedDraft(true, 7, undefined)).toBe(false);
  });

  it('is a draft only when the loaded list lacks the open id', () => {
    expect(isUnsavedDraft(true, 7, saved)).toBe(false);
    expect(isUnsavedDraft(true, 8, saved)).toBe(true);
  });
});
