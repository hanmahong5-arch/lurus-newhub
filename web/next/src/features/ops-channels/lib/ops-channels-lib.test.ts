import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import zh from '../locales/zh.json'
import { buildImportRequest, EMPTY_IMPORT_FORM } from './import'
import { buildKeySettings } from './key-form'
import {
  buildSettingJson,
  canRestore,
  checkImportInput,
  countImportItems,
  expiryToUnix,
  formatCny4,
  formatCountdown,
  mapChannelDetail,
  mapChannelHealth,
  mapChannelList,
  mapChannelUsage,
  mapHealthWindow,
  mapImportOutcome,
  mapKeyProbe,
  planToForm,
  REASON_LABELS,
  type PlanForm,
} from './map'

const BLANK: PlanForm = {
  planKind: '',
  thresholdPct: '',
  expiresOn: '',
  monthlyFeeCny: '',
  testMode: '',
}

describe('mapChannelList', () => {
  it('maps the whitelisted list view and keeps the total', () => {
    const r = mapChannelList({
      channels: [
        { id: 7, name: 'pool-a', type: 1, key: 'sk-****', status: 3, group: 'g' },
      ],
      total: 41,
    })
    expect(r.total).toBe(41)
    expect(r.items[0]).toMatchObject({ id: 7, name: 'pool-a', status: 3 })
  })

  it('maps the operations columns and fails safe when absent', () => {
    const r = mapChannelList({
      channels: [
        {
          id: 1,
          name: 'a',
          plan_kind: 'kimi_coding',
          expires_at: 1_900_000_000,
          key_count: 3,
          enabled_key_count: 2,
          routable: false,
          unroutable_reasons: ['expired', 5],
        },
        { id: 2, name: 'b' },
      ],
      total: 2,
    })
    expect(r.items[0]).toMatchObject({
      planKind: 'kimi_coding',
      expiresAt: 1_900_000_000,
      keyCount: 3,
      enabledKeyCount: 2,
      routable: false,
      unroutableReasons: ['expired'],
    })
    // A row without the verdict is never shown as routable.
    expect(r.items[1]).toMatchObject({ routable: false, unroutableReasons: [] })
  })

  it('tolerates a null channels array', () => {
    expect(mapChannelList({ channels: null, total: 0 }).items).toEqual([])
  })
})

describe('mapChannelDetail', () => {
  it('reads plan settings, key overrides and the last plan-quota windows', () => {
    const d = mapChannelDetail({
      id: 3,
      setting: JSON.stringify({
        plan_kind: 'minimax',
        plan_threshold_pct: 90,
        expires_at: 1_900_000_000,
        plan_monthly_fee_cny4: 2_000_000,
        test_mode: 'none',
        proxy: 'http://p',
      }),
      other_info: JSON.stringify({
        plan_quota: {
          windows: [{ name: '5h', used_pct: 42.5, reset_at: 1_900_000_100 }],
          error: '',
        },
      }),
      channel_info: {
        is_multi_key: true,
        multi_key_size: 3,
        multi_key_proxy: { '1': 'http://k1' },
        multi_key_weight: { '2': 0 },
      },
    })
    expect(d.isMultiKey).toBe(true)
    expect(d.keyCount).toBe(3)
    expect(d.keyProxy[1]).toBe('http://k1')
    expect(d.keyWeight[2]).toBe(0)
    expect(d.plan).toEqual({
      planKind: 'minimax',
      thresholdPct: 90,
      expiresAt: 1_900_000_000,
      monthlyFeeCny4: 2_000_000,
      testMode: 'none',
    })
    expect(d.planWindows).toEqual([
      { name: '5h', usedPct: 42.5, resetAt: 1_900_000_100 },
    ])
  })

  it('counts a single-key channel as one key', () => {
    expect(mapChannelDetail({ id: 1, channel_info: {} }).keyCount).toBe(1)
  })
})

