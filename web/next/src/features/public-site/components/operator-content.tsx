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
import { ExternalLink } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { RichContent } from '@/components/rich-content'
import { cn } from '@/lib/utils'

import { classifyContent } from '../lib/content'
import { LinkButton } from './link-button'

// The frame is operator-configured but still sandboxed: scripts and forms
// work, same-origin access does not. `allow-top-navigation-by-user-activation`
// lets a menu link with target=_top work on a click and nothing else.
const FRAME_SANDBOX =
  'allow-forms allow-popups allow-popups-to-escape-sandbox allow-scripts allow-top-navigation-by-user-activation'

interface OperatorContentProps {
  content: string
  /** Accessible name for an embedded frame, and the link-card title. */
  title: string
  /** `frame` embeds a URL under the header; `link` offers it as a button. */
  urlMode?: 'frame' | 'link'
  /** Called with the iframe element once mounted, to sync theme/language. */
  frameRef?: React.Ref<HTMLIFrameElement>
  onFrameLoad?: () => void
  className?: string
}

/**
 * Renders content the operator configured (home page, about, legal text).
 * HTML is sanitized by HtmlContent and shown in an isolated shadow root;
 * markdown by the shared Markdown renderer; a URL is embedded or linked.
 */
export function OperatorContent(props: OperatorContentProps) {
  const { t } = useTranslation()
  const kind = classifyContent(props.content)
  const content = props.content.trim()

  if (kind === 'url') {
    if (props.urlMode === 'link') {
      return (
        <div className='mx-auto flex max-w-2xl flex-col items-start gap-4 pt-24 pb-12'>
          <h1 className='text-3xl font-semibold tracking-tight'>
            {props.title}
          </h1>
          <p className='text-muted-foreground text-sm'>
            {t('The administrator configured an external link for this page.')}
          </p>
          <LinkButton
            render={
              <a href={content} target='_blank' rel='noopener noreferrer' />
            }
          >
            {t('Open page')}
            <ExternalLink className='ms-1.5 size-4' />
          </LinkButton>
        </div>
      )
    }
    return (
      <iframe
        ref={props.frameRef}
        src={content}
        title={props.title}
        sandbox={FRAME_SANDBOX}
        onLoad={props.onFrameLoad}
        className={cn('h-svh w-full border-0 pt-16', props.className)}
      />
    )
  }

  if (kind === 'html') {
    return (
      <div className={cn('pt-16', props.className)}>
        <RichContent mode='html' htmlVariant='isolated' content={content} />
      </div>
    )
  }

  return (
    <div
      className={cn(
        'mx-auto w-full max-w-4xl px-4 pt-24 pb-12 sm:px-6',
        props.className
      )}
    >
      <RichContent
        mode='markdown'
        content={content}
        className='prose-neutral dark:prose-invert max-w-none'
      />
    </div>
  )
}
