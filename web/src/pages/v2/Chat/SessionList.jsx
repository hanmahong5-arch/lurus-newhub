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
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import HfDialog, { HfDialogFooter } from '../../../components/hifi/HfDialog';

// SessionList — the Chat page's left sidebar: the active (unsaved) draft, the
// "+ new chat" button and the list of server-saved conversations. Split out
// of Chat/index.jsx (cycle-19 L3) to bring that file back under its line
// ceiling; behaviour is unchanged except for the delete confirmation below.
//
// Deleting a saved session used to fire DELETE the instant the row's own
// button was clicked — one stray click on a touchpad, no undo, the
// conversation is gone (repo.DeleteChatSessionOwned is a hard delete). The
// button now only opens a small HfDialog confirmation; the DELETE call
// itself lives in `onDeleteSession`, passed down from Chat/index.jsx, and is
// invoked only from the dialog's confirm button. Deliberately HfDialog, not
// the typed common/ConfirmDialog — losing one saved chat does not deserve
// making the user type its name back.
const SessionList = ({
  sessions,
  activeSessionId,
  messages,
  sessionTitle,
  sessionStartedAgo,
  turnCount,
  deletingSessionId,
  onNewChat,
  onOpenSession,
  onDeleteSession,
}) => {
  const { t: tr } = useTranslation();
  // The id of the session a delete is pending confirmation for, or null.
  const [confirmDeleteId, setConfirmDeleteId] = useState(null);

  const requestDelete = (id, evt) => {
    evt?.stopPropagation?.();
    setConfirmDeleteId(id);
  };

  const confirmDelete = () => {
    const id = confirmDeleteId;
    setConfirmDeleteId(null);
    if (id != null) onDeleteSession(id);
  };

  return (
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
          onClick={onNewChat}
        >
          {tr('console.chat.new_chat_btn', '+ new chat')}
        </button>
      </div>
      {/* The active draft: shown while there are local turns that are
          not yet reflected as an entry in `sessions` below — either the
          persist call for this turn hasn't resolved yet, or it failed
          (best-effort, see persistSession's comment on Chat/index.jsx).
          Once the draft's id appears in `sessions` it renders as a normal
          (highlighted) row there instead, so it is never shown twice. */}
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
          {tr('console.chat.no_saved_sessions', 'no saved conversations yet')}
        </div>
      )}
      {sessions.map((s) => (
        <div
          key={s.id}
          data-testid={`session-list-row-${s.id}`}
          onClick={() => onOpenSession(s.id)}
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
            <div className='faint mono' style={{ fontSize: 10, marginTop: 2 }}>
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
            onClick={(e) => requestDelete(s.id, e)}
            style={{ color: 'var(--hf-err)' }}
          >
            {tr('console.common.delete', 'delete')}
          </button>
        </div>
      ))}

      {confirmDeleteId != null && (
        <HfDialog
          title={tr(
            'console.chat.delete_confirm_title',
            'Delete this conversation?',
          )}
          onClose={() => setConfirmDeleteId(null)}
          width={380}
          testId='session-delete-confirm-dialog'
        >
          <p style={{ margin: 0, fontSize: 13, color: 'var(--hf-ink-2)' }}>
            {tr(
              'console.chat.delete_confirm_body',
              'Its saved history is removed from the server and cannot be restored.',
            )}
          </p>
          <HfDialogFooter>
            <button
              type='button'
              className='btn ghost'
              data-testid='session-delete-cancel-btn'
              onClick={() => setConfirmDeleteId(null)}
            >
              {tr('console.common.cancel', 'cancel')}
            </button>
            <button
              type='button'
              className='btn danger'
              data-testid='session-delete-confirm-btn'
              onClick={confirmDelete}
            >
              {tr('console.common.delete', 'delete')}
            </button>
          </HfDialogFooter>
        </HfDialog>
      )}
    </div>
  );
};

export default SessionList;
