import { describe, expect, it } from 'vitest'

import { channelRow } from '../channels-test-utils'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  aggregateChannelsByTag,
  buildOtherSettingsDocument,
  buildSettingDocument,
  channelFormSchema,
  deduplicateKeys,
  formatResponseTime,
  formatUsdBalance,
  getChannelTypeLabel,
  parseGroupsList,
  syncModelsWithMappingPairs,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from './index'

const base: ChannelFormValues = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'alpha',
  key: 'sk-1',
  models: 'model-b, model-a ,model-a',
  group: ['default', 'vip'],
}

describe('create payload', () => {
  it('sends a single key with trimmed, de-duplicated models', () => {
    const body = transformFormDataToCreatePayload(base)
    expect(body.mode).toBe('single')
    expect(body.multi_key_mode).toBeUndefined()
    expect(body.channel).toMatchObject({
      name: 'alpha',
      type: 1,
      key: 'sk-1',
      models: 'model-b,model-a',
      group: 'default,vip',
      status: 1,
      auto_ban: 1,
    })
  })

  it('carries the rotation only for the multi-key mode', () => {
    const multi = transformFormDataToCreatePayload({
      ...base,
      multi_key_mode: 'multi_to_single',
      multi_key_type: 'polling',
    })
    expect(multi.mode).toBe('multi_to_single')
    expect(multi.multi_key_mode).toBe('polling')

    const batch = transformFormDataToCreatePayload({
      ...base,
      multi_key_mode: 'batch',
      batch_add_set_key_prefix_2_name: true,
    })
    expect(batch.mode).toBe('batch')
    expect(batch.batch_add_set_key_prefix_2_name).toBe(true)
    expect(batch.multi_key_mode).toBeUndefined()
  })
})

describe('update payload', () => {
  const stored = channelRow(7, {
    name: 'stored',
    setting: JSON.stringify({ plan_kind: 'minimax', proxy: 'http://old:1' }),
    settings: JSON.stringify({ vertex_key_type: 'json', custom: 1 }),
    channel_info: {
      is_multi_key: true,
      multi_key_size: 2,
      multi_key_polling_index: 0,
      multi_key_mode: 'random',
    },
  })

  it('omits a blank key so the stored one is kept', () => {
    const values = transformChannelToFormDefaults(stored)
    const body = transformFormDataToUpdatePayload(values, stored)
    expect('key' in body).toBe(false)
    expect('key_mode' in body).toBe(false)
    expect(body.multi_key_mode).toBe('random')
  })

  it('sends the key mode only together with a new key on a multi-key channel', () => {
    const values = {
      ...transformChannelToFormDefaults(stored),
      key: ' sk-new ',
      key_mode: 'append' as const,
    }
    const body = transformFormDataToUpdatePayload(values, stored)
    expect(body.key).toBe('sk-new')
    expect(body.key_mode).toBe('append')
  })

  it('round-trips the stored form values', () => {
    const values = transformChannelToFormDefaults(
      channelRow(3, {
        group: 'default,vip',
        base_url: 'https://u.example',
        tag: 'blue',
        remark: 'note',
        priority: 4,
      })
    )
    expect(values).toMatchObject({
      group: ['default', 'vip'],
      base_url: 'https://u.example',
      tag: 'blue',
      remark: 'note',
      priority: 4,
      key: '',
    })
  })
})

describe('setting documents', () => {
  it('writes managed members and keeps every other member', () => {
    const doc = JSON.parse(
      buildSettingDocument(
        { ...base, proxy: 'socks5://p:1', force_format: true },
        JSON.stringify({ plan_kind: 'minimax', expires_at: 99 })
      )
    ) as Record<string, unknown>
    expect(doc).toEqual({
      plan_kind: 'minimax',
      expires_at: 99,
      proxy: 'socks5://p:1',
      force_format: true,
    })
  })

  it('drops a managed member when it is cleared', () => {
    const doc = JSON.parse(
      buildSettingDocument(
        { ...base, proxy: '', force_format: false },
        JSON.stringify({ proxy: 'http://old:1', force_format: true, keep: 1 })
      )
    ) as Record<string, unknown>
    expect(doc).toEqual({ keep: 1 })
  })

  it('survives a corrupt stored document', () => {
    expect(buildSettingDocument(base, '{not json')).toBe('{}')
  })

  it('writes type-specific members only for their type', () => {
    const vertex = JSON.parse(
      buildOtherSettingsDocument(
        { ...base, type: 41, vertex_key_type: 'api_key' },
        ''
      )
    ) as Record<string, unknown>
    expect(vertex).toEqual({ vertex_key_type: 'api_key' })

    const plain = JSON.parse(
      buildOtherSettingsDocument(
        { ...base, type: 1, vertex_key_type: 'api_key' },
        JSON.stringify({ vertex_key_type: 'json' })
      )
    ) as Record<string, unknown>
    expect(plain).toEqual({})
  })
})

