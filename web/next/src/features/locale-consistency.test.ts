import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const here = path.dirname(fileURLToPath(import.meta.url))

/** Read every features/<name>/locales/zh.json. */
function loadAll(): Map<string, Map<string, string>> {
  const out = new Map<string, Map<string, string>>()
  for (const dir of fs.readdirSync(here, { withFileTypes: true })) {
    if (!dir.isDirectory()) continue
    const file = path.join(here, dir.name, 'locales', 'zh.json')
    if (!fs.existsSync(file)) continue
    const json = JSON.parse(fs.readFileSync(file, 'utf8')) as Record<
      string,
      unknown
    >
    const m = new Map<string, string>()
    for (const [k, v] of Object.entries(json)) {
      if (typeof v === 'string') m.set(k, v)
    }
    out.set(dir.name, m)
  }
  const shared = path.join(here, '..', 'i18n', 'locales', 'zh.json')
  if (fs.existsSync(shared)) {
    const json = JSON.parse(fs.readFileSync(shared, 'utf8')) as Record<
      string,
      unknown
    >
    out.set(
      'shared',
      new Map(
        Object.entries(json).filter(
          (e): e is [string, string] => typeof e[1] === 'string'
        )
      )
    )
  }
  return out
}

describe('feature zh locales', () => {
  it('the same key has one Chinese translation across features', () => {
    const byKey = new Map<string, Map<string, string[]>>()
    for (const [feature, entries] of loadAll()) {
      for (const [key, zh] of entries) {
        const forKey = byKey.get(key) ?? new Map<string, string[]>()
        forKey.set(zh, [...(forKey.get(zh) ?? []), feature])
        byKey.set(key, forKey)
      }
    }
    const conflicts: string[] = []
    for (const [key, variants] of byKey) {
      if (variants.size > 1) {
        const detail = [...variants]
          .map(([zh, fs]) => `${JSON.stringify(zh)} (${fs.join(',')})`)
          .join(' vs ')
        conflicts.push(`${JSON.stringify(key)}: ${detail}`)
      }
    }
    expect(conflicts).toEqual([])
  })
})
