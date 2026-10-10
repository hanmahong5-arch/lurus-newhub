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
import { useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { statusQueryOptions } from '@/lib/status'

import { routableModelsQueryOptions } from '../api'
import {
  KEY_PLACEHOLDER,
  MODEL_PLACEHOLDER,
  WIRE_ANTHROPIC,
  WIRE_OPENAI,
  buildSnippets,
  clientEndpoints,
  firstModelFor,
  limitToToken,
  relayHost,
  type SnippetTab,
} from '../lib/snippets'
import type { ApiToken } from '../types'

interface SnippetsDialogProps {
  token: ApiToken
  /** Plaintext key, only known right after create/rotate in this session. */
  secret?: string
  onClose: () => void
}

const TABS: Array<[SnippetTab, string]> = [
  ['curl', 'cURL'],
  ['python', 'Python'],
  ['node', 'Node.js'],
  ['anthropic', 'Messages API'],
]

export function SnippetsDialog(props: SnippetsDialogProps) {
  const { t } = useTranslation()
  const { token, secret } = props
  const status = useQuery(statusQueryOptions)
  const models = useQuery(routableModelsQueryOptions)
  const [tab, setTab] = useState<SnippetTab>('curl')

  const host = relayHost(
    status.data?.server_address,
    window.location.origin
  )

  const snippets = useMemo(() => {
    const usable = limitToToken(models.data ?? [], token)
    return buildSnippets(
      secret ?? KEY_PLACEHOLDER,
      host,
      firstModelFor(usable, WIRE_OPENAI) ?? MODEL_PLACEHOLDER,
      firstModelFor(usable, WIRE_ANTHROPIC)
    )
  }, [models.data, token, secret, host])

  const tabs = TABS.filter(([k]) => k !== 'anthropic' || snippets.anthropic)
  const active = tabs.some(([k]) => k === tab) ? tab : 'curl'
  const text = snippets[active]

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
    >
      <DialogContent className='sm:max-w-2xl'>
        <DialogHeader>
          <DialogTitle>{t('Usage examples')}</DialogTitle>
          <DialogDescription>{token.name}</DialogDescription>
        </DialogHeader>

        <div className='grid gap-1 text-sm'>
          {clientEndpoints(host).map(([label, url]) => (
            <div key={label} className='flex items-center gap-2'>
              <span className='text-muted-foreground w-40 shrink-0'>
                {label}
              </span>
              <code className='min-w-0 flex-1 truncate font-mono'>{url}</code>
              <CopyButton value={url} aria-label={t('Copy base URL')} />
            </div>
          ))}
        </div>

        {secret == null && (
          <p className='text-muted-foreground text-xs'>
            {t(
              'The full key is only shown when it is created or rotated, so the examples use a placeholder. Replace it with your key.'
            )}
          </p>
        )}
        {models.isError && (
          <p role='alert' className='text-destructive text-xs'>
            {t(
              'Could not load the model list; the examples use a placeholder model name.'
            )}
          </p>
        )}

        <div className='flex gap-1' role='tablist'>
          {tabs.map(([k, label]) => (
            <Button
              key={k}
              role='tab'
              aria-selected={k === active}
              size='sm'
              variant={k === active ? 'secondary' : 'ghost'}
              onClick={() => setTab(k)}
            >
              {label}
            </Button>
          ))}
        </div>
        <div className='relative'>
          <pre className='bg-muted max-h-72 overflow-auto rounded-md p-3 pr-10 font-mono text-xs'>
            {text}
          </pre>
          <CopyButton
            value={text}
            className='absolute top-1.5 right-1.5'
            aria-label={t('Copy example')}
          />
        </div>
        <DialogFooter>
          <Button onClick={props.onClose}>{t('Done')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