describe('buildSettingJson', () => {
  it('keeps members it does not own and writes the plan members', () => {
    const r = buildSettingJson('{"proxy":"http://p","system_prompt":"x"}', {
      planKind: 'zhipu_coding',
      thresholdPct: '80',
      expiresOn: '2030-01-15',
      monthlyFeeCny: '199.5',
      testMode: 'auto_ban_only',
    })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    const doc = JSON.parse(r.setting)
    expect(doc.proxy).toBe('http://p')
    expect(doc.system_prompt).toBe('x')
    expect(doc.plan_kind).toBe('zhipu_coding')
    expect(doc.plan_threshold_pct).toBe(80)
    expect(doc.plan_monthly_fee_cny4).toBe(1_995_000)
    expect(doc.test_mode).toBe('auto_ban_only')
    expect(doc.expires_at).toBe(expiryToUnix('2030-01-15'))
  })

  it('removes plan members that were cleared', () => {
    const r = buildSettingJson(
      '{"plan_kind":"minimax","plan_threshold_pct":90,"expires_at":5,"plan_monthly_fee_cny4":10,"test_mode":"all","proxy":"p"}',
      BLANK
    )
    expect(r.ok).toBe(true)
    if (r.ok) expect(JSON.parse(r.setting)).toEqual({ proxy: 'p' })
  })

  it('treats a pay-as-you-go channel with no stored setting as an empty document', () => {
    const r = buildSettingJson('', BLANK)
    expect(r).toEqual({ ok: true, setting: '{}' })
  })

  it('rejects out-of-range and malformed input', () => {
    const bad = (over: Partial<PlanForm>) => buildSettingJson('{}', { ...BLANK, ...over })
    expect(bad({ thresholdPct: '0' })).toEqual({ ok: false, error: 'threshold' })
    expect(bad({ thresholdPct: '101' })).toEqual({ ok: false, error: 'threshold' })
    expect(bad({ expiresOn: '2030-02-31' })).toEqual({ ok: false, error: 'expiry' })
    expect(bad({ monthlyFeeCny: '-1' })).toEqual({ ok: false, error: 'fee' })
    expect(bad({ planKind: 'other' })).toEqual({ ok: false, error: 'kind' })
    expect(bad({ testMode: 'sometimes' })).toEqual({ ok: false, error: 'test-mode' })
  })

  it('refuses to overwrite a corrupt stored document', () => {
    expect(buildSettingJson('{not json', BLANK)).toEqual({
      ok: false,
      error: 'setting-corrupt',
    })
  })

  it('round-trips the form through planToForm', () => {
    const form = planToForm({
      planKind: 'minimax',
      thresholdPct: 90,
      expiresAt: expiryToUnix('2031-06-01'),
      monthlyFeeCny4: 1_234_500,
      testMode: 'all',
    })
    expect(form).toEqual({
      planKind: 'minimax',
      thresholdPct: '90',
      expiresOn: '2031-06-01',
      monthlyFeeCny: '123.45',
      testMode: 'all',
    })
  })
})

