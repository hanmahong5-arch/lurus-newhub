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
import type {
  ChatCompletionRequest,
  Message,
  PlaygroundConfig,
  ParameterEnabled,
} from '../../types'
import { formatMessageForAPI, isValidMessage } from '../message/message-utils'

/**
 * Build API request payload from messages and config
 */
export function buildChatCompletionPayload(
  messages: Message[],
  config: PlaygroundConfig,
  parameterEnabled: ParameterEnabled
): ChatCompletionRequest {
  // Filter and format valid messages
  const processedMessages = messages
    .filter(isValidMessage)
    .map(formatMessageForAPI)

  const payload: ChatCompletionRequest = {
    model: config.model,
    group: config.group,
    messages: processedMessages,
    stream: config.stream,
  }

  if (parameterEnabled.temperature) {
    payload.temperature = config.temperature
  }

  if (parameterEnabled.top_p) {
    payload.top_p = config.top_p
  }

  if (parameterEnabled.max_tokens) {
    payload.max_tokens = config.max_tokens
  }

  if (parameterEnabled.frequency_penalty) {
    payload.frequency_penalty = config.frequency_penalty
  }

  if (parameterEnabled.presence_penalty) {
    payload.presence_penalty = config.presence_penalty
  }

  if (parameterEnabled.seed && config.seed !== null) {
    payload.seed = config.seed
  }

  return payload
}

/**
 * The same call against the public relay. The playground-only `group` field
 * is dropped: on the public endpoint the API key decides the group.
 */
export function buildCurlSnippet(
  payload: ChatCompletionRequest,
  origin: string
): string {
  const { group: _group, ...body } = payload
  const backslash = String.fromCharCode(92)
  // A single quote inside the single-quoted body is closed, escaped, reopened.
  const json = JSON.stringify(body, null, 2).replaceAll(
    "'",
    `'${backslash}''`
  )
  // The console never holds a plaintext key, so the snippet carries a
  // placeholder the reader replaces.
  const apiKey = 'YOUR_API_KEY'
  return [
    `curl ${origin}/v1/chat/completions ${backslash}`,
    `  -H 'Content-Type: application/json' ${backslash}`,
    `  -H 'Authorization: Bearer ${apiKey}' ${backslash}`,
    `  -d '${json}'`,
  ].join('\n')
}
