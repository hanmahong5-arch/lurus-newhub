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
import React, { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import HFShell from '../../../components/hifi/HFShell';
import WIPBanner from '../../../components/hifi/WIPBanner';
import ConfirmDialog from '../../../components/common/ConfirmDialog';
import { API, showError, showSuccess } from '../../../helpers';
import { useTenantSlug } from '../../../hooks/common/useTenantSlug';

/* Wave 2: Chat wired to non-stream POST /api/v2/:slug/chat/send.
   In-memory conversation only — no chat_session table yet, so the
   sidebar lists the *current* session (started + turn count), not a
   server-side history. SSE streaming, branching/retry, and
   multi-conversation rehydrate are deferred to v3. */

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
      setMessages((prev) => [
        ...prev,
        {
          role: 'assistant',
          content: data.message.content,
          meta: {
            latency_ms: data.latency_ms,
            prompt_tokens: data.usage?.prompt_tokens,
            completion_tokens: data.usage?.completion_tokens,
          },
        },
      ]);
    } catch (err) {
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
  }, [input, messages, model, sending, tenantSlug, tr]);

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
                'The current conversation will be cleared and cannot be recovered (local session only).',
              ),
            ]}
            confirmText='clear'
            confirmButtonText={tr(
              'console.chat.clear_confirm_btn',
              'Clear conversation',
            )}
            confirmButtonType='danger'
            onConfirm={() => {
              setMessages([]);
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
              onClick={() => setMessages([])}
            >
              {tr('console.chat.new_chat_btn', '+ new chat')}
            </button>
          </div>
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
            <div className='faint mono' style={{ fontSize: 10, marginTop: 2 }}>
              {tr('console.chat.ago', '{{ago}} ago', {
                ago: sessionStartedAgo,
              })}{' '}
              ·{' '}
              {tr('console.chat.turns', '{{count}} turns', {
                count: turnCount,
              })}
            </div>
          </div>
          <div style={{ padding: '14px 16px' }}>
            <WIPBanner
              mini
              reason={tr(
                'console.chat.wip_persistence',
                'persistence + streaming deferred to v3',
              )}
              todo='no chat_session table yet'
            />
          </div>
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
                    <WIPBanner
                      mini
                      reason={tr(
                        'console.chat.wip_retry',
                        'retry / branch deferred to v3',
                      )}
                    />
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