describe('health', () => {
  it('maps reasons, cooldown and per-key state', () => {
    const h = mapChannelHealth({
      channel_id: 5,
      routable: false,
      reasons: ['cooling_429', 'plan_window_exhausted'],
      cooldown_until: 2000,
      window: null,
      last_success_at: null,
      keys: [
        {
          index: 0,
          fingerprint: 'abc',
          routable: false,
          reasons: ['auth_failed'],
          cooldown_until: 0,
          weight: 20,
          has_proxy: true,
          last_error: 'bad key',
        },
      ],
    })
    expect(h.routable).toBe(false)
    expect(h.reasons).toEqual(['cooling_429', 'plan_window_exhausted'])
    expect(h.lastSuccessAt).toBeNull()
    expect(h.windows).toEqual([])
    expect(h.keys[0]).toMatchObject({ weight: 20, hasProxy: true, lastError: 'bad key' })
  })

  it('has a label for every stable reason code', () => {
    for (const code of [
      'disabled_manual',
      'auto_disabled',
      'cooling_429',
      'plan_window_exhausted',
      'expired',
      'balance_low',
      'auth_failed',
    ]) {
      expect(REASON_LABELS[code]).toBeTruthy()
    }
  })

  it('accepts both window shapes', () => {
    expect(mapHealthWindow({ windows: [{ name: '5h', used_pct: 10 }] })).toHaveLength(1)
    expect(mapHealthWindow({ name: 'weekly', used_pct: 70, reset_at: 9 })).toEqual([
      { name: 'weekly', usedPct: 70, resetAt: 9 },
    ])
    expect(mapHealthWindow(null)).toEqual([])
  })

  it('formats a countdown and clamps at zero', () => {
    expect(formatCountdown(1000 + 3725, 1000)).toBe('1h 02m')
    expect(formatCountdown(1000 + 247, 1000)).toBe('4m 07s')
    expect(formatCountdown(1000 + 12, 1000)).toBe('12s')
    expect(formatCountdown(900, 1000)).toBe('0s')
  })
})

describe('key probe', () => {
  it('only allows a restore with a fresh proof from a passing probe', () => {
    const p = mapKeyProbe({
      ok: true,
      latency_ms: 80,
      restore_proof: '1.abc',
      restore_proof_expires_at: 2000,
    })
    expect(canRestore(p, 1999)).toBe(true)
    expect(canRestore(p, 2000)).toBe(false)
    expect(canRestore({ ...p, ok: false }, 1000)).toBe(false)
    expect(canRestore({ ...p, restoreProof: '' }, 1000)).toBe(false)
  })

  it('sends only changed key settings', () => {
    const cur = { weight: 50, proxy: '' }
    expect(buildKeySettings({ weight: '50', proxy: '' }, cur)).toEqual({ ok: true, body: {} })
    expect(buildKeySettings({ weight: '70', proxy: '' }, cur)).toEqual({
      ok: true,
      body: { weight: 70 },
    })
    expect(buildKeySettings({ weight: '50', proxy: ' http://x ' }, cur)).toEqual({
      ok: true,
      body: { proxy: 'http://x' },
    })
    expect(
      buildKeySettings({ weight: '50', proxy: '' }, { weight: 50, proxy: 'http://x' })
    ).toEqual({ ok: true, body: { proxy: '' } })
    expect(buildKeySettings({ weight: '101', proxy: '' }, cur)).toEqual({
      ok: false,
      error: 'weight',
    })
    expect(buildKeySettings({ weight: '1.5', proxy: '' }, cur).ok).toBe(false)
  })
})

describe('usage', () => {
  it('maps totals, per-key rows and a missing utilization to null', () => {
    const u = mapChannelUsage({
      window: '7d',
      keys: [
        { key_idx: -1, requests: 3, cost_cny4: 5000 },
        { key_idx: 0, requests: 1 },
      ],
      total: { requests: 4, errors: 1, cost_cny4: 5000, quota: 100 },
      plan_monthly_fee_cny4: 0,
    })
    expect(u.keys.map((k) => k.keyIdx)).toEqual([-1, 0])
    expect(u.total.costCny4).toBe(5000)
    expect(u.utilization).toBeNull()
  })

  it('keeps a utilization of zero (an idle paid plan) distinct from none', () => {
    expect(mapChannelUsage({ utilization: 0, total: {}, keys: [] }).utilization).toBe(0)
  })

  it('formats server CNY units (0.0001 yuan)', () => {
    expect(formatCny4(1_995_000)).toBe('¥199.50')
    expect(formatCny4(undefined)).toBe('--')
  })
})

