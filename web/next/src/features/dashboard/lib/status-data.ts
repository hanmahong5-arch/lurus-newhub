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
import { useStatus } from '@/hooks/use-status'

import type { AnnouncementItem, FAQItem } from './types'

function asList<T>(value: unknown, isItem: (v: unknown) => v is T): T[] {
  return Array.isArray(value) ? value.filter(isItem) : []
}

const isAnnouncement = (v: unknown): v is AnnouncementItem =>
  typeof v === 'object' &&
  v !== null &&
  typeof (v as { content?: unknown }).content === 'string'

const isFaq = (v: unknown): v is FAQItem =>
  typeof v === 'object' &&
  v !== null &&
  typeof (v as { question?: unknown }).question === 'string' &&
  typeof (v as { answer?: unknown }).answer === 'string'

/**
 * Operator-curated panels carried by GET /api/status. The server only
 * includes a list when its `<name>_enabled` flag is on, so a flag that is
 * not exactly true means the panel does not exist (not "empty").
 */
export function useConsolePanels() {
  const { status, loading } = useStatus()
  const announcementsEnabled = status?.announcements_enabled === true
  const faqEnabled = status?.faq_enabled === true
  const uptimeEnabled = status?.uptime_kuma_enabled === true
  return {
    loading,
    announcementsEnabled,
    faqEnabled,
    uptimeEnabled,
    announcements: announcementsEnabled
      ? asList(status?.announcements, isAnnouncement)
      : [],
    faq: faqEnabled ? asList(status?.faq, isFaq) : [],
  }
}
