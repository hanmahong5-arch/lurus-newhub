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
import { BRAND_NAME, PRODUCT_NAME } from '@/lib/constants'

import { parseHeaderNav, type HeaderNav } from './header-nav'

export interface SiteInfo {
  loaded: boolean
  name: string
  /** Docs link set by the operator; '' = none. */
  docsLink: string
  /** Operator footer markup (sanitized by the renderer); '' = none. */
  footerHtml: string
  agreementEnabled: boolean
  privacyEnabled: boolean
  /** Public origin of the API, when the operator set one; '' = use this page's. */
  serverAddress: string
  nav: HeaderNav
}

function str(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** What the public pages need from GET /api/status. */
export function useSiteInfo(): SiteInfo {
  const { status, loading } = useStatus()
  return {
    loaded: !loading,
    name: str(status?.system_name) || `${BRAND_NAME} ${PRODUCT_NAME}`,
    docsLink: safeLink(str(status?.docs_link)),
    footerHtml: str(status?.footer_html),
    agreementEnabled: status?.user_agreement_enabled === true,
    privacyEnabled: status?.privacy_policy_enabled === true,
    serverAddress: str(status?.server_address).replace(/\/+$/, ''),
    nav: parseHeaderNav(status?.HeaderNavModules),
  }
}

/** Origin the relay answers on, for copy-paste examples. */
export function relayOrigin(info: Pick<SiteInfo, 'serverAddress'>): string {
  if (info.serverAddress) return info.serverAddress
  return typeof window === 'undefined' ? '' : window.location.origin
}

/**
 * An operator-supplied link, usable as an href: an absolute http(s) URL or a
 * same-site path. Anything else (javascript:, data:, protocol-relative) is
 * dropped to ''.
 */
export function safeLink(value: string): string {
  if (value.startsWith('/') && !value.startsWith('//')) return value
  try {
    const url = new URL(value)
    return url.protocol === 'http:' || url.protocol === 'https:' ? value : ''
  } catch {
    return ''
  }
}
