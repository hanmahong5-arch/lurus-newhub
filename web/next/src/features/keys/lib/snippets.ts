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
import type { ApiToken, RoutableModel } from '../types'

export const WIRE_OPENAI = 'openai'
export const WIRE_ANTHROPIC = 'anthropic'

/** Stand-in shown when the plaintext key is not available any more. */
export const KEY_PLACEHOLDER = 'YOUR_API_KEY'
export const MODEL_PLACEHOLDER = 'YOUR_MODEL'

/**
 * The host customers should call. Operator-configured `server_address` wins,
 * otherwise the origin the console is served from (so UAT advertises itself,
 * never production). Trailing slashes are stripped so `${host}/v1` is safe.
 */
export function relayHost(serverAddress: unknown, origin: string): string {
  const configured =
    typeof serverAddress === 'string' ? serverAddress.trim() : ''
  return (configured || origin).replace(/\/+$/, '')
}

export function clientEndpoints(host: string): Array<[string, string]> {
  return [
    ['Chat completions', `${host}/v1`],
    ['Messages', `${host}/v1/messages`],
    ['Native (v1beta)', `${host}/v1beta`],
  ]
}

/** Narrow routable models to a key's own allowlist (CSV) when it has one. */
export function limitToToken(
  models: RoutableModel[],
  token: Pick<ApiToken, 'model_limits_enabled' | 'model_limits'> | null
): RoutableModel[] {
  if (!token || !token.model_limits_enabled || !token.model_limits) {
    return models
  }
  const allowed = new Set(
    token.model_limits
      .split(',')
      .map((s) => s.trim())
      .filter(Boolean)
  )
  if (allowed.size === 0) return models
  return models.filter((m) => allowed.has(m.id))
}

export function firstModelFor(
  models: RoutableModel[],
  wire: string
): string | null {
  return (
    models.find((m) => m.supported_endpoint_types.includes(wire))?.id ?? null
  )
}

export type SnippetTab = 'curl' | 'python' | 'node' | 'anthropic'

export interface Snippets {
  curl: string
  python: string
  node: string
  /** Empty when no routable model speaks the messages wire. */
  anthropic: string
}

export function buildSnippets(
  key: string,
  host: string,
  chatModel: string,
  anthropicModel: string | null
): Snippets {
  return {
    curl: `curl ${host}/v1/chat/completions \\
  -H "Authorization: Bearer ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${chatModel}","messages":[{"role":"user","content":"hi"}]}'`,
    python: `import requests

resp = requests.post(
    "${host}/v1/chat/completions",
    headers={"Authorization": "Bearer ${key}"},
    json={
        "model": "${chatModel}",
        "messages": [{"role": "user", "content": "hi"}],
    },
)
print(resp.json()["choices"][0]["message"]["content"])`,
    node: `const resp = await fetch("${host}/v1/chat/completions", {
  method: "POST",
  headers: {
    Authorization: "Bearer ${key}",
    "Content-Type": "application/json",
  },
  body: JSON.stringify({
    model: "${chatModel}",
    messages: [{ role: "user", content: "hi" }],
  }),
});
console.log(await resp.json());`,
    anthropic: anthropicModel
      ? `curl ${host}/v1/messages \\
  -H "x-api-key: ${key}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${anthropicModel}","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}'`
      : '',
  }
}
