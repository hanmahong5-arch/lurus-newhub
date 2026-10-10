import i18next from 'i18next'
import { afterEach, describe, expect, it } from 'vitest'

import { currentLocale } from './locale'

describe('currentLocale', () => {
  const prev = i18next.language
  afterEach(async () => {
    await i18next.changeLanguage(prev)
  })

  it('maps the UI language to a BCP-47 tag', async () => {
    await i18next.changeLanguage('zh')
    expect(currentLocale()).toBe('zh-CN')
    await i18next.changeLanguage('en')
    expect(currentLocale()).toBe('en-US')
  })
})