describe('import', () => {
  it('maps dry-run rows with their status and error code', () => {
    const o = mapImportOutcome({
      dry_run: true,
      summary: { ready: 1, duplicate: 1, invalid: 0, probe_failed: 0, imported: 0 },
      results: [
        { index: 0, status: 'ready', fingerprint: 'aa' },
        {
          index: 1,
          status: 'duplicate',
          error_code: 'duplicate_existing_key',
          existing_channel_id: 9,
        },
      ],
    })
    expect(o.rows[1]).toMatchObject({ errorCode: 'duplicate_existing_key', existingChannelId: 9 })
    expect(o.summary.ready).toBe(1)
  })

  it('validates the pasted text shapes', () => {
    expect(checkImportInput('  ')).toBe('empty')
    expect(checkImportInput('[1,')).toBe('bad-json')
    expect(checkImportInput('[]')).toBe('empty')
    expect(checkImportInput('k1\nk2')).toBe('ok')
    expect(countImportItems('k1\n\n k2 \n')).toBe(2)
    expect(countImportItems('["a","b","c"]')).toBe(3)
  })

  it('builds an append request without type fields', () => {
    const r = buildImportRequest({ ...EMPTY_IMPORT_FORM, keys: 'k1', target: '12', probe: true }, false)
    expect(r).toEqual({
      ok: true,
      body: { dry_run: false, probe: true, keys: 'k1', channel_id: 12 },
    })
  })

  it('requires a type when creating new channels, and never probes a dry run', () => {
    expect(buildImportRequest({ ...EMPTY_IMPORT_FORM, keys: 'k1' }, true)).toEqual({
      ok: false,
      error: 'type',
    })
    const r = buildImportRequest(
      { ...EMPTY_IMPORT_FORM, keys: 'k1', type: '1', models: 'model-a', probe: true },
      true
    )
    expect(r).toEqual({
      ok: true,
      body: { dry_run: true, probe: false, keys: 'k1', type: 1, models: 'model-a' },
    })
    expect(buildImportRequest(EMPTY_IMPORT_FORM, true)).toEqual({ ok: false, error: 'keys' })
  })
})

describe('locale', () => {
  it('has a Chinese entry for every string the feature renders', () => {
    const root = join(import.meta.dirname, '..')
    const used = new Set<string>()
    const call = new RegExp("\\bt\\(\\s*'((?:[^'\\\\]|\\\\.)*)'", 'g')
    const label = new RegExp("^\\s+[\\w']+: '((?:[^'\\\\]|\\\\.)*)',$", 'gm')
    const walk = (dir: string) => {
      for (const f of readdirSync(dir)) {
        const p = join(dir, f)
        if (statSync(p).isDirectory()) {
          if (f !== 'locales') walk(p)
          continue
        }
        if (!/\.tsx?$/.test(f) || /\.test\./.test(f)) continue
        const src = readFileSync(p, 'utf8')
        for (const m of src.matchAll(call)) used.add(m[1])
        if (f === 'map.ts') {
          for (const m of src.matchAll(label)) {
            if (REASON_LABELS_VALUES.has(m[1]) || IMPORT_LABELS_VALUES.has(m[1])) used.add(m[1])
          }
        }
      }
    }
    walk(root)
    const dict = zh as Record<string, string>
    const missing = [...used].filter((k) => !dict[k])
    expect(missing).toEqual([])
    for (const v of REASON_LABELS_VALUES) expect(dict[v]).toBeTruthy()
    for (const v of IMPORT_LABELS_VALUES) expect(dict[v]).toBeTruthy()
  })
})

const REASON_LABELS_VALUES = new Set(Object.values(REASON_LABELS))
const IMPORT_LABELS_VALUES = new Set<string>([
  'Empty key',
  'Weight must be between 0 and 100',
  'Invalid expiry time',
  'Proxy address rejected by the safety check',
  'Models are required',
  'Base URL rejected by the safety check',
  'Already exists in this workspace',
  'Repeated in this batch',
  'Key test failed',
  'Test cancelled',
])
