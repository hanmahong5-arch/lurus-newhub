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
import type {
  ChatMessage,
  ChatReply,
  ChatRole,
  ChatSessionDetail,
  ChatSessionSummary,
  RoutableModel,
  SessionGroup,
  WireMessage,
} from '../types';

/** The server rejects a message longer than this (v2_chat_session.go). */
export const MAX_MESSAGE_CHARS = 200_000;
/** chat_sessions.title is VARCHAR(255); the server rejects, never truncates. */
export const MAX_TITLE_CHARS = 255;
/** Length of the title derived from a conversation's first message. */
const PREVIEW_CHARS = 36;

/** The wire the console builds chat completions for. */
const CHAT_WIRE = 'openai';

const ROLES: readonly ChatRole[] = ['user', 'assistant', 'system'];

let keyCounter = 0;
export function newMessageKey(): string {
  keyCounter += 1;
  return `m${keyCounter}`;
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === 'object' && value !== null
    ? (value as Record<string, unknown>)
    : {};
}

function str(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function optNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value)
    ? value
    : undefined;
}

export function makeMessage(
  role: ChatRole,
  content: string,
  meta?: ChatMessage['meta'],
): ChatMessage {
  return { key: newMessageKey(), role, content, ...(meta ? { meta } : {}) };
}

/** `GET /chat/sessions` -> `{ sessions: [...] }` */
export function mapSessionList(raw: unknown): ChatSessionSummary[] {
  const sessions = record(raw).sessions;
  if (!Array.isArray(sessions)) return [];
  return sessions
    .map((s) => {
      const r = record(s);
      return {
        id: typeof r.id === 'number' ? r.id : Number(r.id),
        title: str(r.title),
        model: str(r.model),
        messageCount: optNumber(r.message_count) ?? 0,
        updatedAt: str(r.updated_at),
      };
    })
    .filter((s) => Number.isInteger(s.id) && s.id > 0);
}

/** `GET /chat/sessions/:id` (and the create / update responses). */
export function mapSessionDetail(raw: unknown): ChatSessionDetail {
  const r = record(raw);
  const messages = Array.isArray(r.messages) ? r.messages : [];
  return {
    id: typeof r.id === 'number' ? r.id : Number(r.id),
    title: str(r.title),
    model: str(r.model),
    messages: messages.map((m) => {
      const mr = record(m);
      const role = ROLES.find((x) => x === mr.role) ?? 'assistant';
      return makeMessage(role, str(mr.content));
    }),
  };
}

/** `POST /chat/send`; `null` when the body carries no assistant message. */
export function mapReply(raw: unknown): ChatReply | null {
  const r = record(raw);
  if (typeof r.message !== 'object' || r.message === null) return null;
  const usage = record(r.usage);
  return {
    content: str(record(r.message).content),
    latencyMs: optNumber(r.latency_ms),
    promptTokens: optNumber(usage.prompt_tokens),
    completionTokens: optNumber(usage.completion_tokens),
  };
}

/** `GET /models/routable` -> `{ items: [...] }` */
export function mapRoutableModels(raw: unknown): RoutableModel[] {
  const items = record(raw).items;
  if (!Array.isArray(items)) return [];
  return items
    .map((m) => {
      const r = record(m);
      return {
        id: str(r.id),
        ownedBy: str(r.owned_by),
        endpointTypes: Array.isArray(r.supported_endpoint_types)
          ? r.supported_endpoint_types.filter(
              (x): x is string => typeof x === 'string',
            )
          : [],
      };
    })
    .filter((m) => m.id !== '');
}

/**
 * Not every routable model speaks chat completions (System One answers only
 * its own endpoint, embeddings only /v1/embeddings). A model that does not
 * say which endpoints it has is kept: the field is the backend's to fill.
 */
export function speaksChat(model: RoutableModel): boolean {
  return (
    model.endpointTypes.length === 0 || model.endpointTypes.includes(CHAT_WIRE)
  );
}

/** The title a new conversation is saved under: its first message, clipped. */
export function formatPreview(text: string): string {
  if (!text) return '';
  const chars = [...text];
  return chars.length > PREVIEW_CHARS
    ? `${chars.slice(0, PREVIEW_CHARS).join('')}…`
    : text;
}

export function firstUserText(messages: readonly ChatMessage[]): string {
  return messages.find((m) => m.role === 'user')?.content ?? '';
}

export function toWire(messages: readonly ChatMessage[]): WireMessage[] {
  return messages.map(({ role, content }) => ({ role, content }));
}

export function countReplies(messages: readonly ChatMessage[]): number {
  return messages.filter((m) => m.role === 'assistant').length;
}

function startOfDay(date: Date): number {
  return new Date(
    date.getFullYear(),
    date.getMonth(),
    date.getDate(),
  ).getTime();
}

/** Bucket the (already most-recent-first) list by how long ago it changed. */
export function groupSessions(
  sessions: readonly ChatSessionSummary[],
  now: Date = new Date(),
): SessionGroup[] {
  const today = startOfDay(now);
  const buckets: Record<SessionGroup['key'], ChatSessionSummary[]> = {
    today: [],
    yesterday: [],
    week: [],
    older: [],
  };
  for (const session of sessions) {
    const stamp = Date.parse(session.updatedAt);
    if (Number.isNaN(stamp)) {
      buckets.older.push(session);
      continue;
    }
    const days = Math.round((today - startOfDay(new Date(stamp))) / 86_400_000);
    if (days <= 0) buckets.today.push(session);
    else if (days === 1) buckets.yesterday.push(session);
    else if (days < 7) buckets.week.push(session);
    else buckets.older.push(session);
  }
  return (['today', 'yesterday', 'week', 'older'] as const)
    .filter((key) => buckets[key].length > 0)
    .map((key) => ({ key, items: buckets[key] }));
}

/** Plain-text export of a conversation. */
export function toTranscript(
  messages: readonly ChatMessage[],
  labels: Record<ChatRole, string>,
): string {
  return messages.map((m) => `${labels[m.role]}:\n${m.content}`).join('\n\n');
}

/** `value` is a usable session title (non-blank, within the column width). */
export function isValidTitle(value: string): boolean {
  const trimmed = value.trim();
  return trimmed !== '' && [...trimmed].length <= MAX_TITLE_CHARS;
}

/**
 * Whether the conversation on screen is an unsaved draft: it has messages and
 * is not an entry of the saved list. While the list is still loading (or its
 * read failed) nothing is known about a saved id, so an open saved
 * conversation is never mislabelled; only a conversation with no id yet is a
 * draft in that window.
 */
export function isUnsavedDraft(
  hasMessages: boolean,
  activeSessionId: number | null,
  saved: readonly ChatSessionSummary[] | undefined,
): boolean {
  if (!hasMessages) return false;
  if (activeSessionId === null) return true;
  if (saved === undefined) return false;
  return !saved.some((s) => s.id === activeSessionId);
}
