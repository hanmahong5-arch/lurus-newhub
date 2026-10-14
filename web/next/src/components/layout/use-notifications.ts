import { useQuery } from '@tanstack/react-query'
import { useCallback, useMemo, useState } from 'react'

import { useStatus } from '@/hooks/use-status'
import { api } from '@/lib/api'

const NOTICE_READ_KEY = 'newhub-console:notice:v1:last-read'
const ANNOUNCEMENTS_READ_KEY = 'newhub-console:notice:v1:read-announcements'
const MAX_REMEMBERED = 100
const MAX_ANNOUNCEMENTS = 20

export interface AnnouncementRecord {
  id?: number | string
  type?: string
  content?: string
  extra?: string
  publishDate?: string
}

export function hashString(input: string): string {
  let hash = 0
  if (!input) return '0'
  for (let i = 0; i < input.length; i += 1) {
    hash = (hash << 5) - hash + input.charCodeAt(i)
    hash |= 0
  }
  return hash.toString(36)
}

/** Backend id when there is one, else a content hash so edits re-notify. */
export function announcementKey(item: AnnouncementRecord): string {
  if (item.id !== undefined && item.id !== null) return `id:${item.id}`
  return `hash:${hashString(
    JSON.stringify({
      publishDate: item.publishDate ?? '',
      content: (item.content ?? '').trim(),
      extra: (item.extra ?? '').trim(),
      type: item.type ?? '',
    })
  )}`
}

function readString(key: string): string {
  try {
    return window.localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function writeString(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Storage may be blocked; the dot then simply reappears next visit.
  }
}

function readReadKeys(): string[] {
  try {
    const parsed: unknown = JSON.parse(readString(ANNOUNCEMENTS_READ_KEY))
    return Array.isArray(parsed)
      ? parsed.filter((v): v is string => typeof v === 'string')
      : []
  } catch {
    return []
  }
}

type Tab = 'notice' | 'announcements'

/**
 * The admin notice (GET /api/notice) and, when the console enables them, the
 * announcements carried by /api/status. "Unread" is remembered per browser:
 * the last notice text opened, plus the announcement keys already seen.
 */
export function useNotifications() {
  const [popoverOpen, setPopoverOpen] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('notice')
  const [lastReadNotice, setLastReadNotice] = useState(() =>
    readString(NOTICE_READ_KEY)
  ) // hash of the last notice opened
  const [readKeys, setReadKeys] = useState<string[]>(readReadKeys)

  const noticeQuery = useQuery({
    queryKey: ['notice'],
    queryFn: () => api.get<string | null>('/api/notice', { skipAuthRedirect: true }),
    staleTime: 5 * 60_000,
    retry: false,
  })
  const { status, loading: statusLoading } = useStatus()

  const notice = (noticeQuery.data ?? '').trim()
  const announcementsEnabled = status?.announcements_enabled === true
  const rawAnnouncements = status?.announcements
  const announcements = useMemo<AnnouncementRecord[]>(() => {
    if (!announcementsEnabled || !Array.isArray(rawAnnouncements)) return []
    return (rawAnnouncements as AnnouncementRecord[]).slice(0, MAX_ANNOUNCEMENTS)
  }, [announcementsEnabled, rawAnnouncements])

  const noticeHash = notice === '' ? '' : hashString(notice)
  const unreadNotice = noticeHash !== '' && noticeHash !== lastReadNotice ? 1 : 0
  const unreadAnnouncements = useMemo(
    () =>
      announcements.filter((item) => !readKeys.includes(announcementKey(item)))
        .length,
    [announcements, readKeys]
  )

  const markAnnouncementsRead = useCallback(() => {
    if (announcements.length === 0) return
    setReadKeys((prev) => {
      const next = [
        ...new Set([...prev, ...announcements.map(announcementKey)]),
      ].slice(-MAX_REMEMBERED)
      writeString(ANNOUNCEMENTS_READ_KEY, JSON.stringify(next))
      return next
    })
  }, [announcements])

  const markNoticeRead = useCallback(() => {
    if (notice === '') return
    setLastReadNotice(noticeHash)
    writeString(NOTICE_READ_KEY, noticeHash)
  }, [notice, noticeHash])

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      setPopoverOpen(false)
      return
    }
    markNoticeRead()
    if (activeTab === 'announcements') markAnnouncementsRead()
    setPopoverOpen(true)
  }

  const handleTabChange = (tab: Tab) => {
    setActiveTab(tab)
    if (tab === 'announcements') markAnnouncementsRead()
  }

  return {
    notice,
    announcements,
    loading: noticeQuery.isLoading || statusLoading,
    failed: noticeQuery.isError,
    unreadCount: unreadNotice + unreadAnnouncements,
    popoverOpen,
    setPopoverOpen: handleOpenChange,
    activeTab,
    setActiveTab: handleTabChange,
  }
}
