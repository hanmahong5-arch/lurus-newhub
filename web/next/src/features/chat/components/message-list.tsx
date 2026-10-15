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
import { Bot, Check, Copy, Loader2, MessageSquarePlus } from 'lucide-react';
import { memo } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { Markdown } from '@/components/ui/markdown';
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard';
import { cn } from '@/lib/utils';

import type { ModelsStatus } from '../hooks/use-chat-controller';
import type { ChatMessage } from '../types';
import {
  Conversation,
  ConversationContent,
  ConversationScrollButton,
} from './conversation';

function CopyMessageButton(props: { content: string }) {
  const { t } = useTranslation();
  const { copyToClipboard, copiedText } = useCopyToClipboard({ notify: false });
  const copied = copiedText === props.content;
  return (
    <Button
      type='button'
      variant='ghost'
      size='xs'
      className='text-muted-foreground'
      onClick={() => void copyToClipboard(props.content)}
    >
      {copied ? <Check aria-hidden='true' /> : <Copy aria-hidden='true' />}
      {copied ? t('Copied!') : t('Copy')}
    </Button>
  );
}

function ReplyFooter(props: { message: ChatMessage }) {
  const { t } = useTranslation();
  const { message } = props;
  const meta = message.meta;
  const tokens =
    meta?.promptTokens !== undefined || meta?.completionTokens !== undefined
      ? (meta?.promptTokens ?? 0) + (meta?.completionTokens ?? 0)
      : undefined;
  return (
    <div className='text-muted-foreground mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
      {meta?.latencyMs !== undefined && (
        <span className='font-mono tabular-nums'>{meta.latencyMs} ms</span>
      )}
      {tokens !== undefined && (
        <span className='font-mono tabular-nums'>
          {t('{{count}} tokens', { count: tokens })}
        </span>
      )}
      <CopyMessageButton content={message.content} />
    </div>
  );
}

const MessageRow = memo(function MessageRow(props: {
  message: ChatMessage;
  fallbackModel: string;
}) {
  const { t } = useTranslation();
  const { message, fallbackModel } = props;

  if (message.role === 'user') {
    return (
      <div className='flex justify-end' data-role='user'>
        <div className='bg-secondary text-foreground max-w-[85%] rounded-2xl px-3.5 py-2 text-sm leading-relaxed break-words whitespace-pre-wrap'>
          {message.content}
        </div>
      </div>
    );
  }

  if (message.role === 'system') {
    return (
      <div
        className='bg-muted/50 text-muted-foreground rounded-lg border border-dashed px-3 py-2 text-xs leading-relaxed whitespace-pre-wrap'
        data-role='system'
      >
        <span className='mr-1.5 font-medium'>{t('System message')}</span>
        {message.content}
      </div>
    );
  }

  return (
    <div className='flex gap-3' data-role='assistant'>
      <div className='bg-muted text-muted-foreground mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border'>
        <Bot className='size-4' aria-hidden='true' />
      </div>
      <div className='min-w-0 flex-1'>
        <div className='text-muted-foreground mb-1 truncate font-mono text-xs'>
          {message.meta?.model || fallbackModel}
        </div>
        <div className='text-sm leading-relaxed'>
          <Markdown>{message.content}</Markdown>
        </div>
        <ReplyFooter message={message} />
      </div>
    </div>
  );
});

function EmptyThread(props: { modelsStatus: ModelsStatus }) {
  const { t } = useTranslation();
  const blocked = props.modelsStatus === 'empty';
  return (
    <div className='flex min-h-[min(480px,calc(100svh-22rem))] flex-col items-center justify-center gap-3 px-4 text-center'>
      <div className='bg-muted/50 text-muted-foreground flex size-11 items-center justify-center rounded-xl border'>
        <MessageSquarePlus className='size-5' aria-hidden='true' />
      </div>
      <h2 className='text-xl font-semibold tracking-tight'>
        {t('Ask anything to begin')}
      </h2>
      <p className='text-muted-foreground max-w-md text-sm text-balance'>
        {blocked
          ? t(
              'No chat model is routable for this tenant yet, so there is nothing to talk to.',
            )
          : t(
              'Conversations are saved to your account, so you can pick them up from any device.',
            )}
      </p>
    </div>
  );
}

export function MessageList(props: {
  messages: ChatMessage[];
  model: string;
  sending: boolean;
  opening: boolean;
  modelsStatus: ModelsStatus;
}) {
  const { t } = useTranslation();
  const { messages, model, sending, opening, modelsStatus } = props;
  return (
    <Conversation
      className={cn('transition-opacity', opening && 'opacity-60')}
      aria-busy={opening}
    >
      <ConversationContent>
        <div className='mx-auto flex w-full max-w-3xl flex-col gap-6 px-4 py-6'>
          {messages.length === 0 && !sending && (
            <EmptyThread modelsStatus={modelsStatus} />
          )}
          {messages.map((message) => (
            <MessageRow
              key={message.key}
              message={message}
              fallbackModel={model}
            />
          ))}
          {sending && (
            <div
              className='text-muted-foreground flex items-center gap-2 text-sm'
              data-testid='sending-indicator'
              role='status'
            >
              <Loader2 className='size-4 animate-spin' aria-hidden='true' />
              {t('{{model}} is thinking...', { model })}
            </div>
          )}
        </div>
      </ConversationContent>
      <ConversationScrollButton />
    </Conversation>
  );
}
