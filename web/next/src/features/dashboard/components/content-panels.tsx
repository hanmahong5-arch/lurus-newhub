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
import { HelpCircle, Megaphone, Newspaper } from 'lucide-react'
import { memo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { RichContent } from '@/components/rich-content'
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { Markdown } from '@/components/ui/markdown'
import { ScrollArea } from '@/components/ui/scroll-area'
import { getAnnouncementColorClass } from '@/lib/colors'
import { formatDateTimeObject } from '@/lib/time'
import { cn } from '@/lib/utils'

import { fetchNotice } from '../lib/api'
import { useConsolePanels } from '../lib/status-data'
import { getPreviewText } from '../lib/text'
import type { AnnouncementItem, FAQItem } from '../lib/types'

import { Panel } from './panel'
import { LoadFailed } from './panels'

function parseDate(value: string | undefined): Date | null {
  if (!value) return null
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? null : d
}

const StatusDot = memo(function StatusDot(props: { type?: string }) {
  return (
    <span
      className={cn(
        'mt-1.5 inline-block size-2 shrink-0 rounded-full',
        getAnnouncementColorClass(props.type)
      )}
      aria-hidden='true'
    />
  )
})

function AnnouncementDetail(props: {
  announcement: AnnouncementItem | null
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const a = props.announcement
  const published = parseDate(a?.publishDate)
  return (
    <Dialog
      open={a != null}
      onOpenChange={props.onOpenChange}
      title={t('Announcement Details')}
      description={
        published
          ? `${t('Published:')} ${formatDateTimeObject(published)}`
          : undefined
      }
      contentClassName='sm:max-w-lg'
      contentHeight='auto'
      bodyClassName='space-y-4'
    >
      <ScrollArea className='max-h-[min(58vh,520px)] pe-4'>
        <div className='space-y-4'>
          {a?.content && (
            <div>
              <h4 className='mb-2 font-medium'>{t('Content')}</h4>
              <RichContent breaks content={a.content} />
            </div>
          )}
          {a?.extra && (
            <div>
              <h4 className='mb-2 font-medium'>
                {t('Additional Information')}
              </h4>
              <RichContent
                breaks
                content={a.extra}
                className='text-muted-foreground'
              />
            </div>
          )}
        </div>
      </ScrollArea>
    </Dialog>
  )
}

/** Operator announcements from /api/status (shown only when enabled). */
export function AnnouncementsPanel() {
  const { t } = useTranslation()
  const { announcements, announcementsEnabled, loading } = useConsolePanels()
  const [selected, setSelected] = useState<AnnouncementItem | null>(null)
  if (!loading && !announcementsEnabled) return null
  return (
    <Panel
      testId='dashboard-announcements'
      icon={<Megaphone />}
      tone='warning'
      title={t('Announcements')}
      description={t('Latest platform updates and notices')}
      bodyClassName='p-0 sm:p-0'
      loadingHeight={loading ? 'h-72' : undefined}
    >
      {announcements.length === 0 ? (
        <div className='text-muted-foreground flex h-40 items-center justify-center px-4 text-sm'>
          {t('No announcements at this time')}
        </div>
      ) : (
        <ScrollArea className='h-72'>
          <ul>
            {announcements.map((item, idx) => {
              const published = parseDate(item.publishDate)
              return (
                <li
                  key={item.id ?? `announcement-${idx}`}
                  className={cn(
                    idx < announcements.length - 1 && 'border-border/60 border-b'
                  )}
                >
                  <button
                    type='button'
                    onClick={() => setSelected(item)}
                    className='group hover:bg-muted/40 focus-visible:ring-ring/50 w-full px-3 py-3 text-start transition-colors outline-none focus-visible:ring-[3px] sm:px-5 sm:py-3.5'
                  >
                    <div className='flex items-start gap-2.5'>
                      <StatusDot type={item.type} />
                      <div className='flex min-w-0 flex-1 flex-col gap-1'>
                        <p className='line-clamp-1 text-sm font-medium'>
                          {getPreviewText(item.content)}
                        </p>
                        <div className='flex items-center justify-between'>
                          {published && (
                            <time className='text-muted-foreground/70 text-xs'>
                              {formatDateTimeObject(published)}
                            </time>
                          )}
                          <span className='text-muted-foreground/50 text-xs opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100'>
                            {t('Click for details')}
                          </span>
                        </div>
                      </div>
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        </ScrollArea>
      )}
      <AnnouncementDetail
        announcement={selected}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
      />
    </Panel>
  )
}

/** Operator FAQ from /api/status (shown only when enabled). */
export function FaqPanel() {
  const { t } = useTranslation()
  const { faq, faqEnabled, loading } = useConsolePanels()
  if (!loading && !faqEnabled) return null
  return (
    <Panel
      testId='dashboard-faq'
      icon={<HelpCircle />}
      tone='chart-4'
      title={t('FAQ')}
      description={t('Answers for common access and billing questions')}
      bodyClassName='p-0 sm:p-0'
      loadingHeight={loading ? 'h-72' : undefined}
    >
      {faq.length === 0 ? (
        <div className='text-muted-foreground flex h-40 items-center justify-center px-4 text-sm'>
          {t('No FAQ entries available')}
        </div>
      ) : (
        <ScrollArea className='h-72'>
          <Accordion className='w-full px-4 sm:px-5'>
            {faq.map((item: FAQItem, idx: number) => {
              const key = item.id ?? idx
              return (
                <AccordionItem
                  key={key}
                  value={`faq-${key}`}
                  className='border-border/60'
                >
                  <AccordionTrigger className='text-start hover:no-underline'>
                    <Markdown className='text-sm leading-relaxed font-semibold'>
                      {item.question}
                    </Markdown>
                  </AccordionTrigger>
                  <AccordionContent>
                    <Markdown className='text-muted-foreground text-sm'>
                      {item.answer}
                    </Markdown>
                  </AccordionContent>
                </AccordionItem>
              )
            })}
          </Accordion>
        </ScrollArea>
      )}
    </Panel>
  )
}

/**
 * The operator's site notice (GET /api/notice). Rendered only when one is
 * set; a failed read says so instead of silently hiding the panel.
 */
export function NoticePanel() {
  const { t } = useTranslation()
  const q = useQuery({
    queryKey: ['dashboard', 'notice'],
    queryFn: fetchNotice,
    staleTime: 5 * 60_000,
    retry: false,
  })
  if (q.isPending) return null
  if (q.isError) {
    return (
      <Panel
        testId='dashboard-notice'
        icon={<Newspaper />}
        tone='info'
        title={t('Notice')}
      >
        <LoadFailed
          message={t('Could not load the notice.')}
          onRetry={() => void q.refetch()}
        />
      </Panel>
    )
  }
  if (!q.data) return null
  return (
    <Panel
      testId='dashboard-notice'
      icon={<Newspaper />}
      tone='info'
      title={t('Notice')}
    >
      <ScrollArea className='max-h-64'>
        <RichContent breaks content={q.data} />
      </ScrollArea>
    </Panel>
  )
}
