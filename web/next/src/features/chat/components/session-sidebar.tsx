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
import type { UseQueryResult } from '@tanstack/react-query';
import { Loader2, MessageSquare, Pencil, Plus, Trash2 } from 'lucide-react';
import type { TFunction } from 'i18next';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { DataTableRowActionMenu } from '@/components/data-table';
import { ErrorState } from '@/components/error-state';
import { Button } from '@/components/ui/button';
import {
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu';
import { Skeleton } from '@/components/ui/skeleton';
import { getServerErrorMessage } from '@/lib/server-error-message';
import { cn } from '@/lib/utils';

import { groupSessions } from '../lib/chat';
import type { ChatSessionSummary, SessionGroup } from '../types';

/** The conversation on screen that has no entry in the saved list (yet). */
export interface DraftEntry {
  title: string;
  turnCount: number;
}

export interface SessionSidebarProps {
  sessions: UseQueryResult<ChatSessionSummary[]>;
  activeSessionId: number | null;
  draft: DraftEntry | null;
  openingId: number | null;
  removingId: number | null | undefined;
  onNewChat: () => void;
  onOpen: (id: number) => void;
  onRename: (session: ChatSessionSummary) => void;
  onDelete: (session: ChatSessionSummary) => void;
}

function groupLabel(t: TFunction, key: SessionGroup['key']): string {
  switch (key) {
    case 'today':
      return t('Today');
    case 'yesterday':
      return t('Yesterday');
    case 'week':
      return t('Previous 7 days');
    default:
      return t('Older');
  }
}

function SessionRow(props: {
  session: ChatSessionSummary;
  active: boolean;
  opening: boolean;
  removing: boolean;
  onOpen: SessionSidebarProps['onOpen'];
  onRename: SessionSidebarProps['onRename'];
  onDelete: SessionSidebarProps['onDelete'];
}) {
  const { t } = useTranslation();
  const { session, active, opening, removing } = props;
  const title = session.title || t('New chat');
  return (
    <li
      className={cn(
        'group/row hover:bg-accent/60 relative rounded-lg transition-colors',
        active && 'bg-accent',
        removing && 'pointer-events-none opacity-50',
      )}
      data-testid={`session-row-${session.id}`}
    >
      <button
        type='button'
        className='focus-visible:ring-ring/50 flex w-full min-w-0 flex-col rounded-lg py-2 ps-3 pe-10 text-start outline-none focus-visible:ring-2'
        aria-current={active ? 'true' : undefined}
        onClick={() => props.onOpen(session.id)}
      >
        <span className='truncate text-sm font-medium'>{title}</span>
        <span className='text-muted-foreground text-xs'>
          {t('{{count}} messages', { count: session.messageCount })}
        </span>
      </button>
      <div className='absolute end-1 top-1/2 flex -translate-y-1/2 items-center'>
        {opening || removing ? (
          <Loader2
            className='text-muted-foreground me-2 size-4 animate-spin'
            aria-hidden='true'
          />
        ) : (
          <div className='has-[[data-popup-open]]:opacity-100 pointer-coarse:opacity-100 opacity-0 transition-opacity focus-within:opacity-100 group-hover/row:opacity-100'>
            <DataTableRowActionMenu
              ariaLabel={t('Actions for {{name}}', { name: title })}
              contentClassName='w-44'
              modal={false}
            >
              <DropdownMenuItem onClick={() => props.onRename(session)}>
                {t('Rename')}
                <DropdownMenuShortcut>
                  <Pencil size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() => props.onDelete(session)}
                className='text-destructive focus:text-destructive'
              >
                {t('Delete conversation')}
                <DropdownMenuShortcut>
                  <Trash2 size={16} />
                </DropdownMenuShortcut>
              </DropdownMenuItem>
            </DataTableRowActionMenu>
          </div>
        )}
      </div>
    </li>
  );
}

function SessionListBody(props: SessionSidebarProps) {
  const { t } = useTranslation();
  const { sessions, draft } = props;
  const groups = useMemo(
    () => groupSessions(sessions.data ?? []),
    [sessions.data],
  );

  if (sessions.isPending) {
    return (
      <div
        className='space-y-2 p-2'
        role='status'
        aria-label={t('Loading conversations...')}
      >
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} className='h-11 w-full rounded-lg' />
        ))}
      </div>
    );
  }

  // A failed read is an error, never an empty list.
  if (sessions.isError && sessions.data === undefined) {
    return (
      <div data-testid='chat-sessions-error'>
        <ErrorState
          className='min-h-0 py-8'
          title={t('Failed to load conversations')}
          description={getServerErrorMessage(sessions.error)}
          onRetry={() => void sessions.refetch()}
        />
      </div>
    );
  }

  return (
    <div className='space-y-3 p-2'>
      {draft !== null && (
        <section aria-label={t('Current conversation')}>
          <h3 className='text-muted-foreground px-3 pb-1 text-xs font-medium'>
            {t('Current conversation')}
          </h3>
          <div
            className='bg-accent rounded-lg px-3 py-2'
            data-testid='session-draft'
          >
            <div className='truncate text-sm font-medium'>{draft.title}</div>
            <div className='text-muted-foreground text-xs'>
              {t('{{count}} turns', { count: draft.turnCount })} ·{' '}
              {t('Not saved yet')}
            </div>
          </div>
        </section>
      )}
      {groups.length === 0 ? (
        <p className='text-muted-foreground px-3 py-4 text-xs'>
          {t('No saved conversations yet')}
        </p>
      ) : (
        groups.map((group) => (
          <section key={group.key} aria-label={groupLabel(t, group.key)}>
            <h3 className='text-muted-foreground px-3 pb-1 text-xs font-medium'>
              {groupLabel(t, group.key)}
            </h3>
            <ul className='space-y-0.5'>
              {group.items.map((session) => (
                <SessionRow
                  key={session.id}
                  session={session}
                  active={session.id === props.activeSessionId}
                  opening={session.id === props.openingId}
                  removing={session.id === props.removingId}
                  onOpen={props.onOpen}
                  onRename={props.onRename}
                  onDelete={props.onDelete}
                />
              ))}
            </ul>
          </section>
        ))
      )}
    </div>
  );
}

/** Left rail: new-chat button plus the caller's saved conversations. */
export function SessionSidebar(props: SessionSidebarProps) {
  const { t } = useTranslation();
  return (
    <div className='flex h-full min-h-0 flex-col'>
      <div className='shrink-0 p-3'>
        <Button className='w-full justify-center' onClick={props.onNewChat}>
          <Plus aria-hidden='true' />
          {t('New chat')}
        </Button>
      </div>
      <div className='text-muted-foreground flex shrink-0 items-center gap-1.5 px-5 pb-1 text-xs font-medium'>
        <MessageSquare className='size-3.5' aria-hidden='true' />
        {t('Saved conversations')}
      </div>
      <div className='min-h-0 flex-1 overflow-y-auto'>
        <SessionListBody {...props} />
      </div>
    </div>
  );
}
