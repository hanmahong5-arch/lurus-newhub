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
import { CAPABILITIES } from '@/features/models/lib/catalog'

import type { EndpointInfo } from '../../api'

export type SampleLang = 'curl' | 'python' | 'javascript'
export const SAMPLE_LANGS: SampleLang[] = ['curl', 'python', 'javascript']

export const LANG_LABEL: Record<SampleLang, string> = {
  curl: 'cURL',
  python: 'Python',
  javascript: 'JavaScript',
}

export interface EndpointRow {
  type: string
  method: string
  path: string
}

/** `POST /v1/...` for the capability, preferring what the server reported. */
function splitPath(spec: string | undefined): { method: string; path: string } {
  const match = /^([A-Z]+)\s+(\S+)/.exec(spec ?? '')
  return match
    ? { method: match[1], path: match[2] }
    : { method: 'POST', path: spec ?? '' }
}

export function endpointRows(
  types: string[],
  reported: Record<string, EndpointInfo>
): EndpointRow[] {
  const rows: EndpointRow[] = []
  for (const type of types) {
    const server = reported[type]
    if (server?.path) {
      rows.push({ type, method: server.method || 'POST', path: server.path })
      continue
    }
    const fallback = CAPABILITIES[type]
    if (fallback) rows.push({ type, ...splitPath(fallback.path) })
  }
  return rows
}

/** Request body for the wire format; the prompt text is a placeholder. */
export function sampleBody(type: string, model: string): unknown {
  switch (type) {
    case 'openai-response':
      return { model, input: 'Hello' }
    case 'anthropic':
      return {
        model,
        max_tokens: 1024,
        messages: [{ role: 'user', content: 'Hello' }],
      }
    case 'gemini':
      return { contents: [{ role: 'user', parts: [{ text: 'Hello' }] }] }
    case 'embeddings':
      return { model, input: 'Hello' }
    case 'jina-rerank':
      return {
        model,
        query: 'Which document mentions billing?',
        documents: ['A note about billing.', 'A note about routing.'],
      }
    case 'image-generation':
      return { model, prompt: 'A lighthouse at dusk' }
    case 'openai-video':
      return { model, prompt: 'A lighthouse at dusk' }
    case 'systemone':
      return {
        model,
        state: 'Customer reports a failed payout.',
        questions: {
          is_urgent: { type: 'noul', instructions: 'Is this urgent?' },
        },
      }
    default:
      return { model, messages: [{ role: 'user', content: 'Hello' }] }
  }
}

export interface SampleInput {
  baseUrl: string
  model: string
  endpoint: EndpointRow
}

export function buildSample(lang: SampleLang, input: SampleInput): string {
  const { baseUrl, model, endpoint } = input
  const path = endpoint.path.replaceAll('{model}', model)
  const url = `${baseUrl}${path}`
  const body = sampleBody(endpoint.type, model)
  const json = JSON.stringify(body, null, 2)

  if (lang === 'curl') {
    return [
      `curl -X ${endpoint.method} "${url}" \\`,
      `  -H "Authorization: Bearer $API_KEY" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -d '${JSON.stringify(body)}'`,
    ].join('\n')
  }
  if (lang === 'python') {
    const indented = json
      .split('\n')
      .map((line, i) => (i === 0 ? line : `    ${line}`))
      .join('\n')
    return [
      'import os',
      'import requests',
      '',
      'response = requests.request(',
      `    "${endpoint.method}",`,
      `    "${url}",`,
      '    headers={"Authorization": f"Bearer {os.environ[\'API_KEY\']}"},',
      `    json=${indented},`,
      ')',
      'print(response.json())',
    ].join('\n')
  }
  const indentedJs = json
    .split('\n')
    .map((line, i) => (i === 0 ? line : `  ${line}`))
    .join('\n')
  return [
    `const response = await fetch("${url}", {`,
    `  method: "${endpoint.method}",`,
    '  headers: {',
    '    Authorization: `Bearer ${process.env.API_KEY}`,',
    '    "Content-Type": "application/json",',
    '  },',
    `  body: JSON.stringify(${indentedJs}),`,
    '})',
    'console.log(await response.json())',
  ].join('\n')
}
