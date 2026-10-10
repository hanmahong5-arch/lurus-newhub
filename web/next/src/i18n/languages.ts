export const LANGUAGE_OPTIONS = [
  { code: 'zh', label: '简体中文' },
  { code: 'en', label: 'English' },
] as const

export type LanguageCode = (typeof LANGUAGE_OPTIONS)[number]['code']

export const DEFAULT_LANGUAGE: LanguageCode = 'zh'
export const LANGUAGE_STORAGE_KEY = 'newhub-console:language'

/** Anything that is not recognisably English resolves to the default (zh). */
export function normalizeLanguage(value?: string | null): LanguageCode {
  const lower = (value ?? '').trim().replaceAll('_', '-').toLowerCase()
  if (lower === 'en' || lower.startsWith('en-')) return 'en'
  if (lower === 'zh' || lower.startsWith('zh-')) return 'zh'
  return DEFAULT_LANGUAGE
}

/** BCP-47 tag for Intl.* constructors. */
export function toIntlLocale(value?: string | null): string {
  return normalizeLanguage(value) === 'en' ? 'en-US' : 'zh-CN'
}

export function readStoredLanguage(): LanguageCode {
  try {
    return normalizeLanguage(window.localStorage.getItem(LANGUAGE_STORAGE_KEY))
  } catch {
    return DEFAULT_LANGUAGE
  }
}

export function storeLanguage(code: LanguageCode): void {
  try {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, code)
  } catch {
    // Storage can be denied; the choice then lasts for this page load only.
  }
}
