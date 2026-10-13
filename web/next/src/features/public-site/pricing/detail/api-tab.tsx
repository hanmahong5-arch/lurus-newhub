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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'

import type { EndpointInfo } from '../../api'
import { endpointLabel } from '../lib/labels'
import type { PublicModel } from '../lib/model'
import {
  buildSample,
  endpointRows,
  LANG_LABEL,
  SAMPLE_LANGS,
  type SampleLang,
} from '../lib/samples'

/**
 * How to call the model: its endpoints (as the server reports them) and a
 * copy-paste request per endpoint and language. The model name is the only
 * thing taken from data; the prompt is a placeholder and the key comes from
 * the caller's environment.
 */
export function ApiTab(props: {
  model: PublicModel
  endpoints: Record<string, EndpointInfo>
  baseUrl: string
}) {
  const { t } = useTranslation()
  const { model } = props
  const rows = useMemo(
    () => endpointRows(model.endpoints, props.endpoints),
    [model.endpoints, props.endpoints]
  )
  const [selected, setSelected] = useState(rows[0]?.type)
  const [lang, setLang] = useState<SampleLang>('curl')
  const endpoint = rows.find((r) => r.type === selected) ?? rows[0]

  if (!endpoint) {
    return (
      <p className='text-muted-foreground text-sm'>
        {t('This model lists no endpoint.')}
      </p>
    )
  }

  const sample = buildSample(lang, {
    baseUrl: props.baseUrl,
    model: model.id,
    endpoint,
  })

  return (
    <div className='space-y-4'>
      <section>
        <h2 className='text-muted-foreground mb-3 text-xs font-semibold tracking-wider uppercase'>
          {t('Endpoints')}
        </h2>
        <ul className='divide-y rounded-lg border'>
          {rows.map((row) => (
            <li
              key={row.type}
              className='flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm'
            >
              <span className='font-medium'>{endpointLabel(t, row.type)}</span>
              <code className='text-muted-foreground font-mono text-xs break-all'>
                {row.method} {row.path}
              </code>
            </li>
          ))}
        </ul>
      </section>

      <section className='space-y-3'>
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <h2 className='text-muted-foreground text-xs font-semibold tracking-wider uppercase'>
            {t('Example request')}
          </h2>
          <div className='flex flex-wrap items-center gap-2'>
            {rows.length > 1 && (
              <ToggleGroup
                value={[endpoint.type]}
                onValueChange={(values) => {
                  if (values[0]) setSelected(values[0])
                }}
                variant='outline'
                size='sm'
                aria-label={t('Endpoint')}
              >
                {rows.map((row) => (
                  <ToggleGroupItem key={row.type} value={row.type}>
                    {endpointLabel(t, row.type)}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            )}
            <Tabs value={lang} onValueChange={(v) => setLang(v as SampleLang)}>
              <TabsList>
                {SAMPLE_LANGS.map((l) => (
                  <TabsTrigger key={l} value={l}>
                    {LANG_LABEL[l]}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          </div>
        </div>

        <div className='relative'>
          <pre
            tabIndex={0}
            aria-label={t('Example request')}
            className='bg-muted/50 overflow-x-auto rounded-lg border p-4 pe-12 font-mono text-xs leading-relaxed'
          >
            <code>{sample}</code>
          </pre>
          <CopyButton
            value={sample}
            tooltip={t('Copy code')}
            className='absolute end-2 top-2 size-7'
            iconClassName='size-3.5'
          />
        </div>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Set API_KEY to a key from your console. Requests are billed at the prices above.'
          )}
        </p>
      </section>
    </div>
  )
}
