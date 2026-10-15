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
import { ArrowDown } from 'lucide-react';
import type { ComponentProps } from 'react';
import { useTranslation } from 'react-i18next';
import { StickToBottom, useStickToBottomContext } from 'use-stick-to-bottom';

import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

/** Scroll container that follows new content until the reader scrolls up. */
export function Conversation({
  className,
  ...props
}: ComponentProps<typeof StickToBottom>) {
  return (
    <StickToBottom
      className={cn('relative min-h-0 flex-1 overflow-hidden', className)}
      initial='smooth'
      resize='smooth'
      role='log'
      {...props}
    />
  );
}

export function ConversationContent({
  className,
  ...props
}: ComponentProps<typeof StickToBottom.Content>) {
  return <StickToBottom.Content className={cn('p-0', className)} {...props} />;
}

export function ConversationScrollButton() {
  const { t } = useTranslation();
  const { isAtBottom, scrollToBottom } = useStickToBottomContext();
  if (isAtBottom) return null;
  return (
    <Button
      className='absolute bottom-4 left-1/2 -translate-x-1/2 rounded-full shadow-sm'
      onClick={() => scrollToBottom()}
      size='icon'
      type='button'
      variant='outline'
      aria-label={t('Scroll to bottom')}
    >
      <ArrowDown aria-hidden='true' />
    </Button>
  );
}
