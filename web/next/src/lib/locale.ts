import i18next from 'i18next'

import { toIntlLocale } from '@/i18n/languages'

/**
 * BCP-47 locale of the console UI language (zh -> zh-CN, en -> en-US).
 * Date/number formatting must use this, not the browser locale, so a Chinese
 * UI never shows "10/3/2026, 4:05:06 PM".
 */
export function currentLocale(): string {
  return toIntlLocale(i18next.language)
}
