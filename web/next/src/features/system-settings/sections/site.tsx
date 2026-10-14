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
import { useMemo } from 'react'

import { OptionForm } from '../components/option-form'
import type { OptionField } from '../lib/option-fields'

const BRANDING_FIELDS: OptionField[] = [
  {
    kind: 'text',
    key: 'SystemName',
    label: 'System Name',
    description: 'The name displayed across the application',
    required: true,
    maxLength: 64,
  },
  {
    kind: 'text',
    key: 'ServerAddress',
    label: 'Server Address',
    description:
      'The public URL of this gateway, used in links and callbacks sent to people',
    placeholder: 'https://gateway.example.com',
    url: true,
    trimTrailingSlash: true,
  },
  {
    kind: 'text',
    key: 'Logo',
    label: 'Logo URL',
    description: 'URL to your logo image (optional)',
    placeholder: 'https://example.com/logo.png',
    url: true,
  },
  {
    kind: 'text',
    key: 'general_setting.docs_link',
    label: 'Documentation Link',
    description:
      'Link to your documentation site. Leave empty to hide the Docs entry.',
    placeholder: 'https://docs.example.com',
    url: true,
  },
  {
    kind: 'text',
    key: 'Footer',
    label: 'Footer',
    description: 'Footer text displayed at the bottom of pages',
    placeholder: '© 2026 Your Company. All rights reserved.',
    rows: 4,
  },
]

const PAGES_FIELDS: OptionField[] = [
  {
    kind: 'text',
    key: 'Notice',
    label: 'System Notice',
    description: 'Shown to everyone who opens the console. Leave empty for none.',
    placeholder: 'Planned maintenance on Friday at 22:00 UTC...',
    rows: 6,
  },
  {
    kind: 'text',
    key: 'HomePageContent',
    label: 'Home Page Content',
    description:
      'Content displayed on the home page (Markdown or HTML, or a full URL to embed)',
    placeholder: 'Welcome to our gateway...',
    rows: 8,
  },
  {
    kind: 'text',
    key: 'About',
    label: 'About',
    description:
      'Supports HTML markup or iframe embedding. Enter HTML code directly, or provide a complete URL to automatically embed it as an iframe.',
    placeholder: '<p>About us...</p>',
    rows: 8,
  },
]

const LEGAL_FIELDS: OptionField[] = [
  {
    kind: 'text',
    key: 'legal.user_agreement',
    label: 'User Agreement',
    description:
      'Leave empty to disable the agreement. Supports Markdown, HTML, or a full URL to redirect people.',
    placeholder: 'Provide Markdown, HTML, or an external URL',
    rows: 10,
  },
  {
    kind: 'text',
    key: 'legal.privacy_policy',
    label: 'Privacy Policy',
    description:
      'Leave empty to disable the privacy policy. Supports Markdown, HTML, or a full URL to redirect people.',
    placeholder: 'Provide Markdown, HTML, or an external URL',
    rows: 10,
  },
]

const PANEL_FIELDS: OptionField[] = [
  {
    kind: 'switch',
    key: 'console_setting.announcements_enabled',
    label: 'Announcements panel',
    description: 'Show the announcements panel on the dashboard',
    defaultValue: true,
  },
  {
    kind: 'json',
    key: 'console_setting.announcements',
    label: 'Announcements',
    expect: 'array',
    description:
      'JSON array of announcements: content, publishDate (ISO time), type (default, ongoing, success, warning or error) and an optional extra.',
    visibleWhen: (draft) => draft.bool('console_setting.announcements_enabled'),
  },
  {
    kind: 'switch',
    key: 'console_setting.api_info_enabled',
    label: 'API information panel',
    description: 'Show the API endpoint list on the dashboard',
    defaultValue: true,
  },
  {
    kind: 'json',
    key: 'console_setting.api_info',
    label: 'API information',
    expect: 'array',
    description:
      'JSON array with url, route, description and color for each endpoint (at most 50).',
    visibleWhen: (draft) => draft.bool('console_setting.api_info_enabled'),
  },
  {
    kind: 'switch',
    key: 'console_setting.faq_enabled',
    label: 'FAQ panel',
    description: 'Show the frequently asked questions on the dashboard',
    defaultValue: true,
  },
  {
    kind: 'json',
    key: 'console_setting.faq',
    label: 'FAQ',
    expect: 'array',
    description: 'JSON array of {"question": "...", "answer": "..."} entries.',
    visibleWhen: (draft) => draft.bool('console_setting.faq_enabled'),
  },
  {
    kind: 'switch',
    key: 'console_setting.uptime_kuma_enabled',
    label: 'Uptime panel',
    description: 'Show the service status groups on the dashboard',
    defaultValue: true,
  },
  {
    kind: 'json',
    key: 'console_setting.uptime_kuma_groups',
    label: 'Uptime groups',
    expect: 'array',
    description:
      'JSON array of status page groups: categoryName, url, slug and description.',
    visibleWhen: (draft) => draft.bool('console_setting.uptime_kuma_enabled'),
  },
]

export function BrandingSection() {
  const fields = useMemo(() => BRANDING_FIELDS, [])
  return <OptionForm fields={fields} />
}

export function PagesSection() {
  return <OptionForm fields={PAGES_FIELDS} />
}

export function LegalSection() {
  return <OptionForm fields={LEGAL_FIELDS} />
}

export function PanelsSection() {
  return <OptionForm fields={PANEL_FIELDS} />
}
