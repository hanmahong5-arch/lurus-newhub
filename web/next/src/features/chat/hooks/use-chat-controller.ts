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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { ApiError, isApiError } from '@/lib/api';
import { handleServerError } from '@/lib/handle-server-error';

import {
  createSession,
  deleteSession,
  getSession,
  routableModelsQueryOptions,
  sendChat,
  sessionsQueryKey,
  sessionsQueryOptions,
  updateSession,
} from '../api';
import {
  countReplies,
  firstUserText,
  formatPreview,
  makeMessage,
  speaksChat,
  toWire,
} from '../lib/chat';
import type {
  ChatMessage,
  ChatSessionSummary,
  RoutableModel,
  SaveFailure,
} from '../types';

export type ModelsStatus = 'loading' | 'error' | 'empty' | 'ready';

interface PersistResult {
  /** The session id to use from now on, or null when there is none. */
  id: number | null;
  saved: boolean;
  code?: string;
}

/**
 * State machine of the chat page.
 *
 * A turn in flight is tied to the conversation it was sent from by a
 * generation counter, bumped on EVERY navigation (open / new chat / delete of
 * the open one) - even null -> null, which a session-id comparison cannot see
 * (two different unsaved drafts are both `null`). When the turn resolves and
 * the counter moved, the answer is dropped: it is neither rendered into, nor
 * saved onto, whatever conversation is on screen now. The sidebar is never
 * locked while a turn is in flight.
 */
