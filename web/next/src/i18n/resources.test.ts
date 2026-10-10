import { describe, expect, it, vi } from 'vitest'

import { normalizeLanguage } from './languages'
import {
  buildResources,
  featureLocaleLanguage,
  mergeDictionaries,
} from './resources'

describe('featureLocaleLanguage', () => {
  it('recognises per-feature locale files only', () => {
    expect(featureLocaleLanguage('features/keys/locales/zh.json')).toBe('zh')
    expect(featureLocaleLanguage('features/usage-logs/locales/en.json')).toBe(
      'en'
    )
    expect(featureLocaleLanguage('features/keys/locales/fr.json')).toBeNull()
    expect(featureLocaleLanguage('i18n/locales/zh.json')).toBeNull()
  })
})

describe('mergeDictionaries', () => {
  it('merges disjoint feature dictionaries and reports clashes', () => {
    const onClash = vi.fn()
    const merged = mergeDictionaries(
      [
        { source: 'common', dict: { A: '甲' } },
        { source: 'keys', dict: { B: '乙', C: 'x' } },
        { source: 'logs', dict: { D: '丁', C: 'y' } },
      ],
      onClash
    )
    expect(merged).toEqual({ A: '甲', B: '乙', C: 'y', D: '丁' })
    expect(onClash).toHaveBeenCalledWith('C', 'keys', 'logs')
  })

  it('does not flag an identical string defined twice', () => {
    const onClash = vi.fn()
    mergeDictionaries(
      [
        { source: 'a', dict: { K: 'same' } },
        { source: 'b', dict: { K: 'same' } },
      ],
      onClash
    )
    expect(onClash).not.toHaveBeenCalled()
  })
})

describe('buildResources', () => {
  it('layers feature dictionaries over the common one per language', () => {
    const res = buildResources([
      { lang: 'zh', source: './keys/locales/zh.json', dict: { Foo: '富' } },
      { lang: 'en', source: './keys/locales/en.json', dict: { Foo: 'Foo!' } },
    ])
    expect(res.zh.translation.Foo).toBe('富')
    expect(res.en.translation.Foo).toBe('Foo!')
    expect(res.zh.translation.Dashboard).toBe('仪表盘')
  })
})

describe('normalizeLanguage', () => {
  it('defaults to Chinese and recognises English variants', () => {
    expect(normalizeLanguage(undefined)).toBe('zh')
    expect(normalizeLanguage('fr')).toBe('zh')
    expect(normalizeLanguage('en-GB')).toBe('en')
    expect(normalizeLanguage('zh-TW')).toBe('zh')
  })
})
