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
import { beforeEach, describe, expect, it } from 'vitest'

import { DEFAULT_CONFIG, DEFAULT_PARAMETER_ENABLED } from '../constants'
import type { ChatCompletionRequest } from '../types'
import {
  buildChatCompletionPayload,
  buildCurlSnippet,
  createUserMessage,
  getGroupFallback,
  getModelFallback,
  loadConfig,
  parseRequestErrorDetails,
  saveConfig,
  setPlaygroundStorageScope,
} from './index'

describe('parseRequestErrorDetails', () => {
  it('reads a relay error body from a wrapped axios error', () => {
    const error = Object.assign(new Error('Request failed with status code 429'), {
      cause: {
        response: {
          data: { error: { message: 'slow down', code: 'rate_limited' } },
        },
      },
    })
    expect(parseRequestErrorDetails(error)).toEqual({
      errorCode: 'rate_limited',
      errorMessage: 'slow down',
    })
  })

  it('reads a console envelope failure (tenant gate)', () => {
    const error = {
      message: 'fallback',
      response: {
        data: { success: false, message: 'tenant disabled', error_code: 'TENANT_DISABLED' },
      },
    }
    expect(parseRequestErrorDetails(error)).toEqual({
      errorCode: 'TENANT_DISABLED',
      errorMessage: 'tenant disabled',
    })
  })

  it('falls back to the generic request error', () => {
    expect(parseRequestErrorDetails(undefined).errorMessage).toBe(
      'Request error occurred'
    )
  })
})

describe('buildCurlSnippet', () => {
  const payload: ChatCompletionRequest = {
    model: 'model-a',
    group: 'vip',
    stream: true,
    messages: [{ role: 'user', content: "it's fine" }],
  }

  it('targets the public relay, drops the playground-only group and escapes quotes', () => {
    const curl = buildCurlSnippet(payload, 'https://hub.example.test')
    expect(curl).toContain('curl https://hub.example.test/v1/chat/completions')
    expect(curl).toContain('Authorization: Bearer YOUR_API_KEY')
    expect(curl).not.toContain('"group"')
    expect(curl).toContain("it'\\''s fine")
  })

  it('does not mutate the payload it was given', () => {
    buildCurlSnippet(payload, 'https://hub.example.test')
    expect(payload.group).toBe('vip')
  })
})

describe('payload and fallbacks', () => {
  it('ships no baked-in model name', () => {
    expect(DEFAULT_CONFIG.model).toBe('')
  })

  it('only sends enabled parameters', () => {
    const body = buildChatCompletionPayload(
      [createUserMessage('hi')],
      { ...DEFAULT_CONFIG, model: 'model-a', temperature: 0.3 },
      { ...DEFAULT_PARAMETER_ENABLED, temperature: true, top_p: false }
    )
    expect(body.temperature).toBe(0.3)
    expect(body).not.toHaveProperty('top_p')
    expect(body).not.toHaveProperty('seed')
    expect(body.messages).toEqual([{ role: 'user', content: 'hi' }])
  })

  it('picks the first listed model and prefers the default group', () => {
    const models = [
      { label: 'model-a', value: 'model-a' },
      { label: 'model-b', value: 'model-b' },
    ]
    expect(getModelFallback(models, '')).toBe('model-a')
    expect(getModelFallback(models, 'model-b')).toBeNull()
    expect(getModelFallback([], '')).toBeNull()
    const groups = [
      { label: 'vip', value: 'vip' },
      { label: 'default', value: 'default' },
    ]
    expect(getGroupFallback(groups, 'gone')).toBe('default')
  })
})

describe('storage scope', () => {
  beforeEach(() => {
    window.localStorage.clear()
    setPlaygroundStorageScope('')
  })

  it('keeps one account preferences invisible to the next account', () => {
    setPlaygroundStorageScope('1')
    saveConfig({ ...DEFAULT_CONFIG, model: 'model-a', temperature: 0.2 })
    expect(loadConfig().temperature).toBe(0.2)

    setPlaygroundStorageScope('2')
    expect(loadConfig().temperature).toBeUndefined()

    setPlaygroundStorageScope('1')
    expect(loadConfig().temperature).toBe(0.2)
  })

  it('never reads the legacy console keys', () => {
    window.localStorage.setItem(
      'playground_config',
      JSON.stringify({ model: 'legacy-model' })
    )
    expect(loadConfig().model).toBeUndefined()
  })
})