describe('form schema', () => {
  it('accepts a minimal valid channel', () => {
    expect(channelFormSchema.safeParse(base).success).toBe(true)
  })

  it('requires a name, models and a group', () => {
    const result = channelFormSchema.safeParse({
      ...base,
      name: '',
      models: '',
      group: [],
    })
    expect(result.success).toBe(false)
    const paths = result.error?.issues.map((i) => i.path.join('.')) ?? []
    expect(paths).toEqual(expect.arrayContaining(['name', 'models', 'group']))
  })

  it('requires a base URL for the types that cannot run without one', () => {
    const result = channelFormSchema.safeParse({ ...base, type: 3 })
    expect(result.success).toBe(false)
    expect(result.error?.issues.map((i) => i.path.join('.'))).toContain(
      'base_url'
    )
  })

  it.each([
    ['not a url', false],
    ['ftp://host:1', false],
    ['http://host:8080', true],
    ['socks5h://u:p@host:1080', true],
    ['', true],
  ])('validates the proxy %s -> %s', (proxy, valid) => {
    expect(channelFormSchema.safeParse({ ...base, proxy }).success).toBe(valid)
  })

  it('rejects a model mapping that is not string to string', () => {
    expect(
      channelFormSchema.safeParse({ ...base, model_mapping: '{"a": 1}' }).success
    ).toBe(false)
    expect(
      channelFormSchema.safeParse({ ...base, model_mapping: '{"a": "b"}' }).success
    ).toBe(true)
  })

  it('checks status code mapping ranges', () => {
    expect(
      channelFormSchema.safeParse({ ...base, status_code_mapping: '{"400":"500"}' })
        .success
    ).toBe(true)
    expect(
      channelFormSchema.safeParse({ ...base, status_code_mapping: '{"99":"500"}' })
        .success
    ).toBe(false)
  })

  it('requires valid JSON objects for the overrides', () => {
    expect(
      channelFormSchema.safeParse({ ...base, param_override: '[1]' }).success
    ).toBe(false)
    expect(
      channelFormSchema.safeParse({ ...base, header_override: '{"X-A":"b"}' })
        .success
    ).toBe(true)
  })

  it('requires the region for Vertex and a valid key', () => {
    const vertex = {
      ...base,
      type: 41,
      vertex_key_type: 'json' as const,
      other: '{"default":"us"}',
    }
    expect(channelFormSchema.safeParse({ ...vertex, key: '{"a":1}' }).success).toBe(
      true
    )
    expect(channelFormSchema.safeParse({ ...vertex, key: 'nope' }).success).toBe(
      false
    )
    expect(channelFormSchema.safeParse({ ...vertex, other: '' }).success).toBe(
      false
    )
  })
})

describe('tag aggregation', () => {
  it('groups by tag and summarises the children', () => {
    const rows = aggregateChannelsByTag([
      channelRow(1, { tag: 'blue', priority: 5, weight: 1, used_quota: 10, group: 'a' }),
      channelRow(2, { tag: 'blue', priority: 5, weight: 2, used_quota: 5, group: 'b,a', status: 2 }),
      channelRow(3, { tag: null }),
    ])
    expect(rows).toHaveLength(2)
    const blue = rows[0] as ReturnType<typeof channelRow> & {
      children: unknown[]
    }
    expect(blue.children).toHaveLength(2)
    expect(blue.priority).toBe(5)
    expect(blue.weight).toBeNull()
    expect(blue.used_quota).toBe(15)
    expect(blue.group).toBe('a,b')
    expect(blue.status).toBe(1)
  })

  it('marks a tag disabled when no child is enabled', () => {
    const [row] = aggregateChannelsByTag([
      channelRow(1, { tag: 't', status: 2 }),
      channelRow(2, { tag: 't', status: 3 }),
    ])
    expect(row?.status).toBe(2)
  })
})

describe('helpers', () => {
  it('sorts groups with default first', () => {
    expect(parseGroupsList('vip, default ,alpha')).toEqual([
      'default',
      'alpha',
      'vip',
    ])
    expect(parseGroupsList('')).toEqual([])
  })

  it('formats balances as USD and never as quota', () => {
    expect(formatUsdBalance(12.5)).toBe('$12.50')
    expect(formatUsdBalance(0.12345)).toBe('$0.1235')
    expect(formatUsdBalance(Number.NaN)).toBe('-')
    expect(formatUsdBalance(null)).toBe('-')
  })

  it('formats response times', () => {
    expect(formatResponseTime(0)).toBe('Not tested')
    expect(formatResponseTime(420)).toBe('420ms')
    expect(formatResponseTime(1500)).toBe('1.50s')
  })

  it('keeps unknown channel types visible', () => {
    expect(getChannelTypeLabel(14)).toBe('Anthropic')
    expect(getChannelTypeLabel(9999)).toBe('Type #9999')
  })

  it('de-duplicates keys in order', () => {
    expect(deduplicateKeys('a\nb\n a \nb\n')).toEqual({
      deduplicatedText: 'a\nb',
      beforeCount: 5,
      afterCount: 2,
      removedCount: 3,
    })
  })

  it('publishes aliases and hides raw upstream names when syncing', () => {
    expect(
      syncModelsWithMappingPairs('raw-1,keep', [
        { from: 'alias-1', to: 'raw-1' },
      ])
    ).toBe('keep,alias-1')
  })
})