export function useChatController() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const sessionsQuery = useQuery(sessionsQueryOptions);
  const modelsQuery = useQuery(routableModelsQueryOptions);

  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [input, setInput] = useState('');
  // What the user (or an opened session) chose; '' means "the default".
  const [pickedModel, setPickedModel] = useState('');
  const [activeSessionId, setActiveSessionIdState] = useState<number | null>(
    null,
  );
  const [generation, setGeneration] = useState(0);
  const [pendingGeneration, setPendingGeneration] = useState<number | null>(
    null,
  );
  const [openingId, setOpeningId] = useState<number | null>(null);
  const [saveFailure, setSaveFailure] = useState<SaveFailure | null>(null);
  // The model the last send was refused for (no longer routable).
  const [staleModel, setStaleModel] = useState<string | null>(null);

  // Refs mirror the state that async continuations must read synchronously.
  const activeIdRef = useRef<number | null>(null);
  const generationRef = useRef(0);
  const openSeqRef = useRef(0);

  const chatModels = useMemo<RoutableModel[]>(
    () => (modelsQuery.data ?? []).filter(speaksChat),
    [modelsQuery.data],
  );

  // The default is the first routable model from the API - derived, never
  // copied into state, so a refetch cannot clobber an explicit pick and there
  // is no window in which a built-in name could be sent.
  const model = pickedModel || chatModels[0]?.id || '';

  let modelsStatus: ModelsStatus = 'ready';
  if (modelsQuery.data === undefined) {
    modelsStatus = modelsQuery.isError ? 'error' : 'loading';
  } else if (chatModels.length === 0) {
    modelsStatus = 'empty';
  }

  const sending =
    pendingGeneration !== null && pendingGeneration === generation;
  const canSend = modelsStatus === 'ready' && model !== '';

  const sessions = sessionsQuery.data;
  const activeSummary = useMemo(
    () => sessions?.find((s) => s.id === activeSessionId),
    [sessions, activeSessionId],
  );
  const draftTitle = formatPreview(firstUserText(messages));
  const title = activeSummary?.title || draftTitle || t('New chat');
  const turnCount = countReplies(messages);

  const setActiveSession = useCallback((id: number | null) => {
    generationRef.current += 1;
    openSeqRef.current += 1;
    activeIdRef.current = id;
    setGeneration(generationRef.current);
    setActiveSessionIdState(id);
    setOpeningId(null);
  }, []);

  const newChat = useCallback(() => {
    setActiveSession(null);
    setMessages([]);
    setSaveFailure(null);
  }, [setActiveSession]);

  const refreshSessions = useCallback(
    () => qc.invalidateQueries({ queryKey: sessionsQueryKey }),
    [qc],
  );

  const openSession = useCallback(
    async (id: number) => {
      if (id === activeIdRef.current) return;
      openSeqRef.current += 1;
      const seq = openSeqRef.current;
      setOpeningId(id);
      try {
        const detail = await getSession(id);
        if (seq !== openSeqRef.current) return;
        setActiveSession(id);
        setMessages(detail.messages);
        // Adopt the session's own model rather than whatever the picker shows.
        if (detail.model) setPickedModel(detail.model);
        // A session loaded from the server is, by definition, saved.
        setSaveFailure(null);
      } catch (err) {
        if (seq !== openSeqRef.current) return;
        setOpeningId(null);
        handleServerError(err, t('Failed to load conversation'));
        if (isApiError(err) && err.status === 404) void refreshSessions();
      }
    },
    [refreshSessions, setActiveSession, t],
  );

  const removeMutation = useMutation({
    mutationFn: (id: number) => deleteSession(id),
    onSuccess: (_data, id) => {
      qc.setQueryData<ChatSessionSummary[]>(sessionsQueryKey, (prev) =>
        prev?.filter((s) => s.id !== id),
      );
      if (activeIdRef.current === id) newChat();
    },
    onError: (err) => {
      handleServerError(err, t('Failed to delete conversation'));
      if (isApiError(err) && err.status === 404) void refreshSessions();
    },
  });

  const renameMutation = useMutation({
    mutationFn: (vars: { id: number; title: string }) =>
      updateSession(vars.id, { title: vars.title.trim() }),
    onSuccess: (detail, vars) => {
      const next = detail.title || vars.title.trim();
      qc.setQueryData<ChatSessionSummary[]>(sessionsQueryKey, (prev) =>
        prev?.map((s) => (s.id === vars.id ? { ...s, title: next } : s)),
      );
      toast.success(t('Conversation renamed'));
    },
    onError: (err) => {
      handleServerError(err, t('Failed to rename conversation'));
      if (isApiError(err) && err.status === 404) void refreshSessions();
    },
  });

  // Saves the full conversation (create on the first successful turn, replace
  // on every turn after). A failure never rolls back the rendered turn, but it
  // is not silent either: the caller shows a "not saved" marker.
  //
  // On a 404 (the session was deleted elsewhere) the id comes back null, so
  // the NEXT call creates instead of replacing a tombstone forever. On any
  // other failure the id is kept, so the next turn retries the same session
  // rather than forking a duplicate.
  const persist = useCallback(
    async (
      sessionId: number | null,
      all: ChatMessage[],
      turnModel: string,
    ): Promise<PersistResult> => {
      const wire = toWire(all);
      try {
        if (sessionId !== null) {
          // No title: a conversation the user renamed keeps its name.
          await updateSession(sessionId, { model: turnModel, messages: wire });
          return { id: sessionId, saved: true };
        }
        const created = await createSession({
          title: formatPreview(firstUserText(all)),
          model: turnModel,
          messages: wire,
        });
        const id =
          Number.isInteger(created.id) && created.id > 0 ? created.id : null;
        return { id, saved: id !== null };
      } catch (err) {
        const code = isApiError(err) ? err.code : undefined;
        if (isApiError(err) && err.status === 404) {
          return { id: null, saved: false, code };
        }
        return { id: sessionId, saved: false, code };
      }
    },
    [],
  );

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || sending || !canSend) return;
    setStaleModel(null);

    // Snapshot which conversation this turn belongs to BEFORE any await.
    const turnGeneration = generationRef.current;
    const turnSessionId = activeIdRef.current;
    const turnModel = model;
    const prior = messages;
    const next = [...prior, makeMessage('user', text)];

    setMessages(next);
    setInput('');
    setPendingGeneration(turnGeneration);

    try {
      const reply = await sendChat(turnModel, toWire(next));
      if (!reply) throw new ApiError(t('Invalid chat response'));
      if (generationRef.current !== turnGeneration) return;

      const final = [
        ...next,
        makeMessage('assistant', reply.content, {
          model: turnModel,
          latencyMs: reply.latencyMs,
          promptTokens: reply.promptTokens,
          completionTokens: reply.completionTokens,
        }),
      ];
      setMessages(final);

      const result = await persist(turnSessionId, final, turnModel);
      // The user switched conversations during the save itself.
      if (generationRef.current !== turnGeneration) return;
      if (result.id !== turnSessionId) {
        // Same conversation, new identity: adopt it without a navigation bump.
        activeIdRef.current = result.id;
        setActiveSessionIdState(result.id);
      }
      setSaveFailure(result.saved ? null : { code: result.code });
      void refreshSessions();
    } catch (err) {
      // A failure belonging to a conversation that is no longer on screen is
      // neither surfaced nor rolled back onto the one that is.
      if (generationRef.current !== turnGeneration) return;
      if (isApiError(err) && err.code?.toLowerCase() === 'model_not_found') {
        // The routing truth said this model was routable; it stopped being so
        // between the list loading and this send. Refetch so the picker stops
        // offering it.
        toast.error(
          t(
            'Model "{{model}}" is no longer routable for this tenant. Pick another.',
            { model: turnModel },
          ),
        );
        setStaleModel(turnModel);
        void modelsQuery.refetch();
      } else {
        handleServerError(err, t('Failed to send the message'));
      }
      // Roll back the optimistic user message so a retry does not double-send,
      // and give the text back unless the user has already typed something.
      setMessages(prior);
      setInput((current) => (current === '' ? text : current));
    } finally {
      setPendingGeneration((g) => (g === turnGeneration ? null : g));
    }
  }, [
    input,
    sending,
    canSend,
    model,
    messages,
    persist,
    refreshSessions,
    modelsQuery,
    t,
  ]);

  const selectModel = useCallback((id: string) => {
    setPickedModel(id);
    setStaleModel(null);
  }, []);

  return {
    // conversation
    messages,
    title,
    turnCount,
    input,
    setInput,
    sending,
    send,
    newChat,
    saveFailure: messages.length > 0 ? saveFailure : null,
    // sessions
    sessionsQuery,
    activeSessionId,
    openingId,
    openSession,
    removeSession: removeMutation.mutate,
    removingId: removeMutation.isPending ? removeMutation.variables : null,
    renameSession: renameMutation.mutate,
    renamePending: renameMutation.isPending,
    renameAsync: renameMutation.mutateAsync,
    // models
    model,
    models: chatModels,
    modelsStatus,
    modelsQuery,
    selectModel,
    staleModel: staleModel !== null && staleModel === model ? staleModel : null,
    canSend,
  };
}

export type ChatController = ReturnType<typeof useChatController>;
