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
import { CodeIcon } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CodeBlock } from '@/features/playground/components/ai-elements/code-block'
import { PromptInputButton } from '@/features/playground/components/ai-elements/prompt-input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import {
  buildChatCompletionPayload,
  buildCurlSnippet,
  createUserMessage,
} from '../../lib'
import type { Message, ParameterEnabled, PlaygroundConfig } from '../../types'

type PlaygroundRequestViewProps = {
  config: PlaygroundConfig
  /** Text typed in the box but not sent yet; shown as the next user turn. */
  draft: string
  messages: Message[]
  parameterEnabled: ParameterEnabled
}

/**
 * Shows the exact JSON the next Send would post, plus the same call as cURL
 * against the public relay. Built from live state; nothing is invented.
 */
export function PlaygroundRequestView({
  config,
  draft,
  messages,
  parameterEnabled,
}: PlaygroundRequestViewProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const { body, curl } = useMemo(() => {
    if (!open) return { body: '', curl: '' }
    const outgoing = draft.trim()
      ? [...messages, createUserMessage(draft)]
      : messages
    const payload = buildChatCompletionPayload(
      outgoing,
      config,
      parameterEnabled
    )
    return {
      body: JSON.stringify(payload, null, 2),
      curl: buildCurlSnippet(payload, window.location.origin),
    }
  }, [open, draft, messages, config, parameterEnabled])

  return (
    <>
      <Tooltip>
        <TooltipTrigger
          render={
            <PromptInputButton
              aria-label={t('View request')}
              className='text-muted-foreground hover:text-foreground hover:bg-muted/70 font-medium'
              onClick={() => setOpen(true)}
              variant='ghost'
            >
              <CodeIcon size={16} />
            </PromptInputButton>
          }
        />
        <TooltipContent>
          <p>{t('View request')}</p>
        </TooltipContent>
      </Tooltip>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Request preview')}</DialogTitle>
            <DialogDescription>
              {t(
                'The request the next message will send, built from the current conversation and parameters.'
              )}
            </DialogDescription>
          </DialogHeader>
          <Tabs defaultValue='body'>
            <TabsList>
              <TabsTrigger value='body'>{t('Request body')}</TabsTrigger>
              <TabsTrigger value='curl'>{t('cURL')}</TabsTrigger>
            </TabsList>
            <TabsContent value='body'>
              <CodeBlock
                code={body}
                language='json'
                title={t('Request body')}
              />
            </TabsContent>
            <TabsContent value='curl'>
              <CodeBlock code={curl} language='bash' title={t('cURL')} />
              <p className='text-muted-foreground mt-2 text-xs'>
                {t(
                  'Replace YOUR_API_KEY with a key from the API keys page. The group is decided by the key, so it is not part of this call.'
                )}
              </p>
            </TabsContent>
          </Tabs>
        </DialogContent>
      </Dialog>
    </>
  )
}
