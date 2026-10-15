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
import { queryOptions } from '@tanstack/react-query';

import { http, tenantApi, tenantUrl, unwrap } from '@/lib/api';

import {
  mapReply,
  mapRoutableModels,
  mapSessionDetail,
  mapSessionList,
} from './lib/chat';
import type {
  ChatReply,
  ChatSessionDetail,
  ChatSessionSummary,
  RoutableModel,
  WireMessage,
} from './types';

export const chatQueryKey = ['chat'] as const;
export const sessionsQueryKey = [...chatQueryKey, 'sessions'] as const;

export const sessionsQueryOptions = queryOptions({
  queryKey: sessionsQueryKey,
  queryFn: async (): Promise<ChatSessionSummary[]> =>
    mapSessionList(await tenantApi.get('/chat/sessions')),
  // The sidebar renders its own error state with a retry button.
  meta: { errorToast: false },
});

/**
 * The routing truth for this tenant: the picker, the send payload and the
 * empty state all come from this one list, never a built-in model name.
 */
export const routableModelsQueryOptions = queryOptions({
  queryKey: [...chatQueryKey, 'routable-models'],
  queryFn: async (): Promise<RoutableModel[]> =>
    mapRoutableModels(await tenantApi.get('/models/routable')),
  staleTime: 60_000,
  // The header renders its own error chip with a retry button.
  meta: { errorToast: false },
});

export function getSession(id: number): Promise<ChatSessionDetail> {
  return tenantApi
    .get<unknown>(`/chat/sessions/${id}`)
    .then((raw) => mapSessionDetail(raw));
}

export interface SessionWriteBody {
  /** Omitted on an update so a renamed conversation keeps its name. */
  title?: string;
  model?: string;
  /** On an update this REPLACES the stored turns; omit to leave them alone. */
  messages?: WireMessage[];
}

export function createSession(
  body: SessionWriteBody,
): Promise<ChatSessionDetail> {
  return tenantApi
    .post<unknown>('/chat/sessions', body)
    .then((raw) => mapSessionDetail(raw));
}

export async function updateSession(
  id: number,
  body: SessionWriteBody,
): Promise<ChatSessionDetail> {
  // tenantApi has no PATCH; same envelope handling as every other call.
  const res = await http.request<unknown>({
    method: 'PATCH',
    url: tenantUrl(`/chat/sessions/${id}`),
    data: body,
  });
  return mapSessionDetail(unwrap<unknown>(res.data, res.status));
}

export async function deleteSession(id: number): Promise<void> {
  await tenantApi.delete(`/chat/sessions/${id}`);
}

/** One non-streaming completion over the full history (`/chat/send`). */
export async function sendChat(
  model: string,
  messages: WireMessage[],
): Promise<ChatReply | null> {
  return mapReply(
    await tenantApi.post<unknown>('/chat/send', { model, messages }),
  );
}
