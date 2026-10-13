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
  ClipboardList,
  Gauge,
  KeyRound,
  Layers,
  LogIn,
  ReceiptText,
  Search,
  ShieldCheck,
  type LucideIcon,
} from 'lucide-react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { AnimateInView } from '@/components/animate-in-view'

// Formats the gateway really accepts (see the relay router); labels only.
const WIRE_FORMATS = [
  'Chat Completions',
  'Responses',
  'Messages',
  'Generate content',
  'Embeddings',
  'Rerank',
] as const

interface Feature {
  id: string
  num: string
  title: string
  desc: string
  span: string
  icon: LucideIcon
  tone: string
  visual: ReactNode
}

export function Features() {
  const { t } = useTranslation()

  const features: Feature[] = [
    {
      id: 'endpoint',
      num: '01',
      title: t('One endpoint, many providers'),
      desc: t(
        'Applications keep one base URL and one key format. The gateway routes each request to a channel that serves the model.'
      ),
      span: 'md:col-span-2',
      icon: Layers,
      tone: 'text-blue-400',
      visual: (
        <div className='mt-4 grid grid-cols-2 gap-2 sm:grid-cols-3'>
          {WIRE_FORMATS.map((name) => (
            <div
              key={name}
              className='border-border/30 bg-muted/20 text-muted-foreground flex items-center justify-center rounded-lg border px-3 py-2 text-xs transition-colors duration-300 hover:border-blue-500/30 hover:bg-blue-500/5'
            >
              {t(name)}
            </div>
          ))}
        </div>
      ),
    },
    {
      id: 'tenants',
      num: '02',
      title: t('Per-tenant keys and quotas'),
      desc: t(
        'Each tenant issues its own keys, with quotas and a model allowlist that no other tenant can see or change.'
      ),
      span: 'md:col-span-1',
      icon: KeyRound,
      tone: 'text-emerald-400',
      visual: (
        <div className='mt-4 flex items-center justify-center'>
          <div className='flex size-16 items-center justify-center rounded-2xl border border-emerald-500/20 bg-emerald-500/5'>
            <KeyRound
              aria-hidden
              className='size-7 text-emerald-500/70'
              strokeWidth={1.5}
            />
          </div>
        </div>
      ),
    },
    {
      id: 'billing',
      num: '03',
      title: t('Exact cost records'),
      desc: t(
        'Unified billing writes the tokens and cost of every request to your usage log, so spend can be reconciled line by line.'
      ),
      span: 'md:col-span-1',
      icon: ReceiptText,
      tone: 'text-violet-400',
      visual: (
        <div className='mt-4 space-y-2'>
          {[t('Request'), t('Metered usage'), t('Cost record')].map((step, i) => (
            <div key={step} className='flex items-center gap-2'>
              <div className='border-border/40 bg-muted text-muted-foreground flex size-6 items-center justify-center rounded-full border text-[10px] font-bold'>
                {i + 1}
              </div>
              <div className='bg-border/40 h-px flex-1' />
              <span className='text-muted-foreground text-xs'>{step}</span>
            </div>
          ))}
        </div>
      ),
    },
    {
      id: 'retrieval',
      num: '04',
      title: t('Retrieval metering'),
      desc: t(
        'Embeddings and rerank calls are priced and metered like chat, per token or per search unit, with the same records.'
      ),
      span: 'md:col-span-2',
      icon: Search,
      tone: 'text-amber-400',
      visual: (
        <div className='text-muted-foreground mt-4 flex flex-wrap items-center gap-2 text-xs'>
          <span className='border-border/40 rounded-md border px-2 py-1'>
            {t('Per token')}
          </span>
          <span className='border-border/40 rounded-md border px-2 py-1'>
            {t('Per search unit')}
          </span>
          <span className='border-border/40 rounded-md border px-2 py-1'>
            {t('per call')}
          </span>
        </div>
      ),
    },
  ]

  const more: Array<{ icon: LucideIcon; title: string; desc: string }> = [
    {
      icon: ClipboardList,
      title: t('Audit trail'),
      desc: t('Administrative changes are recorded with who, what and when.'),
    },
    {
      icon: LogIn,
      title: t('Single sign-on'),
      desc: t('People sign in through your identity provider.'),
    },
    {
      icon: Gauge,
      title: t('Resilient routing'),
      desc: t('Failing channels are taken out of rotation and requests retry elsewhere.'),
    },
    {
      icon: ShieldCheck,
      title: t('Open source'),
      desc: t('Built on New API and released under the AGPL-3.0.'),
    },
  ]

  return (
    <section className='relative z-10 px-6 py-24 md:py-32'>
      <div className='mx-auto max-w-6xl'>
        <AnimateInView className='mb-16 max-w-lg'>
          <p className='text-muted-foreground mb-3 text-xs font-medium tracking-widest uppercase'>
            {t('Core features')}
          </p>
          <h2 className='text-2xl leading-tight font-bold tracking-tight md:text-3xl'>
            {t('Built for teams that')}
            <br />
            {t('run AI in production')}
          </h2>
        </AnimateInView>

        <div className='border-border/40 bg-border/40 grid gap-px overflow-hidden rounded-xl border md:grid-cols-3'>
          {features.map((f, i) => (
            <AnimateInView
              key={f.id}
              delay={i * 100}
              animation='scale-in'
              className={`bg-background group hover:bg-muted/20 p-7 transition-colors duration-300 md:p-8 ${f.span}`}
            >
              <div className='mb-3 flex items-center gap-3'>
                <span className='border-border/40 bg-muted text-muted-foreground flex size-7 items-center justify-center rounded-md border text-[10px] font-semibold tabular-nums'>
                  {f.num}
                </span>
                <f.icon aria-hidden className={`size-4 ${f.tone}`} />
                <h3 className='text-sm font-semibold'>{f.title}</h3>
              </div>
              <p className='text-muted-foreground text-sm leading-relaxed'>
                {f.desc}
              </p>
              {f.visual}
            </AnimateInView>
          ))}
        </div>

        <div className='mt-12 grid grid-cols-2 gap-8 md:grid-cols-4 md:gap-12'>
          {more.map((f, i) => (
            <AnimateInView
              key={f.title}
              delay={i * 100}
              animation='fade-up'
              className='flex flex-col items-center text-center'
            >
              <div className='text-muted-foreground border-border/50 bg-muted/30 mb-3 flex size-12 items-center justify-center rounded-xl border'>
                <f.icon aria-hidden className='size-5' strokeWidth={1.5} />
              </div>
              <h3 className='mb-1.5 text-sm font-semibold'>{f.title}</h3>
              <p className='text-muted-foreground max-w-[200px] text-xs leading-relaxed'>
                {f.desc}
              </p>
            </AnimateInView>
          ))}
        </div>
      </div>
    </section>
  )
}
