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
  Activity,
  CircleDollarSign,
  Coins,
  FileText,
  Gauge,
  Globe,
  LayoutDashboard,
  Palette,
  Route,
  Scale,
  ScrollText,
  ShieldAlert,
  SlidersHorizontal,
  Tags,
  Users,
  type LucideIcon,
} from 'lucide-react'
import type { ComponentType } from 'react'

import {
  BehaviorSection,
  ChannelsSection,
  CurrencySection,
  LimitsSection,
  LogsSection,
  ModerationSection,
  OutboundSection,
  QuotaSection,
  RelaySection,
} from './operations'
import { GroupPricingSection, ModelPricingSection } from './pricing'
import {
  BrandingSection,
  LegalSection,
  PagesSection,
  PanelsSection,
} from './site'

export type SectionDef = {
  /** URL slug: /admin/settings/<id>. */
  id: string
  /** English source string for the nav entry and the page title. */
  title: string
  description?: string
  icon: LucideIcon
  component: ComponentType
}

export type SectionGroup = {
  id: string
  title: string
  sections: SectionDef[]
}

/**
 * Only sections whose option keys exist on this server are listed. Accounts,
 * sign-in providers, payment gateways, check-in and invitation rewards belong
 * to the platform identity service and are deliberately absent.
 */
export const SECTION_GROUPS: SectionGroup[] = [
  {
    id: 'site',
    title: 'Site',
    sections: [
      {
        id: 'branding',
        title: 'Branding',
        description:
          'Name, logo and footer shown across the console and public pages.',
        icon: Palette,
        component: BrandingSection,
      },
      {
        id: 'pages',
        title: 'Notice & pages',
        description: 'The system notice, home page and about page.',
        icon: FileText,
        component: PagesSection,
      },
      {
        id: 'legal',
        title: 'Legal documents',
        description: 'User agreement and privacy policy.',
        icon: Scale,
        component: LegalSection,
      },
      {
        id: 'panels',
        title: 'Dashboard panels',
        description: 'Announcements, API information, FAQ and uptime groups.',
        icon: LayoutDashboard,
        component: PanelsSection,
      },
    ],
  },
  {
    id: 'operations',
    title: 'Operations',
    sections: [
      {
        id: 'behavior',
        title: 'Behavior',
        icon: SlidersHorizontal,
        component: BehaviorSection,
      },
      {
        id: 'quota',
        title: 'Quota',
        icon: Coins,
        component: QuotaSection,
      },
      {
        id: 'currency',
        title: 'Currency & display',
        icon: CircleDollarSign,
        component: CurrencySection,
      },
      {
        id: 'channels',
        title: 'Channel health',
        description:
          'Automatic disabling, retries, scheduled tests and per-model probes.',
        icon: Activity,
        component: ChannelsSection,
      },
      {
        id: 'logs',
        title: 'Logs & data',
        icon: ScrollText,
        component: LogsSection,
      },
      {
        id: 'moderation',
        title: 'Content moderation',
        icon: ShieldAlert,
        component: ModerationSection,
      },
      {
        id: 'outbound',
        title: 'Outbound requests',
        description:
          'What the server itself may fetch (SSRF guard) and the optional fetch worker.',
        icon: Globe,
        component: OutboundSection,
      },
      {
        id: 'limits',
        title: 'Rate limits',
        icon: Gauge,
        component: LimitsSection,
      },
      {
        id: 'relay',
        title: 'Relay',
        icon: Route,
        component: RelaySection,
      },
    ],
  },
  {
    id: 'pricing',
    title: 'Models & pricing',
    sections: [
      {
        id: 'model-prices',
        title: 'Model prices',
        icon: Tags,
        component: ModelPricingSection,
      },
      {
        id: 'group-pricing',
        title: 'Groups & ratios',
        icon: Users,
        component: GroupPricingSection,
      },
    ],
  },
]

export const ALL_SECTIONS: SectionDef[] = SECTION_GROUPS.flatMap(
  (group) => group.sections
)

export const DEFAULT_SECTION_ID = 'branding'

export function findSection(id: string | undefined): SectionDef | undefined {
  return ALL_SECTIONS.find((section) => section.id === id)
}
