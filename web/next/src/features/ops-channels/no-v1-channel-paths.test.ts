import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

/**
 * Ops-channels must not call deprecated REST /api/channel|/api/group|
 * /api/prefill_group — the v2 admin facade is the only supported path.
 * Comments that merely mention the old surface are allowed.
 */
const root = join(__dirname)
const banned = [
  /['"`]\/api\/channel(?:\/|$)/,
  /['"`]\/api\/group(?:\/|$)/,
  /['"`]\/api\/prefill_group(?:\/|$)/,
  /fetch\(\s*['"`]\/api\/channel/,
]

function walk(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      out.push(...walk(p))
      continue
    }
    if (
      /\.(ts|tsx)$/.test(name) &&
      !name.endsWith('.test.ts') &&
      !name.endsWith('.test.tsx')
    ) {
      out.push(p)
    }
  }
  return out
}

describe('ops-channels leaves deprecated REST /api', () => {
  it('has no live /api/channel|/api/group|/api/prefill_group call sites', () => {
    const hits: string[] = []
    for (const file of walk(root)) {
      const src = readFileSync(file, 'utf8')
      const lines = src.split(/\r?\n/)
      lines.forEach((line, i) => {
        const trimmed = line.trim()
        if (
          trimmed.startsWith('//') ||
          trimmed.startsWith('*') ||
          trimmed.startsWith('/*')
        ) {
          return
        }
        for (const re of banned) {
          if (re.test(line)) {
            hits.push(`${file}:${i + 1}: ${trimmed}`)
          }
        }
      })
    }
    expect(hits).toEqual([])
  })
})
