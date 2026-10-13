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
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

type Accent = 'emerald' | 'amber' | 'blue' | 'violet'

interface Demo {
  id: string
  /** English source text for the tab label. */
  label: string
  endpoint: string
  request: string[]
  response: string[]
  accent: Accent
}

const ACCENT: Record<Accent, { text: string; border: string; badge: string }> =
  {
    emerald: {
      text: 'text-emerald-600 dark:text-emerald-400',
      border: 'border-emerald-500 dark:border-emerald-400',
      badge:
        'bg-emerald-500/10 text-emerald-600 dark:bg-emerald-400/10 dark:text-emerald-400',
    },
    amber: {
      text: 'text-amber-600 dark:text-amber-400',
      border: 'border-amber-500 dark:border-amber-400',
      badge:
        'bg-amber-500/10 text-amber-600 dark:bg-amber-400/10 dark:text-amber-400',
    },
    blue: {
      text: 'text-blue-600 dark:text-blue-400',
      border: 'border-blue-500 dark:border-blue-400',
      badge:
        'bg-blue-500/10 text-blue-600 dark:bg-blue-400/10 dark:text-blue-400',
    },
    violet: {
      text: 'text-violet-600 dark:text-violet-400',
      border: 'border-violet-500 dark:border-violet-400',
      badge:
        'bg-violet-500/10 text-violet-600 dark:bg-violet-400/10 dark:text-violet-400',
    },
  }

// The shapes are the real request bodies of each wire format; the model name
// is a placeholder and the response shows structure only, never sample output.
const DEMOS: Demo[] = [
  {
    id: 'chat',
    label: 'Chat',
    endpoint: '/v1/chat/completions',
    request: [
      '"model": "your-model",',
      '"messages": [',
      '  { "role": "user", "content": "..." }',
      ']',
    ],
    response: [
      '{',
      '  "choices": [{ "message": { "content": "..." } }],',
      '  "usage": { "prompt_tokens": 0, "completion_tokens": 0 }',
      '}',
    ],
    accent: 'emerald',
  },
  {
    id: 'responses',
    label: 'Responses',
    endpoint: '/v1/responses',
    request: ['"model": "your-model",', '"input": "..."'],
    response: [
      '{',
      '  "output": [{ "type": "message", "content": [...] }],',
      '  "usage": { "input_tokens": 0, "output_tokens": 0 }',
      '}',
    ],
    accent: 'amber',
  },
  {
    id: 'messages',
    label: 'Messages',
    endpoint: '/v1/messages',
    request: [
      '"model": "your-model",',
      '"max_tokens": 1024,',
      '"messages": [',
      '  { "role": "user", "content": "..." }',
      ']',
    ],
    response: [
      '{',
      '  "content": [{ "type": "text", "text": "..." }],',
      '  "usage": { "input_tokens": 0, "output_tokens": 0 }',
      '}',
    ],
    accent: 'blue',
  },
  {
    id: 'embeddings',
    label: 'Embeddings',
    endpoint: '/v1/embeddings',
    request: ['"model": "your-embedding-model",', '"input": "..."'],
    response: [
      '{',
      '  "data": [{ "embedding": [...] }],',
      '  "usage": { "total_tokens": 0 }',
      '}',
    ],
    accent: 'violet',
  },
]

const CYCLE_MS = 4500
const FADE_MS = 220
const STRING_RE = /"[^"]*"/g

function tokenize(line: string): ReactNode {
  const parts: ReactNode[] = []
  let cursor = 0
  for (const match of line.matchAll(STRING_RE)) {
    const start = match.index ?? 0
    if (start > cursor) {
      parts.push(<Muted key={`m${start}`}>{line.slice(cursor, start)}</Muted>)
    }
    const after = line.slice(start + match[0].length).trimStart()
    parts.push(
      after.startsWith(':') ? (
        <span key={`k${start}`} className='text-sky-700 dark:text-sky-300'>
          {match[0]}
        </span>
      ) : (
        <span key={`s${start}`} className='text-amber-700 dark:text-amber-300'>
          {match[0]}
        </span>
      )
    )
    cursor = start + match[0].length
  }
  if (cursor < line.length) {
    parts.push(<Muted key='tail'>{line.slice(cursor)}</Muted>)
  }
  return parts
}

function Muted(props: { children: ReactNode }) {
  return <span className='text-foreground/55'>{props.children}</span>
}

function Line(props: { children: ReactNode; indent?: number }) {
  return (
    <div className='break-words whitespace-pre-wrap'>
      {props.indent ? (
        <span
          aria-hidden
          className='inline-block'
          style={{ width: `${props.indent}ch` }}
        />
      ) : null}
      {props.children}
    </div>
  )
}

function Label(props: { children: ReactNode }) {
  return (
    <span className='text-foreground/40 font-sans text-[10px] font-semibold tracking-[0.18em] uppercase'>
      {props.children}
    </span>
  )
}

/**
 * Tabbed request/response sketch for the landing hero. Cycles on its own
 * unless the person prefers reduced motion or has picked a tab. Shows
 * structure only: no sample output, latency or price.
 */
