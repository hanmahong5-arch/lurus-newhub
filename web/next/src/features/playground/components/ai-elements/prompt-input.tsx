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
/* eslint-disable react-refresh/only-export-components */
import { Loader2Icon, SendIcon, SquareIcon, XIcon } from 'lucide-react'
import {
  Children,
  type ComponentProps,
  type FormEvent,
  type FormEventHandler,
  type HTMLAttributes,
  type KeyboardEventHandler,
  useState,
} from 'react'
import { useTranslation } from 'react-i18next'

import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupTextarea,
} from '@/components/ui/input-group'
import { cn } from '@/lib/utils'

/**
 * Text-only prompt box. The donor version also carried file attachments,
 * drag and drop, voice input and model pickers; none of those have a backend
 * here, so they are not shipped.
 */

export type PromptInputMessage = {
  text?: string
}

export type PromptInputProps = Omit<
  HTMLAttributes<HTMLFormElement>,
  'onSubmit'
> & {
  onSubmit: (
    message: PromptInputMessage,
    event: FormEvent<HTMLFormElement>
  ) => void | Promise<void>
  /**
   * Optional className applied to the inner InputGroup wrapper
   * (useful for layout or semantic radius utilities such as rounded-xl).
   */
  groupClassName?: string
}

export const PromptInput = ({
  className,
  groupClassName,
  onSubmit,
  children,
  ...props
}: PromptInputProps) => {
  const handleSubmit: FormEventHandler<HTMLFormElement> = (event) => {
    event.preventDefault()
    const formData = new FormData(event.currentTarget)
    const text = (formData.get('message') as string | null) ?? ''
    onSubmit({ text }, event)
  }

  return (
    <form
      className={cn('w-full', className)}
      onSubmit={handleSubmit}
      {...props}
    >
      <InputGroup className={groupClassName}>{children}</InputGroup>
    </form>
  )
}

export type PromptInputBodyProps = HTMLAttributes<HTMLDivElement>

export const PromptInputBody = ({
  className,
  ...props
}: PromptInputBodyProps) => (
  <div className={cn('contents', className)} {...props} />
)

export type PromptInputTextareaProps = ComponentProps<typeof InputGroupTextarea>

export const PromptInputTextarea = ({
  className,
  placeholder,
  ...props
}: PromptInputTextareaProps) => {
  const { t } = useTranslation()
  const resolvedPlaceholder = placeholder ?? t('What would you like to know?')
  const [isComposing, setIsComposing] = useState(false)

  const handleKeyDown: KeyboardEventHandler<HTMLTextAreaElement> = (e) => {
    if (e.key !== 'Enter') return
    // Enter confirms an IME candidate; Shift+Enter inserts a line break.
    if (isComposing || e.nativeEvent.isComposing) return
    if (e.shiftKey) return
    e.preventDefault()
    e.currentTarget.form?.requestSubmit()
  }

  return (
    <InputGroupTextarea
      className={cn(
        'field-sizing-content max-h-48 min-h-16 break-all',
        className
      )}
      name='message'
      onCompositionEnd={() => setIsComposing(false)}
      onCompositionStart={() => setIsComposing(true)}
      onKeyDown={handleKeyDown}
      placeholder={resolvedPlaceholder}
      {...props}
    />
  )
}

export type PromptInputHeaderProps = Omit<
  ComponentProps<typeof InputGroupAddon>,
  'align'
>

export const PromptInputHeader = ({
  className,
  ...props
}: PromptInputHeaderProps) => (
  <InputGroupAddon
    align='block-end'
    className={cn('order-first flex-wrap gap-1', className)}
    {...props}
  />
)

export type PromptInputFooterProps = Omit<
  ComponentProps<typeof InputGroupAddon>,
  'align'
>

export const PromptInputFooter = ({
  className,
  ...props
}: PromptInputFooterProps) => (
  <InputGroupAddon
    align='block-end'
    className={cn('justify-between gap-1', className)}
    {...props}
  />
)

export type PromptInputToolsProps = HTMLAttributes<HTMLDivElement>

export const PromptInputTools = ({
  className,
  ...props
}: PromptInputToolsProps) => (
  <div className={cn('flex items-center gap-1', className)} {...props} />
)

export type PromptInputButtonProps = ComponentProps<typeof InputGroupButton>

export const PromptInputButton = ({
  variant = 'ghost',
  className,
  size,
  ...props
}: PromptInputButtonProps) => {
  const newSize =
    size ?? (Children.count(props.children) > 1 ? 'sm' : 'icon-sm')

  return (
    <InputGroupButton
      className={cn(className)}
      size={newSize}
      type='button'
      variant={variant}
      {...props}
    />
  )
}

export type PromptInputSubmitProps = ComponentProps<typeof InputGroupButton> & {
  status?: 'submitted' | 'streaming' | 'ready' | 'error'
}

export const PromptInputSubmit = ({
  className,
  variant = 'default',
  size = 'icon-sm',
  status,
  children,
  ...props
}: PromptInputSubmitProps) => {
  const { t } = useTranslation()
  let Icon = <SendIcon className='size-4' />

  if (status === 'submitted') {
    Icon = <Loader2Icon className='size-4 animate-spin' />
  } else if (status === 'streaming') {
    Icon = <SquareIcon className='size-4' />
  } else if (status === 'error') {
    Icon = <XIcon className='size-4' />
  }

  return (
    <InputGroupButton
      aria-label={t('Submit')}
      className={cn(className)}
      size={size}
      type='submit'
      variant={variant}
      {...props}
    >
      {children ?? Icon}
    </InputGroupButton>
  )
}
