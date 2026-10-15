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
import {
  Copy,
  Eraser,
  History,
  Link2,
  Loader2,
  TriangleAlert,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { DataTableRowActionMenu } from '@/components/data-table';
import { Button } from '@/components/ui/button';
import {
  DropdownMenuItem,
  DropdownMenuShortcut,
} from '@/components/ui/dropdown-menu';
import {
  NativeSelect,
  NativeSelectOption,
} from '@/components/ui/native-select';

import type { ModelsStatus } from '../hooks/use-chat-controller';
import type { RoutableModel, SaveFailure } from '../types';

export interface ChatHeaderProps {
  title: string;
  turnCount: number;
  hasMessages: boolean;
  saveFailure: SaveFailure | null;
  model: string;
  models: RoutableModel[];
  modelsStatus: ModelsStatus;
  staleModel: string | null;
  sending: boolean;
  onSelectModel: (id: string) => void;
  onRetryModels: () => void;
  onOpenHistory: () => void;
  onCopyConversation: () => void;
  /** Null while the conversation has not been saved yet (no id to link). */
  onCopyLink: (() => void) | null;
  onClear: () => void;
}

function SaveFailedMarker(props: { failure: SaveFailure }) {
  const { t } = useTranslation();
  // The saved-conversation cap has its own remedy; everything else is generic.
  const hint =
    props.failure.code === 'CHAT_SESSION_LIMIT_REACHED'
      ? t(
          'You have reached the saved conversation limit. Delete an old conversation to save new ones.',
        )
      : t(
          'This conversation could not be saved to the server. It currently exists only in this browser tab.',
        );
  return (
    <span
      data-testid='chat-save-failed'
      title={hint}
      className='text-destructive inline-flex items-center gap-1'
    >
      <TriangleAlert className='size-3' aria-hidden='true' />
      {t('Not saved')}
      <span className='sr-only'>{hint}</span>
    </span>
  );
}

function ModelPicker(props: ChatHeaderProps) {
  const { t } = useTranslation();
  const { model, models, modelsStatus } = props;

  if (modelsStatus === 'error') {
    return (
      <div
        className='flex items-center gap-2'
        data-testid='chat-models-error'
        role='alert'
      >
        <span className='text-destructive text-xs'>
          {t('Failed to load models')}
        </span>
        <Button size='xs' variant='outline' onClick={props.onRetryModels}>
          {t('Retry')}
        </Button>
      </div>
    );
  }
  if (modelsStatus === 'empty') {
    return (
      <span
        className='text-muted-foreground bg-muted rounded-md px-2 py-1 text-xs'
        data-testid='chat-no-models'
      >
        {t('No models are routable for this tenant yet')}
      </span>
    );
  }

  return (
    <div className='flex min-w-0 flex-col items-end'>
      <div className='flex items-center gap-1.5'>
        {modelsStatus === 'loading' && (
          <Loader2
            className='text-muted-foreground size-4 animate-spin'
            aria-hidden='true'
          />
        )}
        <NativeSelect
          className='max-w-[min(18rem,50vw)]'
          size='sm'
          aria-label={t('Model')}
          data-testid='chat-model-select'
          disabled={props.sending}
          value={model}
          onChange={(event) => props.onSelectModel(event.target.value)}
        >
          {/* A saved conversation can carry a model that is not in the current
              list (routing changed since it was saved); keep it selectable so
              the picker does not silently jump to another model. */}
          {model !== '' && !models.some((m) => m.id === model) && (
            <NativeSelectOption value={model}>{model}</NativeSelectOption>
          )}
          {model === '' && <NativeSelectOption value=''>—</NativeSelectOption>}
          {models.map((m) => (
            <NativeSelectOption key={m.id} value={m.id}>
              {m.id}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      </div>
      {props.staleModel !== null && (
        <span
          className='text-destructive mt-1 text-xs'
          data-testid='chat-model-not-found-hint'
        >
          {t(
            'Model "{{model}}" is no longer routable for this tenant. Pick another.',
            { model: props.staleModel },
          )}
        </span>
      )}
    </div>
  );
}

/** Conversation title and meta line, the model picker and the overflow menu. */
export function ChatHeader(props: ChatHeaderProps) {
  const { t } = useTranslation();
  return (
    <header className='flex shrink-0 items-start gap-2 border-b px-3 py-2.5 sm:px-4'>
      <Button
        type='button'
        variant='ghost'
        size='icon'
        className='-ms-1 md:hidden'
        aria-label={t('Conversations')}
        onClick={props.onOpenHistory}
      >
        <History aria-hidden='true' />
      </Button>
      <div className='min-w-0 flex-1'>
        <h1 className='truncate text-base font-semibold' title={props.title}>
          {props.title}
        </h1>
        <p className='text-muted-foreground flex flex-wrap items-center gap-x-1.5 text-xs'>
          <span>{t('{{count}} turns', { count: props.turnCount })}</span>
          {props.saveFailure !== null && props.hasMessages && (
            <>
              <span aria-hidden='true'>·</span>
              <SaveFailedMarker failure={props.saveFailure} />
            </>
          )}
        </p>
      </div>
      <ModelPicker {...props} />
      <DataTableRowActionMenu
        ariaLabel={t('Conversation actions')}
        contentClassName='w-52'
        modal={false}
      >
        <DropdownMenuItem
          disabled={!props.hasMessages}
          onClick={props.onCopyConversation}
        >
          {t('Copy conversation')}
          <DropdownMenuShortcut>
            <Copy size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem
          disabled={props.onCopyLink === null}
          onClick={props.onCopyLink ?? undefined}
        >
          {t('Copy link to this conversation')}
          <DropdownMenuShortcut>
            <Link2 size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
        <DropdownMenuItem disabled={!props.hasMessages} onClick={props.onClear}>
          {t('Clear conversation')}
          <DropdownMenuShortcut>
            <Eraser size={16} />
          </DropdownMenuShortcut>
        </DropdownMenuItem>
      </DataTableRowActionMenu>
    </header>
  );
}
