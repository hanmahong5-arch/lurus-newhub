/**
 * Locale assembly.
 *
 * Translation keys are the English source strings. Common strings live in
 * `src/i18n/locales/{zh,en}.json`; every feature owns its own strings in
 * `src/features/<name>/locales/{zh,en}.json`, so parallel feature work never
 * edits the same JSON file. Feature files are picked up automatically; adding
 * a feature needs no change here.
 */
import common_en from './locales/en.json'
import common_zh from './locales/zh.json'

export type Dictionary = Record<string, string>
export type LocaleCode = 'zh' | 'en'

/** Path of a feature locale -> language, or null if it is not one. */
export function featureLocaleLanguage(path: string): LocaleCode | null {
  const m = /(?:^|\/)features\/[^/]+\/locales\/(zh|en)\.json$/.exec(path)
  return m ? (m[1] as LocaleCode) : null
}

/**
 * Merge dictionaries. Later entries win; feature entries are applied after
 * the common one, so a feature may deliberately refine a common string.
 * Duplicate keys inside two different features are reported through `onClash`.
 */
export function mergeDictionaries(
  entries: Array<{ source: string; dict: Dictionary }>,
  onClash?: (key: string, a: string, b: string) => void
): Dictionary {
  const out: Dictionary = {}
  const owner = new Map<string, string>()
  for (const { source, dict } of entries) {
    for (const [key, value] of Object.entries(dict)) {
      const previous = owner.get(key)
      if (previous && previous !== source && out[key] !== value) {
        onClash?.(key, previous, source)
      }
      out[key] = value
      owner.set(key, source)
    }
  }
  return out
}

/**
 * Feature locale modules, enumerated by the bundler at build time.
 *
 * The call must stay a literal `import.meta.webpackContext(...)`: the bundler
 * only rewrites the exact expression. Reached through a cast or `?.` it is
 * left alone, evaluates to undefined in the shipped bundle, and every feature
 * string silently falls back to English while the unit tests (which pass
 * their own feature list) stay green. Vitest has no such API, hence the guard.
 */
function featureModules(): Array<{
  lang: LocaleCode
  source: string
  dict: Dictionary
}> {
  if ((import.meta.env.MODE as string) === 'test') return []
  const context = import.meta.webpackContext('../features', {
    recursive: true,
    regExp: /\/locales\/(zh|en)\.json$/,
  })
  const found: Array<{ lang: LocaleCode; source: string; dict: Dictionary }> =
    []
  for (const key of context.keys()) {
    const lang = featureLocaleLanguage(`features/${key.replace(/^\.\//, '')}`)
    if (!lang) continue
    const mod = context(key) as { default?: Dictionary } & Dictionary
    const dict = (mod.default ?? mod) as Dictionary
    found.push({ lang, source: key, dict })
  }
  if (found.length === 0) {
    // A console with no feature strings is a broken build, not an empty one.
    // eslint-disable-next-line no-console
    console.error('[i18n] no feature locale files were bundled')
  }
  return found.sort((a, b) => a.source.localeCompare(b.source))
}

export function buildResources(
  features = featureModules()
): Record<LocaleCode, { translation: Dictionary }> {
  const warn = (key: string, a: string, b: string) => {
    if (import.meta.env.DEV) {
      // eslint-disable-next-line no-console
      console.warn(`[i18n] key "${key}" is defined by both ${a} and ${b}`)
    }
  }
  const build = (lang: LocaleCode, common: Dictionary) =>
    mergeDictionaries(
      [
        { source: 'common', dict: common },
        ...features.filter((f) => f.lang === lang),
      ],
      warn
    )
  return {
    zh: { translation: build('zh', common_zh as Dictionary) },
    en: { translation: build('en', common_en as Dictionary) },
  }
}
