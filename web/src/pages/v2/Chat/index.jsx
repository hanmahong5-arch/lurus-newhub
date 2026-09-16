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
import React, {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { API, showError, showSuccess } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* Chat is wired to two, independent backends:
   - POST /api/v2/:slug/chat/send (handler.ChatSend) runs the actual
     multi-turn completion via in-process loopback to /v1/chat/completions.
     This is a REAL model call, not a mock — it always was.
   - GET/POST/PATCH/DELETE /api/v2/:slug/chat/sessions[/:id]
     (migration 038, cycle-10 L3) is a client-driven SAVE of a conversation
     /chat/send already returned; persistSession() below calls it after
     every turn. Best-effort in the sense that a save failure never rolls
     back an already-rendered turn, but NOT silent: a failed save sets
     saveFailed, which renders a small "not saved" marker — see
     persistSession's own comment for the 404-vs-transient-failure split.

   A turn in flight is tied to the conversation it was sent from via a
   generation counter (conversationGenerationRef, captured at send()
   entry — see its own comment for why session id equality alone is not
   enough): if the user switches conversations (or starts a new one)
   before the turn resolves, the response is dropped instead of being
   rendered into, or persisted onto, whatever conversation is now on
   screen. The sidebar is deliberately never disabled while a turn is in
   flight — see openSession's own comment — a slow turn must not lock
   navigation.

   OUT OF SCOPE, deliberately, not because it is missing by oversight:
   - SSE streaming. Loopback SSE would require a second HTTP hop that
     re-multiplexes upstream chunks (this process would have to open its
     own SSE connection to itself and re-encode each chunk onto the
     client's connection); that hop does not exist, so /chat/send only
     ever returns a complete response. See handler.ChatSend's own comment.
   - Per-turn retry/branch and rename-from-sidebar: the sessions API this
     page calls has no sub-resource for either, only whole-session
     GET/POST/PATCH/DELETE. */

const DEFAULT_MODEL = 'gpt-4o';

const formatPreview = (text) => {
  if (!text) return '';
  return text.length > 36 ? text.slice(0, 36) + '…' : text;
};

const HFChat = () => {
  // The route is the static /console/v2/chat — it carries no :tenant_slug
  // segment, so reading useParams() here yielded the literal 'default' and
  // every send was answered 404 by TenantSlugGuard. Same source as the other
  // v2 pages.
  const tenantSlug = useTenantSlug();
  // Aliased to `tr` per the v2 console convention.
  const { t: tr } = useTranslation();

  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const [sending, setSending] = useState(false);
  const [model] = useState(DEFAULT_MODEL);
  const [sessionStartedAt] = useState(() => Date.now());
  // "⋯" clear-conversation confirm dialog
  const [clearVisible, setClearVisible] = useState(false);

  // Sidebar / persistence — sessions is the caller's own saved
  // conversations (GET .../chat/sessions), activeSessionId is null for an
  // unsaved draft that has not completed its first turn yet.
  const [sessions, setSessions] = useState([]);
  const [activeSessionId, setActiveSessionId] = useState(null);
  const [deletingSessionId, setDeletingSessionId] = useState(null);
  // True when the most recent save attempt for the current conversation
  // did not persist (see persistSession's own comment) — surfaced as a
  // small "not saved" marker so a swallowed failure is not invisible.
  const [saveFailed, setSaveFailed] = useState(false);

  // activeSessionIdRef mirrors activeSessionId but updates SYNCHRONOUSLY
  // (state updates do not) — persistSession needs the actual id to PATCH
  // against. conversationGenerationRef is the switch DETECTOR send() uses:
  // it is bumped on every navigation (newChat/openSession), even
  // null -> null (unsaved draft -> "+ new chat" -> another unsaved draft)
  // — a case activeSessionId equality alone cannot see, since both sides
  // are null. Comparing activeSessionId across the same await would have
  // missed exactly that case: a fresh, never-saved draft's first turn
  // resolving after the user already started ANOTHER new (still-null)
  // chat would render straight into it. All writes to activeSessionId go
  // through setActiveSession below so ref/generation/state never drift.
  const activeSessionIdRef = useRef(null);
  const conversationGenerationRef = useRef(0);
  const setActiveSession = useCallback((id) => {
    conversationGenerationRef.current += 1;
    activeSessionIdRef.current = id;
    setActiveSessionId(id);
  }, []);

  const loadSessions = useCallback(async () => {
    try {
      const res = await API.get(`/api/v2/${tenantSlug}/chat/sessions`);
      setSessions(res?.data?.data?.sessions || []);
    } catch (_) {
      // The sidebar's history is a convenience on top of a chat that
      // already works without it — a listing failure must not block
      // sending or receiving a turn.
    }
  }, [tenantSlug]);

  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const newChat = useCallback(() => {
    setActiveSession(null);
    setMessages([]);
    setSaveFailed(false);
  }, [setActiveSession]);

  // Deliberately NOT gated on `sending` — a turn in flight for the
  // conversation being left must not lock the sidebar (see send()'s own
  // generation-token check, which is what actually protects that turn's
  // response from landing in the conversation switched to below).
  const openSession = useCallback(
    async (id) => {
      if (id === activeSessionId) return;
      try {
        const res = await API.get(`/api/v2/${tenantSlug}/chat/sessions/${id}`);
        const data = res?.data?.data;
        if (!data) return;
        setActiveSession(id);
        setMessages(
          (data.messages || []).map(({ role, content }) => ({ role, content })),
        );
        // A session loaded from the server is, by definition, saved —
        // clear any marker left over from the conversation just left.
        setSaveFailed(false);
      } catch (err) {
        showError(
          err?.response?.data?.message ||
            err?.message ||
            tr('console.chat.load_failed', 'Failed to load conversation'),
        );
      }
    },
    [tenantSlug, activeSessionId, tr, setActiveSession],
  );

  const deleteSession = useCallback(
    async (id, evt) => {
      evt?.stopPropagation?.();
      setDeletingSessionId(id);
      try {
        await API.delete(`/api/v2/${tenantSlug}/chat/sessions/${id}`);
        setSessions((prev) => prev.filter((s) => s.id !== id));
        if (id === activeSessionId) {
          newChat();
        }
      } catch (err) {
        showError(
          err?.response?.data?.message ||
            err?.message ||
            tr('console.chat.delete_failed', 'Failed to delete conversation'),
        );
      } finally {
        setDeletingSessionId(null);
      }
    },
    [tenantSlug, activeSessionId, newChat],
  );

  // persistSession SAVES the full conversation (create on first successful
  // turn, PATCH-replace on every turn after) — best-effort in the sense
  // that a failure here never rolls back the already-rendered chat turn
  // (same convention as ResponseRegistry's post-hoc insert hook on the
  // backend, entity.ChatSession's doc comment), but NOT silent: it
  // returns { id, saved } so the caller can update activeSessionId and
  // show the "not saved" marker (see saveFailed) instead of the failure
  // vanishing with no signal.
  //
  // Returns:
  //   { id: <the session id to use from now on, or null>, saved: bool }
  // On a 404 (the session was deleted — another tab, or this page's own
  // delete button firing while a turn was in flight, see deleteSession),
  // `id` comes back null instead of the dead sessionId, so the NEXT call
  // falls back to POST (create) instead of PATCHing a tombstone forever.
  // On any other failure (network, 5xx) `id` is left at sessionId so the
  // next turn retries the same PATCH rather than forking a duplicate
  // session — either way `saved` is false so the caller can surface it.
  const persistSession = useCallback(
    async (sessionId, title, allMessages) => {
      const messages = allMessages.map(({ role, content }) => ({
        role,
        content,
      }));
      try {
        if (sessionId) {
          // PATCH's request shape (updateChatSessionRequest, backend) has
          // no `model` field — this page has no model picker to change it
          // (`model` is a fixed useState with no setter) — so it is
          // deliberately left out rather than sent and silently dropped.
          await API.patch(`/api/v2/${tenantSlug}/chat/sessions/${sessionId}`, {
            title,
            messages,
          });
          return { id: sessionId, saved: true };
        }
        const res = await API.post(`/api/v2/${tenantSlug}/chat/sessions`, {
          title,
          model,
          messages,
        });
        const id = res?.data?.data?.id ?? null;
        return { id, saved: id != null };
      } catch (err) {
        if (err?.response?.status === 404) {
          return { id: null, saved: false };
        }
        return { id: sessionId ?? null, saved: false };
      }
    },
    [tenantSlug, model],
  );

  const sessionTitle = useMemo(() => {
    const firstUser = messages.find((m) => m.role === 'user');
    return firstUser
      ? formatPreview(firstUser.content)
      : tr('console.chat.new_chat', 'new chat');
  }, [messages, tr]);

  const turnCount = useMemo(
    () => messages.filter((m) => m.role === 'assistant').length,
    [messages],
  );

  const sessionStartedAgo = useMemo(() => {
    const seconds = Math.floor((Date.now() - sessionStartedAt) / 1000);
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
    return `${Math.floor(seconds / 3600)}h`;
  }, [sessionStartedAt, messages]);

  const send = useCallback(async () => {
    const text = input.trim();
    if (!text || sending) return;

    // Snapshot which conversation this turn belongs to BEFORE any await.
    // turnGeneration is compared against conversationGenerationRef.current
    // after every await below — if it no longer matches, the user
    // navigated (openSession/newChat) while this turn was in flight;
    // neither is ever disabled during `sending` (a slow turn must not
    // lock the sidebar). turnSessionId is a SEPARATE snapshot: the actual
    // session id (or null, for an unsaved draft) this turn's save should
    // target — it is NOT what detects a switch (two different unsaved
    // drafts are both null; see conversationGenerationRef's own comment
    // for why session-id equality alone cannot tell them apart). On a
    // mismatch the response is dropped outright: no setMessages (would
    // clobber the conversation now on screen), no persistSession (would
    // save this turn's history onto the conversation the user switched
    // to — this is the data-loss regression the accompanying tests pin).
    const turnGeneration = conversationGenerationRef.current;
    const turnSessionId = activeSessionIdRef.current;

    const nextMessages = [...messages, { role: 'user', content: text }];
    setMessages(nextMessages);
    setInput('');
    setSending(true);

    try {
      const res = await API.post(`/api/v2/${tenantSlug}/chat/send`, {
        model,
        messages: nextMessages.map(({ role, content }) => ({ role, content })),
      });
      const data = res?.data?.data;
      if (!data?.message) {
        throw new Error(
          res?.data?.message ||
            tr('console.chat.invalid_response', 'invalid chat response'),
        );
      }

      if (conversationGenerationRef.current !== turnGeneration) {
        return;
      }

      const assistantMsg = {
        role: 'assistant',
        content: data.message.content,
        meta: {
          latency_ms: data.latency_ms,
          prompt_tokens: data.usage?.prompt_tokens,
          completion_tokens: data.usage?.completion_tokens,
        },
      };
      const finalMessages = [...nextMessages, assistantMsg];
      setMessages(finalMessages);

      // Save — see persistSession's own comment: best-effort in that a
      // failure here does not roll back the turn rendered above, but NOT
      // silent (saveFailed below).
      const title = formatPreview(
        finalMessages.find((m) => m.role === 'user')?.content || '',
      );
      const result = await persistSession(turnSessionId, title, finalMessages);

      if (conversationGenerationRef.current !== turnGeneration) {
        // The user switched conversations during the persist call itself.
        return;
      }
      if (result.id !== turnSessionId) {
        setActiveSession(result.id);
      }
      setSaveFailed(!result.saved);
      loadSessions();
    } catch (err) {
      if (conversationGenerationRef.current !== turnGeneration) {
        // The conversation this failure belongs to is no longer on
        // screen — do not surface an error for, or roll back, whatever
        // conversation the user has since switched to.
        return;
      }
      showError(
        err?.response?.data?.message ||
          err?.message ||
          tr('console.chat.send_failed', 'Send failed'),
      );
      // Roll back the optimistic user message on failure so the next
      // retry doesn't double-send.
      setMessages(messages);
    } finally {
      setSending(false);
    }
  }, [
    input,
    messages,
    model,
    sending,
    tenantSlug,
    tr,
    persistSession,
    loadSessions,
    setActiveSession,
  ]);

  const onKeyDown = useCallback(
    (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        send();
      }
    },
    [send],
  );

  return (
    <HFShell
      active='chat'
      crumbs={[
        tr('console.nav.section_workspace', 'workspace'),
        tr('console.chat.crumb', 'chat'),
      ]}
      actions={
        <>
          <button
            type='button'
            className='btn'
            data-testid='chat-share-btn'
            onClick={() => {
              try {
                navigator.clipboard?.writeText(window.location.href);
              } catch (_) {}
              showSuccess(tr('console.chat.link_copied', 'Link copied'));
            }}
          >
            {tr('console.chat.share', 'share ↗')}
          </button>
          <button
            type='button'
            className='btn'
            data-testid='chat-more-btn'
            onClick={() => setClearVisible(true)}
          >
            ⋯
          </button>
          <ConfirmDialog
            visible={clearVisible}
            title={tr('console.chat.clear_title', 'Clear current conversation')}
            consequenceList={[
              tr(
                'console.chat.clear_consequence',
                'Starts a new conversation. Anything already saved stays in the sidebar; unsent input is lost.',
              ),
            ]}
            confirmText='clear'
            confirmButtonText={tr(
              'console.chat.clear_confirm_btn',
              'Clear conversation',
            )}
            confirmButtonType='danger'
            onConfirm={() => {
              newChat();
              setClearVisible(false);
            }}
            onCancel={() => setClearVisible(false)}
          />
        </>
      }
    >
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: '260px 1fr',
          height: '100%',
          minHeight: 0,
        }}
      >
        <div
          style={{
            borderRight: '1px solid var(--hf-rule)',
            background: 'var(--hf-paper)',
            overflow: 'auto',
          }}
        >
          <div style={{ padding: '14px 16px' }}>
            <button
              type='button'
              className='btn primary'
              style={{ width: '100%', justifyContent: 'center' }}
              onClick={newChat}
            >
              {tr('console.chat.new_chat_btn', '+ new chat')}
            </button>
          </div>
          {/* The active draft: shown while there are local turns that are
              not yet reflected as an entry in `sessions` below — either the
              persist call for this turn hasn't resolved yet, or it failed
              (best-effort, see persistSession's comment). Once the draft's
              id appears in `sessions` it renders as a normal (highlighted)
              row there instead, so it is never shown twice. */}
          {messages.length > 0 &&
            !sessions.some((s) => s.id === activeSessionId) && (
              <>
                <div className='lbl' style={{ padding: '8px 16px' }}>
                  {tr('console.chat.current_session', 'current session')}
                </div>
                <div
                  data-testid='session-row'
                  style={{
                    padding: '10px 16px',
                    background: 'var(--hf-elev)',
                    borderLeft: '2px solid var(--hf-accent)',
                    borderBottom: '1px solid var(--hf-rule)',
                  }}
                >
                  <div
                    className='strong'
                    style={{
                      fontSize: 12,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {sessionTitle}
                  </div>
                  <div
                    className='faint mono'
                    style={{ fontSize: 10, marginTop: 2 }}
                  >
                    {tr('console.chat.ago', '{{ago}} ago', {
                      ago: sessionStartedAgo,
                    })}{' '}
                    ·{' '}
                    {tr('console.chat.turns', '{{count}} turns', {
                      count: turnCount,
                    })}
                  </div>
                </div>
              </>
            )}

          <div className='lbl' style={{ padding: '8px 16px' }}>
            {tr('console.chat.saved_sessions', 'saved conversations')}
          </div>
          {sessions.length === 0 && (
            <div
              className='faint'
              style={{ padding: '4px 16px 14px', fontSize: 11 }}
            >
              {tr(
                'console.chat.no_saved_sessions',
                'no saved conversations yet',
              )}
            </div>
          )}
          {sessions.map((s) => (
            <div
              key={s.id}
              data-testid={`session-list-row-${s.id}`}
              onClick={() => openSession(s.id)}
              role='button'
              tabIndex={0}
              style={{
                padding: '10px 16px',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: 8,
                background:
                  s.id === activeSessionId ? 'var(--hf-elev)' : 'transparent',
                borderLeft:
                  s.id === activeSessionId
                    ? '2px solid var(--hf-accent)'
                    : '2px solid transparent',
                borderBottom: '1px solid var(--hf-rule)',
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  className='strong'
                  style={{
                    fontSize: 12,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {s.title || tr('console.chat.new_chat', 'new chat')}
                </div>
                <div
                  className='faint mono'
                  style={{ fontSize: 10, marginTop: 2 }}
                >
                  {tr('console.chat.messages_count', '{{count}} messages', {
                    count: s.message_count,
                  })}
                </div>
              </div>
              <button
                type='button'
                className='btn ghost sm'
                data-testid={`session-delete-${s.id}`}
                disabled={deletingSessionId === s.id}
                onClick={(e) => deleteSession(s.id, e)}
              >
                {tr('console.common.delete', 'delete')}
              </button>
            </div>
          ))}
        </div>

        <div
          style={{
            display: 'grid',
            gridTemplateRows: 'auto 1fr auto',
            minHeight: 0,
          }}
        >
          <div
            style={{
              padding: '12px 22px',
              borderBottom: '1px solid var(--hf-rule)',
              display: 'flex',
              alignItems: 'center',
              gap: 10,
            }}
          >
            <div>
              <div className='display' style={{ fontSize: 17 }}>
                {sessionTitle}
              </div>
              <div className='muted mono' style={{ fontSize: 10 }}>
                {tr('console.chat.turns', '{{count}} turns', {
                  count: turnCount,
                })}{' '}
                ·{' '}
                {tr('console.chat.started_ago', 'started {{ago}} ago', {
                  ago: sessionStartedAgo,
                })}
                {saveFailed && messages.length > 0 && (
                  <>
                    {' '}
                    ·{' '}
                    <span
                      data-testid='chat-save-failed'
                      title={tr(
                        'console.chat.not_saved_hint',
                        'This conversation could not be saved to the server — it currently exists only in this browser tab.',
                      )}
                      style={{ color: 'var(--hf-danger, #c0392b)' }}
                    >
                      {tr('console.chat.not_saved', 'not saved')}
                    </span>
                  </>
                )}
              </div>
            </div>
            <span style={{ flex: 1 }} />
            <span className='pill'>
              <span className='dot ok' /> {model}
            </span>
          </div>

          <div
            style={{
              overflow: 'auto',
              padding: '24px 22px',
              maxWidth: 800,
              margin: '0 auto',
              width: '100%',
            }}
          >
            {messages.length === 0 && (
              <div
                className='muted'
                style={{ textAlign: 'center', padding: '60px 0' }}
              >
                {tr('console.chat.empty_state', 'ask anything to begin')}
              </div>
            )}
            {messages.map((m, i) => (
              <div key={i} style={{ marginBottom: 22 }}>
                <div
                  className='lbl'
                  style={{
                    marginBottom: 6,
                    color:
                      m.role === 'user'
                        ? 'var(--hf-ink-3)'
                        : 'var(--hf-accent)',
                  }}
                >
                  {m.role === 'user' ? tr('console.chat.you', 'you') : model}
                </div>
                <div
                  style={{
                    fontSize: 14,
                    lineHeight: 1.65,
                    color: 'var(--hf-ink)',
                    whiteSpace: 'pre-wrap',
                  }}
                >
                  {m.content}
                </div>
                {m.meta && (
                  <div
                    style={{
                      display: 'flex',
                      gap: 12,
                      marginTop: 8,
                      fontSize: 10,
                    }}
                  >
                    <span className='faint mono'>
                      {m.meta.latency_ms ?? 0}ms
                    </span>
                    <span className='faint mono'>
                      {(m.meta.prompt_tokens ?? 0) +
                        (m.meta.completion_tokens ?? 0)}
                      t
                    </span>
                    <span style={{ flex: 1 }} />
                    <button
                      type='button'
                      className='btn ghost sm'
                      onClick={() => navigator.clipboard?.writeText(m.content)}
                    >
                      {tr('console.common.copy', 'copy')}
                    </button>
                  </div>
                )}
              </div>
            ))}
            {sending && (
              <div className='muted' data-testid='sending-indicator'>
                {tr('console.chat.thinking', '{{model}} is thinking…', {
                  model,
                })}
              </div>
            )}
          </div>

          <div
            style={{
              padding: 22,
              borderTop: '1px solid var(--hf-rule)',
              background: 'var(--hf-paper)',
            }}
          >
            <div
              className='panel'
              style={{ padding: 14, maxWidth: 800, margin: '0 auto' }}
            >
              <textarea
                aria-label='message-input'
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={onKeyDown}
                placeholder={tr('console.chat.ph_input', 'ask anything…')}
                disabled={sending}
                style={{
                  width: '100%',
                  minHeight: 48,
                  fontSize: 13,
                  border: 'none',
                  outline: 'none',
                  background: 'transparent',
                  resize: 'vertical',
                  color: 'var(--hf-ink)',
                  fontFamily: 'inherit',
                }}
              />
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  marginTop: 8,
                }}
              >
                <span style={{ flex: 1 }} />
                <span className='muted mono' style={{ fontSize: 10 }}>
                  {input.length} / 200,000
                </span>
                <button
                  type='button'
                  className='btn primary'
                  onClick={send}
                  disabled={sending || !input.trim()}
                >
                  {tr('console.chat.send_btn', '▶ send')}{' '}
                  <span className='kbd' style={{ marginLeft: 4 }}>
                    ↵
                  </span>
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </HFShell>
  );
};

export default HFChat;
