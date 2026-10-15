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
import { CornerDownLeft, Loader2, SendHorizontal } from 'lucide-react';
import type { KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '@/components/ui/button';
import { Kbd } from '@/components/ui/kbd';
import { Textarea } from '@/components/ui/textarea';

import type { ModelsStatus } from '../hooks/use-chat-controller';
import { MAX_MESSAGE_CHARS } from '../lib/chat';

export function Composer(props: {
  value: string;
  onChange: (value: string) => void;
  onSend: () => void;
  sending: boolean;
  canSend: boolean;
  modelsStatus: ModelsStatus;
}) {
  const { t } = useTranslation();
  const { value, onChange, onSend, sending, canSend, modelsStatus } = props;
  const blank = value.trim() === '';

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    // Enter confirms an IME candidate; it must not send half-typed text.
    if (event.nativeEvent.isComposing) return;
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      onSend();
    }
  };

  let hint: string | null = null;
  if (modelsStatus === 'loading') hint = t('Loading models...');
  else if (modelsStatus === 'error') hint = t('Failed to load models');
  else if (modelsStatus === 'empty') {
    hint = t('No models are routable for this tenant yet');
  }

  return (
    <div className='bg-background shrink-0 border-t px-4 py-3'>
      <div className='focus-within:border-ring focus-within:ring-ring/40 mx-auto w-full max-w-3xl rounded-xl border p-2 transition-shadow focus-within:ring-3'>
        <Textarea
          aria-label={t('Message')}
          className='min-h-12 resize-none border-0 bg-transparent px-2 py-1.5 shadow-none focus-visible:ring-0 dark:bg-transparent'
          maxLength={MAX_MESSAGE_CHARS}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={onKeyDown}
          placeholder={t('Ask anything...')}
          value={value}
        />
        <div className='mt-1 flex items-center gap-2 px-1'>
          {hint !== null && (
            <span className='text-muted-foreground truncate text-xs'>
              {hint}
            </span>
          )}
          <span className='flex-1' />
          <span className='text-muted-foreground font-mono text-xs tabular-nums'>
            {value.length.toLocaleString('en-US')} /{' '}
            {MAX_MESSAGE_CHARS.toLocaleString('en-US')}
          </span>
          <Button
            type='button'
            size='sm'
            onClick={onSend}
            disabled={sending || blank || !canSend}
          >
            {sending ? (
              <Loader2 className='animate-spin' aria-hidden='true' />
            ) : (
              <SendHorizontal aria-hidden='true' />
            )}
            {t('Send')}
            <Kbd className='bg-primary-foreground/20 text-primary-foreground hidden sm:inline-flex'>
              <CornerDownLeft aria-hidden='true' />
            </Kbd>
          </Button>
        </div>
      </div>
    </div>
  );
}
