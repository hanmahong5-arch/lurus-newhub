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

export type ChatRole = 'user' | 'assistant' | 'system';

/** Per-reply figures the server returned for one completion. */
export interface ReplyMeta {
  /** The model that produced the reply. */
  model?: string;
  latencyMs?: number;
  promptTokens?: number;
  completionTokens?: number;
}

export interface ChatMessage {
  /** Client-side identity only; never sent to the server. */
  key: string;
  role: ChatRole;
  content: string;
  /** Present for replies produced in this page view, not for loaded history. */
  meta?: ReplyMeta;
}

/** One row of the saved-conversation list. */
export interface ChatSessionSummary {
  id: number;
  title: string;
  model: string;
  messageCount: number;
  updatedAt: string;
}

export interface ChatSessionDetail {
  id: number;
  title: string;
  model: string;
  messages: ChatMessage[];
}

/** The model call's answer (`POST /chat/send`). */
export interface ChatReply {
  content: string;
  latencyMs?: number;
  promptTokens?: number;
  completionTokens?: number;
}

/** A model the tenant can route (`GET /models/routable`). */
export interface RoutableModel {
  id: string;
  ownedBy: string;
  /** Empty when the backend did not say (treated as "speaks chat"). */
  endpointTypes: string[];
}

/** Wire shape the sessions endpoints take for a turn. */
export interface WireMessage {
  role: ChatRole;
  content: string;
}

export interface SaveFailure {
  /** Server error code, when there was one (e.g. the saved-chat limit). */
  code?: string;
}

export interface SessionGroup {
  key: 'today' | 'yesterday' | 'week' | 'older';
  items: ChatSessionSummary[];
}