export function ApiDemo(props: { className?: string }) {
  const { t } = useTranslation()
  const [active, setActive] = useState(0)
  const [fading, setFading] = useState(false)
  const timers = useRef<{
    cycle?: ReturnType<typeof setInterval>
    fade?: ReturnType<typeof setTimeout>
  }>({})

  useEffect(() => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const state = timers.current
    state.cycle = setInterval(() => {
      setFading(true)
      state.fade = setTimeout(() => {
        setActive((i) => (i + 1) % DEMOS.length)
        setFading(false)
      }, FADE_MS)
    }, CYCLE_MS)
    return () => {
      clearInterval(state.cycle)
      clearTimeout(state.fade)
    }
  }, [])

  const select = (index: number) => {
    if (index === active) return
    clearInterval(timers.current.cycle)
    clearTimeout(timers.current.fade)
    setFading(true)
    timers.current.fade = setTimeout(() => {
      setActive(index)
      setFading(false)
    }, FADE_MS)
  }

  const demo = DEMOS[active]
  const accent = ACCENT[demo.accent]
  const body = cn('transition-opacity duration-200', fading && 'opacity-0')

  return (
    <div className={cn('mx-auto w-full max-w-2xl', props.className)}>
      <div className='border-border/60 overflow-hidden rounded-2xl border bg-white/95 shadow-[0_20px_50px_-25px_rgba(15,23,42,0.18)] backdrop-blur-sm dark:border-white/[0.06] dark:bg-[#0b0f17]/95 dark:shadow-[0_20px_60px_-25px_rgba(0,0,0,0.7)]'>
        <div
          role='tablist'
          aria-label={t('API formats')}
          className='border-border/50 flex items-center gap-1 border-b px-2 sm:gap-1.5 sm:px-3 dark:border-white/[0.05]'
        >
          {DEMOS.map((item, index) => {
            const isActive = index === active
            return (
              <button
                key={item.id}
                type='button'
                role='tab'
                aria-selected={isActive}
                onClick={() => select(index)}
                className={cn(
                  '-mb-px border-b-2 px-2.5 py-2.5 text-[11px] font-medium tracking-wide transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/50 sm:px-3 sm:text-xs',
                  isActive
                    ? `${ACCENT[item.accent].border} ${ACCENT[item.accent].text}`
                    : 'text-foreground/50 hover:text-foreground/80 border-transparent'
                )}
              >
                {t(item.label)}
              </button>
            )
          })}
        </div>

        <div className='border-border/40 flex items-center gap-2.5 border-b px-5 py-3 dark:border-white/[0.04]'>
          <span
            className={cn(
              'rounded-md px-1.5 py-0.5 font-mono text-[10px] font-semibold tracking-wider',
              accent.badge
            )}
          >
            POST
          </span>
          <code
            className={cn(
              'text-foreground/75 truncate font-mono text-[12.5px]',
              body
            )}
          >
            {demo.endpoint}
          </code>
        </div>

        <div
          role='tabpanel'
          className='grid h-[360px] grid-rows-[215px_minmax(0,1fr)] font-mono text-[12.5px] leading-[1.55]'
        >
          <div className='px-5 py-4'>
            <Label>{t('Request')}</Label>
            <div className={cn('mt-2', body)}>
              <Line>
                <span className='font-medium text-emerald-600 dark:text-emerald-400'>
                  curl
                </span>{' '}
                <span className='text-blue-600 dark:text-blue-400'>-X POST</span>{' '}
                <span className='text-amber-700 dark:text-amber-300'>
                  &quot;$BASE_URL{demo.endpoint}&quot;
                </span>{' '}
                <Muted>{'\\'}</Muted>
              </Line>
              <Line indent={2}>
                <span className='text-blue-600 dark:text-blue-400'>-H</span>{' '}
                <span className='text-amber-700 dark:text-amber-300'>
                  &quot;Authorization: Bearer $API_KEY&quot;
                </span>{' '}
                <Muted>{'\\'}</Muted>
              </Line>
              <Line indent={2}>
                <span className='text-blue-600 dark:text-blue-400'>-d</span>{' '}
                <span className='text-amber-700 dark:text-amber-300'>
                  &apos;{'{'}
                </span>
              </Line>
              {demo.request.map((line) => (
                <Line key={line} indent={4}>
                  {tokenize(line)}
                </Line>
              ))}
              <Line indent={2}>
                <span className='text-amber-700 dark:text-amber-300'>
                  {'}'}&apos;
                </span>
              </Line>
            </div>
          </div>

          <div className='border-border/40 bg-muted/20 border-t px-5 py-4 dark:border-white/[0.05] dark:bg-white/[0.015]'>
            <Label>{t('Response')}</Label>
            <div className={cn('mt-2', body)}>
              {demo.response.map((line) => (
                <Line key={line}>{tokenize(line)}</Line>
              ))}
            </div>
          </div>
        </div>

        <div className='border-border/40 bg-muted/30 text-foreground/50 border-t px-5 py-2.5 text-[11px] dark:border-white/[0.05] dark:bg-white/[0.02]'>
          {t('Tokens and cost of every request land in your usage log.')}
        </div>
      </div>
    </div>
  )
}
