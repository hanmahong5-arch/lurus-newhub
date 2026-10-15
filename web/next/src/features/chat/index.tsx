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
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ConfirmDialog } from '@/components/confirm-dialog';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard';

import { ChatHeader } from './components/chat-header';
import { Composer } from './components/composer';
import { MessageList } from './components/message-list';
import { RenameDialog } from './components/rename-dialog';
import { SessionSidebar, type DraftEntry } from './components/session-sidebar';
import { useChatController } from './hooks/use-chat-controller';
import {
  firstUserText,
  formatPreview,
  isUnsavedDraft,
  toTranscript,
} from './lib/chat';
import type { ChatSessionSummary } from './types';

/**
 * Server-side chat: saved conversations on the left, the open thread on the
 * right. Replies arrive whole (the backend completion is non-streaming) and
 * every finished turn is saved back to the caller's conversation list.
 */
export function ChatPage(props: {
  /** Conversation named in the URL (?session=<id>); opened on arrival. */
  sessionParam?: number;
  /** Called when the open conversation changes, to keep the URL in step. */
  onSessionChange?: (id: number | null) => void;
} = {}) {
  const { t } = useTranslation();
  const chat = useChatController();
  const { copyToClipboard } = useCopyToClipboard();
  const { sessionParam, onSessionChange } = props;

  // A linked conversation is opened once on arrival (and again if the URL
  // changes under us). openSession ignores the id that is already open, so
  // the URL echo of our own navigation is a no-op.
  const { openSession } = chat;
  useEffect(() => {
    if (sessionParam !== undefined) void openSession(sessionParam);
  }, [sessionParam, openSession]);

  // Keep the URL naming the open conversation, so the address bar (and the
  // copied link) always leads back to it. While the linked conversation is
  // still loading nothing is open yet; clearing the URL then would lose it.
  const awaitingLinked = useRef(sessionParam !== undefined);
  useEffect(() => {
    const active = chat.activeSessionId;
    if (active === null && awaitingLinked.current) return;
    awaitingLinked.current = false;
    if ((sessionParam ?? null) !== active) onSessionChange?.(active);
  }, [chat.activeSessionId, sessionParam, onSessionChange]);

  const [historyOpen, setHistoryOpen] = useState(false);
  const [clearOpen, setClearOpen] = useState(false);
  const [renameTarget, setRenameTarget] = useState<ChatSessionSummary | null>(
    null,
  );
  const [deleteTarget, setDeleteTarget] = useState<ChatSessionSummary | null>(
    null,
  );

  const hasMessages = chat.messages.length > 0;
  const draft: DraftEntry | null = isUnsavedDraft(
    hasMessages,
    chat.activeSessionId,
    chat.sessionsQuery.data,
  )
    ? {
        title: formatPreview(firstUserText(chat.messages)) || t('New chat'),
        turnCount: chat.turnCount,
      }
    : null;

  const sidebarProps = {
    sessions: chat.sessionsQuery,
    activeSessionId: chat.activeSessionId,
    draft,
    openingId: chat.openingId,
    removingId: chat.removingId,
    onNewChat: () => {
      chat.newChat();
      setHistoryOpen(false);
    },
    onOpen: (id: number) => {
      void chat.openSession(id);
      setHistoryOpen(false);
    },
    onRename: setRenameTarget,
    onDelete: setDeleteTarget,
  };

  const copyConversation = () => {
    void copyToClipboard(
      toTranscript(chat.messages, {
        user: t('You'),
        assistant: t('Assistant'),
        system: t('System message'),
      }),
    );
  };

  const activeId = chat.activeSessionId;
  const copyLink =
    activeId === null
      ? null
      : () => {
          void copyToClipboard(
            `${window.location.origin}/chat?session=${activeId}`,
          );
        };

  return (
    <div className='relative flex h-[calc(100svh-4.5rem)] min-h-[28rem] w-full min-w-0 flex-1 overflow-hidden sm:h-[calc(100svh-5rem)]'>
      <aside
        className='bg-muted/20 hidden w-72 shrink-0 border-e md:block'
        aria-label={t('Conversations')}
      >
        <SessionSidebar {...sidebarProps} />
      </aside>

      <section className='flex min-w-0 flex-1 flex-col'>
        <ChatHeader
          title={chat.title}
          turnCount={chat.turnCount}
          hasMessages={hasMessages}
          saveFailure={chat.saveFailure}
          model={chat.model}
          models={chat.models}
          modelsStatus={chat.modelsStatus}
          staleModel={chat.staleModel}
          sending={chat.sending}
          onSelectModel={chat.selectModel}
          onRetryModels={() => void chat.modelsQuery.refetch()}
          onOpenHistory={() => setHistoryOpen(true)}
          onCopyConversation={copyConversation}
          onCopyLink={copyLink}
          onClear={() => setClearOpen(true)}
        />
        <MessageList
          messages={chat.messages}
          model={chat.model}
          sending={chat.sending}
          opening={chat.openingId !== null}
          modelsStatus={chat.modelsStatus}
        />
        <Composer
          value={chat.input}
          onChange={chat.setInput}
          onSend={() => void chat.send()}
          sending={chat.sending}
          canSend={chat.canSend}
          modelsStatus={chat.modelsStatus}
        />
      </section>

      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent side='left' className='w-80 max-w-[85vw] gap-0 p-0'>
          <SheetHeader className='sr-only'>
            <SheetTitle>{t('Conversations')}</SheetTitle>
            <SheetDescription>{t('Your saved conversations')}</SheetDescription>
          </SheetHeader>
          <SessionSidebar {...sidebarProps} />
        </SheetContent>
      </Sheet>

      <RenameDialog
        session={renameTarget}
        pending={chat.renamePending}
        onClose={() => setRenameTarget(null)}
        onSubmit={(session, title) => {
          chat
            .renameAsync({ id: session.id, title })
            .then(() => setRenameTarget(null))
            // The failure is already reported by the mutation's onError.
            .catch(() => undefined);
        }}
      />

      <ConfirmDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => {
          if (!open) setDeleteTarget(null);
        }}
        destructive
        title={t('Delete this conversation?')}
        desc={t(
          'Its saved history is removed from the server and cannot be restored.',
        )}
        confirmText={t('Delete')}
        handleConfirm={() => {
          if (deleteTarget !== null) chat.removeSession(deleteTarget.id);
          setDeleteTarget(null);
        }}
      />

      <ConfirmDialog
        open={clearOpen}
        onOpenChange={setClearOpen}
        destructive
        title={t('Clear current conversation')}
        desc={t(
          'Starts a new conversation. Anything already saved stays in the sidebar; unsent input is kept.',
        )}
        confirmText={t('Clear conversation')}
        handleConfirm={() => {
          chat.newChat();
          setClearOpen(false);
        }}
      />
    </div>
  );
}
